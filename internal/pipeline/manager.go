package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"bincat/internal/checkpoint"
	"bincat/internal/config"
	"bincat/internal/meta"
	"bincat/internal/sink"
)

// Pipeline 单个数据源的完整处理链：canal → handler → dispatcher。
type Pipeline struct {
	ds       *meta.DataSource
	disp     *Dispatcher
	engine   *FilterEngine
	canal    *canal.Canal
	log      *slog.Logger
	cpStore  checkpoint.Store
	defaults config.PipelineDefaults

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	up     bool
}

// PipelineManager 管理全部数据源的 pipeline（增删启停，热更新核心）
type PipelineManager struct {
	store    *meta.Store
	sinkPool *sink.Pool
	cpStore  checkpoint.Store
	cipher   *meta.Cipher
	defaults config.PipelineDefaults
	shutdown config.ShutdownConfig
	log      *slog.Logger

	mu        sync.Mutex
	pipelines map[int64]*Pipeline
}

// NewManager 构造并按元数据库启动全部 enabled 数据源的 pipeline
func NewManager(ctx context.Context, store *meta.Store, cpStore checkpoint.Store,
	sinkPool *sink.Pool, cipher *meta.Cipher, defaults config.PipelineDefaults, shutdown config.ShutdownConfig, log *slog.Logger) (*PipelineManager, error) {
	m := &PipelineManager{
		store: store, sinkPool: sinkPool, cpStore: cpStore, cipher: cipher,
		defaults: defaults, shutdown: shutdown, log: log, pipelines: map[int64]*Pipeline{},
	}
	if err := m.reconcile(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// reconcile 数据源与 sink 全量对账（启动与后台变更后调用）
func (m *PipelineManager) reconcile(ctx context.Context) error {
	sinks, err := m.store.ListSinks(ctx)
	if err != nil {
		return fmt.Errorf("list sinks: %w", err)
	}
	if errs := m.sinkPool.Reconcile(ctx, sinks); len(errs) > 0 {
		for _, e := range errs {
			m.log.Error("sink reconcile", "err", e)
		}
	}
	dss, err := m.store.ListDataSources(ctx)
	if err != nil {
		return fmt.Errorf("list datasources: %w", err)
	}
	desired := map[int64]bool{}
	for _, ds := range dss {
		if ds.Enabled {
			desired[ds.ID] = true
		}
	}
	// 停掉移除/禁用的
	m.mu.Lock()
	var stopIDs []int64
	for id := range m.pipelines {
		if !desired[id] {
			stopIDs = append(stopIDs, id)
			delete(m.pipelines, id)
		}
	}
	m.mu.Unlock()
	for _, id := range stopIDs {
		if p := m.stopPipeline(id); p != nil {
			m.log.Info("pipeline stopped", "datasource_id", id)
		}
	}
	// 启动新增的
	for _, ds := range dss {
		if !ds.Enabled {
			continue
		}
		m.mu.Lock()
		_, running := m.pipelines[ds.ID]
		m.mu.Unlock()
		if running {
			continue
		}
		if err := m.startPipeline(ctx, ds); err != nil {
			m.log.Error("start pipeline failed", "datasource", ds.Name, "err", err)
		}
	}
	return nil
}

// startPipeline 建单源 pipeline 并异步跑（canal Run 阻塞，退避重启在 goroutine 内）
func (m *PipelineManager) startPipeline(ctx context.Context, ds *meta.DataSource) error {
	// 密码处理：配置了密钥则解密 PasswordEnc；否则 PasswordEnc 即明文（未启用加密的降级模式）
	if m.cipher != nil && len(ds.PasswordEnc) > 0 {
		pw, err := m.cipher.Decrypt(ds.PasswordEnc)
		if err != nil {
			return fmt.Errorf("decrypt datasource password: %w", err)
		}
		ds.Password = pw
	} else if ds.Password == "" {
		ds.Password = string(ds.PasswordEnc)
	}
	rules, err := m.store.ListRules(ctx, 0, ds.ID)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	sinks, err := m.store.ListSinks(ctx)
	if err != nil {
		return fmt.Errorf("list sinks: %w", err)
	}
	sinkMap := map[int64]*meta.Sink{}
	for _, s := range sinks {
		sinkMap[s.ID] = s
	}
	engine, err := NewFilterEngine(rules, sinkMap)
	if err != nil {
		return fmt.Errorf("compile rules: %w", err)
	}
	disp := NewDispatcher(ds.ID, m.defaults.DispatchQueueSize, m.defaults.AckWindowSize,
		m.sinkPool, m.cpStore, m.defaults.CheckpointFlushInterval, m.log.With("datasource", ds.Name))

	p := &Pipeline{
		ds: ds, disp: disp, engine: engine, cpStore: m.cpStore,
		defaults: m.defaults, log: m.log.With("datasource", ds.Name), done: make(chan struct{}),
	}
	pctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	if err := m.buildCanal(p); err != nil {
		cancel()
		disp.Close()
		return err
	}
	m.mu.Lock()
	m.pipelines[ds.ID] = p
	m.mu.Unlock()
	go m.runLoop(p, pctx)
	m.log.Info("pipeline started", "datasource", ds.Name, "server_id", ds.ServerID)
	return nil
}

// buildCanal 构造 canal 实例（含位点恢复逻辑）
func (m *PipelineManager) buildCanal(p *Pipeline) error {
	ds := p.ds
	cfg := canal.NewDefaultConfig()
	cfg.Addr = fmt.Sprintf("%s:%d", ds.Host, ds.Port)
	cfg.User = ds.User
	// 密码解密在 manager 外完成，这里直接用明文字段（meta.DataSource 增加 Password 字段）
	cfg.Password = ds.Password
	cfg.ServerID = ds.ServerID
	cfg.Flavor = "mysql"
	cfg.Dump.ExecutionPath = ""            // 禁用 mysqldump 全量
	cfg.DiscardNoMetaRowEvent = true
	cfg.UseDecimal = true
	cfg.ParseTime = true
	cfg.TimestampStringLocation = time.Local
	// 不设 IncludeTableRegex：表过滤自研，支持热更新加表

	c, err := canal.NewCanal(cfg)
	if err != nil {
		return fmt.Errorf("new canal: %w", err)
	}
	h := &binlogHandler{
		dsName: ds.Name, engine: p.engine, disp: p.disp, log: p.log,
		maxTxRows: m.defaults.MaxTxRows, onTxOverflow: m.defaults.OnTxOverflow,
	}
	// GTID 追踪：canal 的 handler 接口里 OnGTID 拿不到 set，用 pos handler 不可靠，
	// 改由 dispatcher 在保存时通过 checkpoint 查询上一次 GTID；M1 文件位点即可，
	// GTID 优先逻辑在 M2 完善（StartFromGTID）。
	c.SetEventHandler(h)
	p.canal = c
	return nil
}

// runLoop 运行 canal 并在错误后退避重启（无限，直到 stop）
func (m *PipelineManager) runLoop(p *Pipeline, ctx context.Context) {
	defer close(p.done)
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := p.run(ctx)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		p.log.Error("pipeline exited, restarting", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
		// 重建 canal（旧连接状态不可复用）
		p.canal.Close()
		if err := m.buildCanal(p); err != nil {
			p.log.Error("rebuild canal failed", "err", err)
			continue
		}
	}
}

// run 单次运行：恢复位点 → canal.Run
func (p *Pipeline) run(ctx context.Context) error {
	cp, err := p.cpStore.Load(ctx, p.ds.ID)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}
	pos := mysql.Position{Name: "", Pos: 0}
	if cp != nil {
		pos = mysql.Position{Name: cp.BinlogFile, Pos: cp.BinlogPos}
		p.log.Info("resume from checkpoint", "file", cp.BinlogFile, "pos", cp.BinlogPos)
	} else {
		m, err := p.canal.GetMasterPos()
		if err != nil {
			return fmt.Errorf("get master pos: %w", err)
		}
		pos = m
		p.log.Info("first start, begin from current master pos", "file", pos.Name, "pos", pos.Pos)
	}
	go func() {
		<-ctx.Done()
		p.canal.Close()
	}()
	return p.canal.RunFrom(pos)
}

// stopPipeline 停止并排空单源 pipeline；返回被停的 pipeline（不存在返回 nil）
func (m *PipelineManager) stopPipeline(id int64) *Pipeline {
	m.mu.Lock()
	p, ok := m.pipelines[id]
	if ok {
		delete(m.pipelines, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	p.Stop(m.shutdown.DrainTimeout)
	return p
}

// Stop 优雅停止单 pipeline：停拉取 → drain → 强刷位点 → 关 canal
func (p *Pipeline) Stop(drainTimeout time.Duration) {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
	if err := p.disp.Drain(drainTimeout); err != nil {
		p.log.Warn("drain timeout", "err", err)
	}
	p.canal.Close()
	p.disp.Close()
}

// StopAll 停止全部 pipeline（进程停机用）
func (m *PipelineManager) StopAll() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.pipelines))
	for id := range m.pipelines {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if p := m.stopPipeline(id); p != nil {
				m.log.Info("pipeline stopped", "datasource_id", id)
			}
		}(id)
	}
	wg.Wait()
}

// ReloadRules 规则热更新：重编译指定源的过滤引擎（binlog 流不断）
func (m *PipelineManager) ReloadRules(ctx context.Context, datasourceID int64) error {
	rules, err := m.store.ListRules(ctx, 0, datasourceID)
	if err != nil {
		return err
	}
	sinks, err := m.store.ListSinks(ctx)
	if err != nil {
		return err
	}
	sinkMap := map[int64]*meta.Sink{}
	for _, s := range sinks {
		sinkMap[s.ID] = s
	}
	m.mu.Lock()
	p, ok := m.pipelines[datasourceID]
	m.mu.Unlock()
	if !ok {
		return nil // 源未运行，下次启动生效
	}
	if err := p.engine.Reload(rules, sinkMap); err != nil {
		return fmt.Errorf("reload rules: %w", err)
	}
	m.log.Info("rules reloaded", "datasource_id", datasourceID)
	return nil
}

// Reconcile 对外暴露的对账入口（后台数据源/sink 变更后调用）
func (m *PipelineManager) Reconcile(ctx context.Context) error { return m.reconcile(ctx) }
