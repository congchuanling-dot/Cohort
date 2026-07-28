package observability

import (
	"context"
	"time"
)

// EventBus 将同一个事件广播给多个 sink。
//
// 观测链路必须是旁路能力：任何 sink 写入失败都不能中断 Agent 主流程。
// 失败详情暂不回灌给模型，后续可增加 stderr/debug sink 记录内部错误。
type EventBus struct {
	sinks []Sink
}

func NewBus(sinks ...Sink) *EventBus {
	filtered := make([]Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &EventBus{sinks: filtered}
}

func (b *EventBus) Emit(ctx context.Context, event Event) {
	if b == nil {
		return
	}
	event = normalizeEvent(event)
	event = RedactEvent(event)
	for _, sink := range b.sinks {
		_ = sink.Emit(ctx, event)
	}
}

func (b *EventBus) Close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	for _, sink := range b.sinks {
		_ = sink.Close(ctx)
	}
	return nil
}

func normalizeEvent(event Event) Event {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = SchemaVersion
	}
	if event.EventID == "" {
		event.EventID = newEventID()
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.Severity == "" {
		event.Severity = SeverityInfo
	}
	return event
}
