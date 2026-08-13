package replay

import (
	"context"
	"testing"

	"cohort/internal/llm"
)

type liveClient struct {
	calls int
}

func (c *liveClient) Chat(_ context.Context, _ llm.ChatRequest) (<-chan llm.Event, error) {
	c.calls++
	stream := make(chan llm.Event, 1)
	stream <- llm.Event{Type: llm.EventDone, Response: &llm.Response{Content: "live"}}
	close(stream)
	return stream, nil
}

func TestForkClientReplaysPrefixThenDelegates(t *testing.T) {
	request := llm.ChatRequest{
		System:   "system",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "task"}},
	}
	plan := ForkPlan{
		ForkTurn: 2,
		Requests: map[int]Request{1: {
			System:   request.System,
			Messages: request.Messages,
			Hash:     requestHash(request),
		}},
		Responses: map[int]Response{1: {Content: "recorded"}},
	}
	live := &liveClient{}
	client := NewForkClient(plan, live)

	stream, err := client.Chat(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first := <-stream
	if first.Type != llm.EventText || first.Text != "recorded" {
		t.Fatalf("unexpected recorded event: %+v", first)
	}
	stream, err = client.Chat(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second := <-stream
	if second.Type != llm.EventDone || second.Response.Content != "live" || live.calls != 1 {
		t.Fatalf("live suffix was not delegated: event=%+v calls=%d", second, live.calls)
	}
}

func TestForkClientRejectsPrefixDrift(t *testing.T) {
	plan := ForkPlan{
		ForkTurn:  2,
		Requests:  map[int]Request{1: {Hash: "expected"}},
		Responses: map[int]Response{1: {Content: "recorded"}},
	}
	client := NewForkClient(plan, &liveClient{})
	stream, err := client.Chat(context.Background(), llm.ChatRequest{System: "different"})
	if err != nil {
		t.Fatal(err)
	}
	event := <-stream
	if event.Type != llm.EventError || event.Err == nil {
		t.Fatalf("expected replay divergence, got %+v", event)
	}
}
