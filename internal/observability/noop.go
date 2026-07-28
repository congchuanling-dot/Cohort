package observability

import "context"

type NoopBus struct{}

func (NoopBus) Emit(ctx context.Context, event Event) {}

func (NoopBus) Close(ctx context.Context) error {
	return nil
}
