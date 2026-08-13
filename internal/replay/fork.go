package replay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cohort/internal/llm"
)

type ForkPlan struct {
	Manifest      Manifest
	Frames        []Frame
	ForkTurn      int
	Input         string
	PrefixHistory []llm.Message
	Requests      map[int]Request
	Responses     map[int]Response
	Tools         map[string]ToolResult
}

func BuildForkPlan(sessionRoot, sessionID, runID string, forkTurn int) (ForkPlan, error) {
	if forkTurn <= 0 {
		return ForkPlan{}, errors.New("fork turn must be positive")
	}
	manifest, frames, err := LoadBundle(sessionRoot, sessionID, runID)
	if err != nil {
		return ForkPlan{}, err
	}
	exact := ExactResult{
		SessionID:       manifest.SessionID,
		RunID:           manifest.RunID,
		Replayability:   manifest.Replayability,
		FirstDivergence: verifyManifest(manifest, frames),
	}
	if exact.FirstDivergence != nil {
		return ForkPlan{}, ValidateExact(exact)
	}
	plan := ForkPlan{
		Manifest:  manifest,
		Frames:    frames,
		ForkTurn:  forkTurn,
		Requests:  map[int]Request{},
		Responses: map[int]Response{},
		Tools:     map[string]ToolResult{},
	}
	maxTurn := 0
	for _, frame := range frames {
		if frame.Turn > maxTurn {
			maxTurn = frame.Turn
		}
		switch frame.Kind {
		case FrameRequest:
			plan.Requests[frame.Turn] = *frame.Request
		case FrameResponse:
			plan.Responses[frame.Turn] = *frame.Response
		case FrameTool:
			key := ToolFrameKey(frame.Turn, frame.Tool.Index, frame.Tool.Call.ID)
			plan.Tools[key] = *frame.Tool
		}
	}
	if forkTurn > maxTurn+1 {
		return ForkPlan{}, fmt.Errorf("fork turn %d is beyond recorded run with %d turns", forkTurn, maxTurn)
	}
	first, ok := plan.Requests[1]
	if !ok {
		return ForkPlan{}, errors.New("recorded run has no first request")
	}
	inputIndex := -1
	for index, message := range first.Messages {
		if message.Role == llm.RoleUser && StableHash(message.Content) == manifest.InputHash {
			inputIndex = index
		}
	}
	if inputIndex < 0 {
		return ForkPlan{}, errors.New("cannot locate original input in first request")
	}
	plan.Input = first.Messages[inputIndex].Content
	plan.PrefixHistory = append([]llm.Message(nil), first.Messages[:inputIndex]...)
	return plan, nil
}

func ToolFrameKey(turn, index int, callID string) string {
	return fmt.Sprintf("%d:%d:%s", turn, index, strings.TrimSpace(callID))
}

type ForkClient struct {
	mu       sync.Mutex
	Plan     ForkPlan
	Live     llm.Client
	nextTurn int
}

func NewForkClient(plan ForkPlan, live llm.Client) *ForkClient {
	return &ForkClient{Plan: plan, Live: live, nextTurn: 1}
}

func (c *ForkClient) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Event, error) {
	c.mu.Lock()
	turn := c.nextTurn
	c.nextTurn++
	c.mu.Unlock()
	if turn >= c.Plan.ForkTurn {
		if c.Live == nil {
			return nil, errors.New("fork replay reached live suffix without an llm client")
		}
		return c.Live.Chat(ctx, req)
	}
	recordedRequest, ok := c.Plan.Requests[turn]
	if !ok {
		return replayErrorStream(fmt.Errorf("recorded request for turn %d is missing", turn)), nil
	}
	actualHash := requestHash(req)
	if actualHash != recordedRequest.Hash {
		return replayErrorStream(fmt.Errorf(
			"replay prefix diverged at turn %d: request hash expected %s, got %s",
			turn,
			recordedRequest.Hash,
			actualHash,
		)), nil
	}
	recordedResponse, ok := c.Plan.Responses[turn]
	if !ok {
		return replayErrorStream(fmt.Errorf("recorded response for turn %d is missing", turn)), nil
	}
	stream := make(chan llm.Event, 2)
	stream <- llm.Event{Type: llm.EventText, Text: recordedResponse.Content}
	stream <- llm.Event{Type: llm.EventDone, Response: &llm.Response{
		Content:   recordedResponse.Content,
		ToolCalls: append([]llm.ToolCall(nil), recordedResponse.ToolCalls...),
		Usage:     recordedResponse.Usage,
		Raw:       recordedResponse.Raw,
	}}
	close(stream)
	return stream, nil
}

func requestHash(req llm.ChatRequest) string {
	return StableHash(struct {
		System   string
		Messages []llm.Message
		Tools    []llm.ToolSchema
	}{req.System, req.Messages, req.Tools})
}

func replayErrorStream(err error) <-chan llm.Event {
	stream := make(chan llm.Event, 1)
	stream <- llm.Event{Type: llm.EventError, Err: err}
	close(stream)
	return stream
}
