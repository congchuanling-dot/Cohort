package observability

import (
	"context"
	"sync"
	"time"
)

const defaultAsyncSinkCloseBudget = 500 * time.Millisecond

// AsyncSink 把慢外部观测 sink 放到后台队列里，避免网络上报阻塞 Agent 主流程。
// 队列满时会丢弃新事件；观测是旁路能力，不能反向拖慢模型流式输出。
type AsyncSink struct {
	inner   Sink
	events  chan Event
	closeMu sync.Mutex
	closed  bool
	wg      sync.WaitGroup
}

func NewAsyncSink(inner Sink, buffer int) *AsyncSink {
	if buffer <= 0 {
		buffer = 256
	}
	sink := &AsyncSink{
		inner:  inner,
		events: make(chan Event, buffer),
	}
	sink.wg.Add(1)
	go sink.run()
	return sink
}

func (s *AsyncSink) Emit(ctx context.Context, event Event) error {
	if s == nil || s.inner == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.events <- event:
	default:
	}
	return nil
}

func (s *AsyncSink) Close(ctx context.Context) error {
	if s == nil || s.inner == nil {
		return nil
	}
	s.closeMu.Lock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	s.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(defaultAsyncSinkCloseBudget)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-done:
		return s.inner.Close(ctx)
	}
}

func (s *AsyncSink) run() {
	defer s.wg.Done()
	for event := range s.events {
		_ = s.inner.Emit(context.Background(), event)
	}
}
