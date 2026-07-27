package llm

import (
	"context"
	"errors"
	"fmt"
)

// NamedClient 是 fallback 链中的一个模型客户端。
type NamedClient struct {
	// Name 是可读名称，用于错误提示。
	Name string
	// Client 是实际模型客户端。
	Client Client
}

// FallbackClient 按顺序尝试多个模型客户端。
//
// 只有当前客户端在没有任何可见文本、且底层没有收到 tool_call/tool_use 增量时失败，
// 才会切换到下一个客户端，避免同一轮模型输出被重放。
type FallbackClient struct {
	clients []NamedClient
}

// NewFallbackClient 创建 fallback 包装器。少于两个客户端时直接返回原客户端。
func NewFallbackClient(clients []NamedClient) (Client, error) {
	filtered := make([]NamedClient, 0, len(clients))
	for _, client := range clients {
		if client.Client == nil {
			continue
		}
		filtered = append(filtered, client)
	}
	if len(filtered) == 0 {
		return nil, errors.New("llm fallback chain has no clients")
	}
	if len(filtered) == 1 {
		return filtered[0].Client, nil
	}
	return &FallbackClient{clients: filtered}, nil
}

// Chat 实现 Client 接口。
func (c *FallbackClient) Chat(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		var lastErr error
	profileLoop:
		for i, client := range c.clients {
			stream, err := client.Client.Chat(ctx, req)
			if err != nil {
				lastErr = fmt.Errorf("llm profile %q failed to start: %w", client.Name, err)
				if i+1 < len(c.clients) && canReplayAfterError(err, false) {
					continue
				}
				out <- Event{Type: EventError, Err: lastErr}
				return
			}
			emittedText := false
			completed := false
			for event := range stream {
				switch event.Type {
				case EventText:
					if event.Text != "" {
						emittedText = true
					}
					out <- event
				case EventDone:
					out <- event
					completed = true
				case EventError:
					if event.Err != nil {
						lastErr = fmt.Errorf("llm profile %q failed: %w", client.Name, event.Err)
					} else {
						lastErr = fmt.Errorf("llm profile %q failed", client.Name)
					}
					if i+1 < len(c.clients) && canReplayAfterError(event.Err, emittedText) {
						continue profileLoop
					}
					out <- Event{Type: EventError, Err: lastErr}
					return
				}
				if completed {
					return
				}
			}
			if completed {
				return
			}
			lastErr = fmt.Errorf("llm profile %q stream closed without done event", client.Name)
			if i+1 < len(c.clients) && !emittedText {
				continue
			}
			out <- Event{Type: EventError, Err: lastErr}
			return
		}
		if lastErr == nil {
			lastErr = errors.New("llm fallback chain exhausted")
		}
		out <- Event{Type: EventError, Err: lastErr}
	}()
	return out, nil
}

func canReplayAfterError(err error, emittedText bool) bool {
	if emittedText {
		return false
	}
	return !hasModelProgress(err)
}

type modelProgressError struct {
	err error
}

func (e modelProgressError) Error() string {
	return e.err.Error()
}

func (e modelProgressError) Unwrap() error {
	return e.err
}

func markModelProgress(err error) error {
	if err == nil || hasModelProgress(err) {
		return err
	}
	return modelProgressError{err: err}
}

func hasModelProgress(err error) bool {
	var progress modelProgressError
	return errors.As(err, &progress)
}
