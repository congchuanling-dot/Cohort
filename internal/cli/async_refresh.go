package cli

import (
	"fmt"
	"io"
	"sync"

	"cohort/internal/app"
	"cohort/internal/session"
	"cohort/internal/tuning"
)

// asyncTuningRefresher 串行、合并地刷新日常观测报告。
// Queue 不阻塞 REPL；容量为 1 的 channel 会把任务高峰合并成最多一次待处理刷新。
type asyncTuningRefresher struct {
	cfg       app.Config
	errOut    io.Writer
	queue     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newAsyncTuningRefresher(cfg app.Config, errOut io.Writer) *asyncTuningRefresher {
	if !cfg.Observability.AutoRefresh {
		return nil
	}
	refresher := &asyncTuningRefresher{
		cfg:    cfg,
		errOut: errOut,
		queue:  make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go refresher.run()
	return refresher
}

func (r *asyncTuningRefresher) Queue() {
	if r == nil {
		return
	}
	select {
	case r.queue <- struct{}{}:
	default:
	}
}

func (r *asyncTuningRefresher) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		close(r.queue)
		<-r.done
	})
}

func (r *asyncTuningRefresher) run() {
	defer close(r.done)
	for range r.queue {
		_, err := tuning.Generate(r.cfg.Workspace, tuning.Options{
			SessionRoot: session.DefaultRootDir,
			Limit:       r.cfg.Observability.AutoRefreshLimit,
		})
		if err != nil && r.errOut != nil {
			fmt.Fprintf(r.errOut, "\n[observability] auto refresh failed: %v\n", err)
		}
	}
}
