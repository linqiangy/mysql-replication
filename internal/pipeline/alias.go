package pipeline

import (
	"github.com/go-mysql-org/go-mysql/replication"
)

// replicationRotateEvent 事件类型别名（handler OnRotate 使用 DummyEventHandler 默认实现）
type replicationRotateEvent = replication.RotateEvent
