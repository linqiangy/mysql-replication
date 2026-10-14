package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// ConsoleSink 调试用 sink：JSON 逐行打印到 stdout（M1 验收通道）
type ConsoleSink struct {
	name   string
	out    *os.File
	pretty bool
}

// newConsoleSink 工厂：params 可选 pretty=true 美化输出
func newConsoleSink(cfg *meta.Sink) (Sink, error) {
	pretty := false
	if v, ok := cfg.Params["pretty"].(bool); ok {
		pretty = v
	}
	return &ConsoleSink{name: cfg.Name, out: os.Stdout, pretty: pretty}, nil
}

// Name 实现 Sink
func (c *ConsoleSink) Name() string { return c.name }

// Type 实现 Sink
func (c *ConsoleSink) Type() string { return "console" }

// Open 实现 Sink
func (c *ConsoleSink) Open(ctx context.Context) error { return nil }

// SendBatch 实现 Sink：逐行 JSON 打印，永不失败（console 无远端）
func (c *ConsoleSink) SendBatch(ctx context.Context, events []*model.ChangeEvent) error {
	for _, ev := range events {
		var b []byte
		var err error
		if c.pretty {
			b, err = json.MarshalIndent(ev, "", "  ")
		} else {
			b, err = json.Marshal(ev)
		}
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		fmt.Fprintln(c.out, string(b))
	}
	return nil
}

// Close 实现 Sink
func (c *ConsoleSink) Close(ctx context.Context) error { return nil }

func init() { Register("console", newConsoleSink) }
