package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// KafkaSink Kafka 投递：acks=all + 按主键哈希分区（同行变更有序）。
type KafkaSink struct {
	name          string
	writer        *kafka.Writer
	topicTemplate string // 含 {{schema}}/{{table}} 占位时启用动态 topic；否则 Writer.Topic
}

// newKafkaSink 工厂：params = { brokers:[], topic, ack?(all/one/none), timeout_seconds?, sasl_user?, sasl_password? }
func newKafkaSink(cfg *meta.Sink) (Sink, error) {
	var brokers []string
	switch b := cfg.Params["brokers"].(type) {
	case []any:
		for _, v := range b {
			brokers = append(brokers, fmt.Sprintf("%v", v))
		}
	case []string:
		brokers = b
	case string:
		brokers = strings.Split(b, ",")
	}
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka sink %q: params.brokers is required", cfg.Name)
	}
	topic, _ := cfg.Params["topic"].(string)
	acks := kafka.RequireAll
	switch a, _ := cfg.Params["ack"].(string); a {
	case "one":
		acks = kafka.RequireOne
	case "none":
		acks = kafka.RequireNone
	}
	var mech sasl.Mechanism
	if u, ok := cfg.Params["sasl_user"].(string); ok && u != "" {
		p, _ := cfg.Params["sasl_password"].(string)
		mech = plain.Mechanism{Username: u, Password: p}
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{},
		RequiredAcks: acks,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 10 * time.Second,
	}
	if mech != nil {
		w.Transport = &kafka.Transport{SASL: mech}
	}
	var tmpl string
	if strings.Contains(topic, "{{") {
		tmpl = topic
	} else {
		w.Topic = topic
	}
	return &KafkaSink{name: cfg.Name, writer: w, topicTemplate: tmpl}, nil
}

// Name 实现 Sink
func (k *KafkaSink) Name() string { return k.name }

// Type 实现 Sink
func (k *KafkaSink) Type() string { return "kafka" }

// Open 实现 Sink：验证 broker 地址非空（实际连接由首批发送建立）
func (k *KafkaSink) Open(ctx context.Context) error {
	if k.writer.Addr == nil {
		return fmt.Errorf("kafka sink %q: no brokers", k.name)
	}
	return nil
}

// SendBatch 实现 Sink：kafka-go 内部自动合并批量发送
func (k *KafkaSink) SendBatch(ctx context.Context, events []*model.ChangeEvent) error {
	if len(events) == 0 {
		return nil
	}
	msgs := make([]kafka.Message, 0, len(events))
	for _, ev := range events {
		body, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		topic := k.writer.Topic
		if k.topicTemplate != "" {
			topic = strings.ReplaceAll(k.topicTemplate, "{{schema}}", ev.Schema)
			topic = strings.ReplaceAll(topic, "{{table}}", ev.Table)
		}
		msgs = append(msgs, kafka.Message{
			Topic: topic,
			Key:   []byte(pkKey(ev)),
			Value: body,
		})
	}
	return k.writer.WriteMessages(ctx, msgs...)
}

// Close 实现 Sink
func (k *KafkaSink) Close(ctx context.Context) error { return k.writer.Close() }

// pkKey 主键值拼 key（同主键同分区保证行级有序；无主键退化为表名+事件id）
func pkKey(ev *model.ChangeEvent) string {
	row := ev.After
	if row == nil {
		row = ev.Before
	}
	if len(ev.PK) > 0 && row != nil {
		vals := make([]string, 0, len(ev.PK))
		for _, col := range ev.PK {
			if v, ok := row[col]; ok {
				vals = append(vals, fmt.Sprintf("%v", v))
			}
		}
		if len(vals) > 0 {
			return strings.Join(vals, "/")
		}
	}
	return fmt.Sprintf("%s.%s:%s", ev.Schema, ev.Table, ev.ID)
}

func init() { Register("kafka", newKafkaSink) }
