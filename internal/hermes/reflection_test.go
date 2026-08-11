package hermes

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchReflectionPreventsTickerReentry_BitsUT(t *testing.T) {
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	service.ReflectionRunner = func(context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}
	service.DispatchReflection(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reflection runner did not start")
	}
	service.DispatchReflection(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("reflection calls=%d, want one active dispatch", calls.Load())
	}
	close(release)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		service.mu.Lock()
		running := service.reflectionRunning
		service.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("reflection runner did not release dispatch state")
}
