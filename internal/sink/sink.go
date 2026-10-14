// Package sink 定义投递目标抽象：console / http / kafka / rabbitmq 可插拔，
// 由 registry 工厂按类型创建，Pool 统一持有与热更新（Reconcile）。
package sink

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// Sink 投递目标接口。SendBatch 阻塞直至本批全部确认（broker 端 ack）
// 或重试耗尽返回错误——由调用方决定 block / dlq 策略。
type Sink interface {
	// Name sink 名称（元数据库唯一）
	Name() string
	// Type 类型：console/http/kafka/rabbitmq
	Type() string
	// Open 建立连接（启动与 Reconcile 新建时调用）
	Open(ctx context.Context) error
	// SendBatch 阻塞投递一批事件；nil = 服务端已确认
	SendBatch(ctx context.Context, events []*model.ChangeEvent) error
	// Close 排空并关闭连接
	Close(ctx context.Context) error
}

// registry 工厂注册表
var (
	registryMu sync.RWMutex
	registry   = map[string]func(cfg *meta.Sink) (Sink, error){}
)

// Register 注册 sink 工厂（init 时调用）
func Register(typ string, factory func(cfg *meta.Sink) (Sink, error)) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[typ] = factory
}

// Build 按配置构建 sink（不 Open）
func Build(cfg *meta.Sink) (Sink, error) {
	registryMu.RLock()
	f, ok := registry[cfg.Type]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown sink type %q", cfg.Type)
	}
	return f(cfg)
}

// Pool 全局 sink 连接池：按名称持有已 Open 的 sink 实例；
// Reconcile 对比期望配置做增量新建/关闭（热更新），在途批由 dispatcher 自然排空。
type Pool struct {
	mu    sync.RWMutex
	sinks map[string]Sink
}

// NewPool 构造空池
func NewPool() *Pool { return &Pool{sinks: map[string]Sink{}} }

// Get 按名称取运行中的 sink
func (p *Pool) Get(name string) (Sink, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.sinks[name]
	return s, ok
}

// Reconcile 期望配置（元数据库全部 enabled sink）与当前池 diff：
// 新增/变更 → 新建 Open 成功后替换（失败保留旧实例并返回错误列表）；
// 移除 → 从池中摘除并 Close。返回处理过程中的错误。
func (p *Pool) Reconcile(ctx context.Context, desired []*meta.Sink) []error {
	var errs []error
	desiredMap := map[string]*meta.Sink{}
	for _, d := range desired {
		if d.Enabled {
			desiredMap[d.Name] = d
		}
	}
	// 关闭移除的
	p.mu.Lock()
	var toClose []Sink
	for name, s := range p.sinks {
		if _, want := desiredMap[name]; !want {
			delete(p.sinks, name)
			toClose = append(toClose, s)
		}
	}
	p.mu.Unlock()
	for _, s := range toClose {
		if err := s.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close sink %s: %w", s.Name(), err))
		}
	}
	// 新建/校验保留的
	for name, d := range desiredMap {
		if _, exists := p.sinks[name]; exists {
			continue // 变更检测（params diff）在 M2 以指纹比对实现，M1 先全量重启语义
		}
		ns, err := Build(d)
		if err != nil {
			errs = append(errs, fmt.Errorf("build sink %s: %w", name, err))
			continue
		}
		if err := ns.Open(ctx); err != nil {
			errs = append(errs, fmt.Errorf("open sink %s: %w", name, err))
			continue
		}
		p.mu.Lock()
		p.sinks[name] = ns
		p.mu.Unlock()
	}
	return errs
}

// CloseAll 关闭全部 sink（停机）
func (p *Pool) CloseAll(ctx context.Context) {
	p.mu.Lock()
	names := make([]string, 0, len(p.sinks))
	for n := range p.sinks {
		names = append(names, n)
	}
	snapshot := make([]Sink, 0, len(p.sinks))
	for _, s := range p.sinks {
		snapshot = append(snapshot, s)
	}
	p.sinks = map[string]Sink{}
	p.mu.Unlock()
	sort.Strings(names)
	for _, s := range snapshot {
		if err := s.Close(ctx); err != nil {
			_ = err // 停机尽力关闭
		}
	}
}
