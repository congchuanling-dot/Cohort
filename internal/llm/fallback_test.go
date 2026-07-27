package llm

import (
	"context"
	"errors"
	"testing"
)

type scriptedClient struct {
	calls  int
	err    error
	events []Event
}

func (c *scriptedClient) Chat(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	out := make(chan Event, len(c.events))
	for _, event := range c.events {
		out <- event
	}
	close(out)
	return out, nil
}

func collectEvents(t *testing.T, client Client) []Event {
	t.Helper()
	stream, err := client.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for event := range stream {
		events = append(events, event)
	}
	return events
}

func TestFallbackClientUsesNextProfileBeforeProgress_BitsUT(t *testing.T) {
	primary := &scriptedClient{events: []Event{{Type: EventError, Err: errors.New("rate limited")}}}
	secondary := &scriptedClient{events: []Event{
		{Type: EventText, Text: "ok"},
		{Type: EventDone, Response: &Response{Content: "ok"}},
	}}
	client, err := NewFallbackClient([]NamedClient{
		{Name: "primary", Client: primary},
		{Name: "secondary", Client: secondary},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, client)
	if primary.calls != 1 || secondary.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", primary.calls, secondary.calls)
	}
	if len(events) != 2 || events[0].Text != "ok" || events[1].Type != EventDone {
		t.Fatalf("events = %#v, want secondary text and done", events)
	}
}

func TestFallbackClientDoesNotReplayAfterText_BitsUT(t *testing.T) {
	primaryErr := errors.New("stream interrupted")
	primary := &scriptedClient{events: []Event{
		{Type: EventText, Text: "partial"},
		{Type: EventError, Err: primaryErr},
	}}
	secondary := &scriptedClient{events: []Event{{Type: EventDone, Response: &Response{Content: "secondary"}}}}
	client, err := NewFallbackClient([]NamedClient{
		{Name: "primary", Client: primary},
		{Name: "secondary", Client: secondary},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, client)
	if secondary.calls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.calls)
	}
	if len(events) != 2 || events[0].Text != "partial" || events[1].Type != EventError {
		t.Fatalf("events = %#v, want primary text then error", events)
	}
}

func TestFallbackClientDoesNotReplayAfterToolProgress_BitsUT(t *testing.T) {
	primary := &scriptedClient{events: []Event{
		{Type: EventError, Err: markModelProgress(errors.New("bad stream after tool_use"))},
	}}
	secondary := &scriptedClient{events: []Event{{Type: EventDone, Response: &Response{Content: "secondary"}}}}
	client, err := NewFallbackClient([]NamedClient{
		{Name: "primary", Client: primary},
		{Name: "secondary", Client: secondary},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, client)
	if secondary.calls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.calls)
	}
	if len(events) != 1 || events[0].Type != EventError {
		t.Fatalf("events = %#v, want primary error only", events)
	}
}
