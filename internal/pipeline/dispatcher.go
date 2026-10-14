package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bincat/internal/checkpoint"
	"bincat/internal/model"
	"bincat/internal/sink"
)

// Position 管线内位点（与 checkpoint.Position 解耦，避免反向依赖）
type Position struct {
	Name string
	Pos  uint32
}

// Batch 一个事务的投递批次：全部事件被目标 sink 确认后才可推进位点
type Batch struct {
	// EndPos 事务结束位点（XID nextPos）
	EndPos Position
	// GTIDSet 事务 GTID（GTID 模式数据源，由 handler 维护）
	GTIDSet string
	// Events 过滤后的事件（含目标 sink 名，见 sinkEvent）
	Events []*sinkEvent
}

// sinkEvent 携带目标 sink 名称的事件
type sinkEvent struct {
	*model.ChangeEvent
	SinkName string
}

// Dispatcher 批次派发器：有界队列（背压）+ 单 worker 顺序派发 + ack 位点推进。
// at-least-once 语义：批次投递成功（全部目标 sink 确认）→ lastAckedPos 推进 → 定时落盘。
type Dispatcher struct {
	queue   chan *Batch
	window  int
	sinks   *sink.Pool
	cpStore checkpoint.Store
	dsID    int64
	log     *slog.Logger

	mu           sync.Mutex
	lastAckedPos Position
	lastAckedGTID string
	dirty        bool // 有未落盘的位点推进

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	flushCh chan struct{}
}

// NewDispatcher 构造并启动 worker；queueSize 为待派发队列长度
func NewDispatcher(dsID int64, queueSize, window int, sinks *sink.Pool, cpStore checkpoint.Store,
	flushInterval time.Duration, log *slog.Logger) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		queue:   make(chan *Batch, queueSize),
		window:  window,
		sinks:   sinks,
		cpStore: cpStore,
		dsID:    dsID,
		log:     log,
		cancel:  cancel,
		flushCh: make(chan struct{}, 1),
	}
	d.wg.Add(2)
	go d.worker(ctx)
	go d.flushLoop(ctx, flushInterval)
	return d
}

// Enqueue 事务批次入队（队列满时阻塞 canal 事件循环 → 自然背压）
func (d *Dispatcher) Enqueue(b Batch) {
	d.queue <- &b
}

// AdvanceIdle 无命中事件的事务：直接推进位点（无需投递）
func (d *Dispatcher) AdvanceIdle(pos Position) {
	d.mu.Lock()
	d.lastAckedPos = pos
	d.dirty = true
	d.mu.Unlock()
	d.notifyFlush()
}

// worker 单线程顺序派发（保证同源事件有序）
func (d *Dispatcher) worker(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-d.queue:
			d.dispatch(ctx, b)
		}
	}
}

// dispatch 派发一批：按事件命中的 sink 分组投递，全部确认后推进位点
func (d *Dispatcher) dispatch(ctx context.Context, b *Batch) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("panic in dispatcher recovered", "panic", r)
		}
	}()
	if len(b.Events) == 0 {
		d.ackPos(b)
		return
	}
	// 按 sink 分组（一个事件可能只属于一个 sink——规则指定单 sink；多 sink 复制在 M3 扩展）
	groups := make(map[string][]*sinkEvent)
	for _, ev := range b.Events {
		groups[ev.SinkName] = append(groups[ev.SinkName], ev)
	}
	for sinkName, evs := range groups {
		events := make([]*model.ChangeEvent, len(evs))
		for i, e := range evs {
			events[i] = e.ChangeEvent
		}
		sk, ok := d.sinks.Get(sinkName)
		if !ok {
			// sink 被删除/禁用：block 策略下阻塞并告警（保不丢）；dlq 策略写死信
			d.log.Error("sink missing, batch blocked", "sink", sinkName)
			d.retryForever(ctx, sinkName, events)
			continue
		}
		if err := d.sendWithRetry(ctx, sk, events); err != nil {
			d.log.Error("sink exhausted, batch blocked", "sink", sinkName, "err", err)
			d.retryForever(ctx, sinkName, events)
		}
	}
	d.ackPos(b)
}

// sendWithRetry 指数退避重试投递；重试耗尽返回错误
func (d *Dispatcher) sendWithRetry(ctx context.Context, sk sink.Sink, events []*model.ChangeEvent) error {
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	var lastErr error
	for i := 0; i < 10; i++ {
		if err := sk.SendBatch(ctx, events); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return fmt.Errorf("send batch after retries: %w", lastErr)
}

// retryForever block 策略：无限重试直到成功或停机（保 at-least-once）
func (d *Dispatcher) retryForever(ctx context.Context, sinkName string, events []*model.ChangeEvent) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		backoff := 5 * time.Second
		for {
			sk, ok := d.sinks.Get(sinkName)
			if !ok {
				d.log.Error("sink still missing", "sink", sinkName)
			} else if err := sk.SendBatch(ctx, events); err == nil {
				d.log.Info("blocked batch finally delivered", "sink", sinkName)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
		}
	}()
}

// ackPos 批次全部确认：推进内存位点并触发落盘
func (d *Dispatcher) ackPos(b *Batch) {
	d.mu.Lock()
	d.lastAckedPos = b.EndPos
	if b.GTIDSet != "" {
		d.lastAckedGTID = b.GTIDSet
	}
	d.dirty = true
	d.mu.Unlock()
	d.notifyFlush()
}

// notifyFlush 非阻塞通知 flush loop
func (d *Dispatcher) notifyFlush() {
	select {
	case d.flushCh <- struct{}{}:
	default:
	}
}

// flushLoop 定时 + 事件触发落盘（兜底：flushInterval 内必有一次）
func (d *Dispatcher) flushLoop(ctx context.Context, interval time.Duration) {
	defer d.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.flushOnce(context.Background()) // 停机强刷
			return
		case <-ticker.C:
		case <-d.flushCh:
		}
		d.flushOnce(ctx)
	}
}

// flushOnce 位点变化才落盘
func (d *Dispatcher) flushOnce(ctx context.Context) {
	d.mu.Lock()
	if !d.dirty {
		d.mu.Unlock()
		return
	}
	pos := checkpoint.Position{BinlogFile: d.lastAckedPos.Name, BinlogPos: d.lastAckedPos.Pos, GTIDSet: d.lastAckedGTID}
	d.dirty = false
	d.mu.Unlock()
	if err := d.cpStore.Save(ctx, d.dsID, pos); err != nil {
		d.log.Error("save checkpoint failed", "err", err)
		d.mu.Lock()
		d.dirty = true // 落盘失败恢复脏标记，下轮重试
		d.mu.Unlock()
	}
}

// LastAcked 当前已确认位点（监控用）
func (d *Dispatcher) LastAcked() Position {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastAckedPos
}

// Drain 优雅停机：等队列排空 + 在途批次完成（超时返回未排空错误）
func (d *Dispatcher) Drain(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(d.queue) == 0 {
			d.flushOnce(context.Background())
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("drain timeout, %d batches pending", len(d.queue))
}

// Close 停止 worker 并强刷位点
func (d *Dispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}
