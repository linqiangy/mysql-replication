package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// RabbitMQSink AMQP 投递：publisher confirm 确认；exchange 声明（declare=true）。
// 连接断开时返回错误由上层重试（下次 SendBatch 内部重连）。
type RabbitMQSink struct {
	name      string
	url       string
	exchange  string
	declare   bool
	confirm   bool
	routeTmpl string // 支持 {{schema}}/{{table}}/{{action}} 占位

	mu       sync.Mutex
	conn     *amqp.Connection
	ch       *amqp.Channel
	notifyClose chan *amqp.Error
}

// newRabbitMQSink 工厂：params = { url, exchange, declare?, confirm?, routing_key? }
func newRabbitMQSink(cfg *meta.Sink) (Sink, error) {
	urlStr, _ := cfg.Params["url"].(string)
	if urlStr == "" {
		return nil, fmt.Errorf("rabbitmq sink %q: params.url is required", cfg.Name)
	}
	exchange, _ := cfg.Params["exchange"].(string)
	declare := true
	if v, ok := cfg.Params["declare"].(bool); ok {
		declare = v
	}
	confirm := true
	if v, ok := cfg.Params["confirm"].(bool); ok {
		confirm = v
	}
	route, _ := cfg.Params["routing_key"].(string)
	if route == "" {
		route = "bincat.{{schema}}.{{table}}.{{action}}"
	}
	return &RabbitMQSink{
		name: cfg.Name, url: urlStr, exchange: exchange,
		declare: declare, confirm: confirm, routeTmpl: route,
	}, nil
}

// Name 实现 Sink
func (r *RabbitMQSink) Name() string { return r.name }

// Type 实现 Sink
func (r *RabbitMQSink) Type() string { return "rabbitmq" }

// Open 实现 Sink：建立连接与 channel
func (r *RabbitMQSink) Open(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil && !r.conn.IsClosed() {
		return nil
	}
	return r.connectLocked()
}

// connectLocked 建立连接（调用方持锁）
func (r *RabbitMQSink) connectLocked() error {
	conn, err := amqp.Dial(r.url)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("amqp channel: %w", err)
	}
	if r.declare && r.exchange != "" {
		if err := ch.ExchangeDeclare(r.exchange, "topic", true, false, false, false, nil); err != nil {
			conn.Close()
			return fmt.Errorf("declare exchange: %w", err)
		}
	}
	if r.confirm {
		if err := ch.Confirm(false); err != nil {
			conn.Close()
			return fmt.Errorf("enable confirm: %w", err)
		}
	}
	r.conn, r.ch = conn, ch
	r.notifyClose = ch.NotifyClose(make(chan *amqp.Error, 1))
	return nil
}

// ensureConn 确保连接可用（断开则重连）
func (r *RabbitMQSink) ensureConn() error {
	if r.conn != nil && !r.conn.IsClosed() && r.ch != nil {
		return nil
	}
	return r.connectLocked()
}

// SendBatch 实现 Sink：逐事件发布，publisher confirm 等待确认
func (r *RabbitMQSink) SendBatch(ctx context.Context, events []*model.ChangeEvent) error {
	if len(events) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureConn(); err != nil {
		return err
	}
	confirmCh := r.ch.NotifyPublish(make(chan amqp.Confirmation, len(events)))
	for _, ev := range events {
		body, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		rk := renderRoutingKey(r.routeTmpl, ev)
		if err := r.ch.PublishWithContext(ctx, r.exchange, rk, false, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			Body:         body,
		}); err != nil {
			return fmt.Errorf("publish: %w", err)
		}
	}
	if r.confirm {
		for range events {
			select {
			case c, ok := <-confirmCh:
				if !ok {
					return fmt.Errorf("confirm channel closed (connection lost)")
				}
				if !c.Ack {
					return fmt.Errorf("nack from broker")
				}
			case <-ctx.Done():
				return ctx.Err()
			case <-r.notifyClose:
				return fmt.Errorf("connection closed while awaiting confirm")
			}
		}
	}
	return nil
}

// Close 实现 Sink
func (r *RabbitMQSink) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ch != nil {
		r.ch.Close()
	}
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// renderRoutingKey 渲染路由键占位符
func renderRoutingKey(tmpl string, ev *model.ChangeEvent) string {
	s := strings.ReplaceAll(tmpl, "{{schema}}", ev.Schema)
	s = strings.ReplaceAll(s, "{{table}}", ev.Table)
	s = strings.ReplaceAll(s, "{{action}}", ev.Action)
	s = strings.ReplaceAll(s, "{{datasource}}", ev.Datasource)
	return s
}

func init() { Register("rabbitmq", newRabbitMQSink) }
