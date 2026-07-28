package observability

import "context"

// Sink 是观测事件的消费端。实现必须把失败限制在自身内部或返回 error；
// EventBus 会吞掉 sink error，避免观测系统影响 Agent 主流程。
type Sink interface {
	Emit(ctx context.Context, event Event) error
	Close(ctx context.Context) error
}

// Bus 是 Runner 依赖的最小事件总线接口。
type Bus interface {
	Emit(ctx context.Context, event Event)
	Close(ctx context.Context) error
}
