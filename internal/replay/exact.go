package replay

import (
	"errors"
	"fmt"
	"strings"

	"cohort/internal/llm"
)

type ExactResult struct {
	SessionID       string        `json:"session_id"`
	RunID           string        `json:"run_id"`
	Verified        bool          `json:"verified"`
	FrameCount      int           `json:"frame_count"`
	TurnCount       int           `json:"turn_count"`
	LLMCalls        int           `json:"llm_calls"`
	ToolCalls       int           `json:"tool_calls"`
	FinalStatus     string        `json:"final_status"`
	FinalResponse   string        `json:"final_response,omitempty"`
	ProofHash       string        `json:"proof_hash,omitempty"`
	Replayability   Replayability `json:"replayability"`
	FirstDivergence *Divergence   `json:"first_divergence,omitempty"`
}

type Divergence struct {
	Sequence int    `json:"sequence"`
	Turn     int    `json:"turn,omitempty"`
	Kind     string `json:"kind"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Reason   string `json:"reason"`
}

func ExactReplay(sessionRoot, sessionID, runID string) (ExactResult, error) {
	manifest, frames, err := LoadBundle(sessionRoot, sessionID, runID)
	if err != nil {
		return ExactResult{}, err
	}
	if _, err := LoadRuntime(sessionRoot, sessionID, runID, manifest); err != nil {
		return ExactResult{}, err
	}
	result := ExactResult{
		SessionID:     manifest.SessionID,
		RunID:         manifest.RunID,
		FrameCount:    len(frames),
		FinalStatus:   manifest.FinalStatus,
		Replayability: manifest.Replayability,
	}
	if divergence := verifyManifest(manifest, frames); divergence != nil {
		result.FirstDivergence = divergence
		return result, nil
	}
	state := exactState{}
	for index := range frames {
		if divergence := state.consume(frames[index]); divergence != nil {
			result.FirstDivergence = divergence
			result.TurnCount = state.maxTurn
			result.LLMCalls = state.llmCalls
			result.ToolCalls = state.toolCalls
			return result, nil
		}
	}
	if divergence := state.finish(len(frames) + 1); divergence != nil {
		result.FirstDivergence = divergence
		return result, nil
	}
	result.Verified = true
	result.TurnCount = state.maxTurn
	result.LLMCalls = state.llmCalls
	result.ToolCalls = state.toolCalls
	result.FinalResponse = state.finalResponse
	result.ProofHash = StableHash(struct {
		ManifestHash string
		FramesHash   string
		FinalStatus  string
	}{
		ManifestHash: StableHash(manifest),
		FramesHash:   manifest.FramesHash,
		FinalStatus:  manifest.FinalStatus,
	})
	return result, nil
}

func verifyManifest(manifest Manifest, frames []Frame) *Divergence {
	if manifest.SchemaVersion != SchemaVersion {
		return &Divergence{
			Kind:     "manifest",
			Expected: fmt.Sprint(SchemaVersion),
			Actual:   fmt.Sprint(manifest.SchemaVersion),
			Reason:   "unsupported replay schema version",
		}
	}
	if manifest.Status != "complete" {
		return &Divergence{
			Kind:     "manifest",
			Expected: "complete",
			Actual:   manifest.Status,
			Reason:   "replay bundle was not completed",
		}
	}
	if manifest.FrameCount != len(frames) {
		return &Divergence{
			Kind:     "manifest",
			Expected: fmt.Sprint(manifest.FrameCount),
			Actual:   fmt.Sprint(len(frames)),
			Reason:   "frame count does not match manifest",
		}
	}
	hashes := make([]string, 0, len(frames))
	for index := range frames {
		frame := frames[index]
		expectedSequence := index + 1
		if frame.Sequence != expectedSequence {
			return &Divergence{
				Sequence: expectedSequence,
				Turn:     frame.Turn,
				Kind:     string(frame.Kind),
				Expected: fmt.Sprint(expectedSequence),
				Actual:   fmt.Sprint(frame.Sequence),
				Reason:   "non-contiguous replay sequence",
			}
		}
		expectedHash := frameContentHash(frame)
		if frame.Hash != expectedHash {
			return &Divergence{
				Sequence: frame.Sequence,
				Turn:     frame.Turn,
				Kind:     string(frame.Kind),
				Expected: expectedHash,
				Actual:   frame.Hash,
				Reason:   "frame content hash mismatch",
			}
		}
		if divergence := verifyPayloadHash(frame); divergence != nil {
			return divergence
		}
		hashes = append(hashes, frame.Hash)
	}
	framesHash := StableHash(hashes)
	if manifest.FramesHash != framesHash {
		return &Divergence{
			Kind:     "manifest",
			Expected: manifest.FramesHash,
			Actual:   framesHash,
			Reason:   "aggregate frame hash mismatch",
		}
	}
	if len(frames) > 0 && frames[0].Request != nil {
		request := frames[0].Request
		if actual := StableHash(request.System); actual != manifest.SystemPromptHash {
			return &Divergence{
				Sequence: frames[0].Sequence,
				Turn:     frames[0].Turn,
				Kind:     string(FrameRequest),
				Expected: manifest.SystemPromptHash,
				Actual:   actual,
				Reason:   "system prompt differs from manifest",
			}
		}
		if actual := StableHash(request.Tools); actual != manifest.ToolSchemaHash {
			return &Divergence{
				Sequence: frames[0].Sequence,
				Turn:     frames[0].Turn,
				Kind:     string(FrameRequest),
				Expected: manifest.ToolSchemaHash,
				Actual:   actual,
				Reason:   "tool schema differs from manifest",
			}
		}
	}
	return nil
}

func verifyPayloadHash(frame Frame) *Divergence {
	switch frame.Kind {
	case FrameRequest:
		if frame.Request == nil || frame.Response != nil || frame.Tool != nil {
			return malformedFrame(frame, "request frame has invalid payload")
		}
		expected := StableHash(struct {
			System   string
			Messages []llm.Message
			Tools    []llm.ToolSchema
		}{frame.Request.System, frame.Request.Messages, frame.Request.Tools})
		if frame.Request.Hash != expected {
			return payloadMismatch(frame, expected, frame.Request.Hash)
		}
	case FrameResponse:
		if frame.Response == nil || frame.Request != nil || frame.Tool != nil {
			return malformedFrame(frame, "response frame has invalid payload")
		}
		expected := StableHash(struct {
			Content   string
			ToolCalls []llm.ToolCall
			Usage     llm.Usage
			Raw       string
		}{frame.Response.Content, frame.Response.ToolCalls, frame.Response.Usage, frame.Response.Raw})
		if frame.Response.Hash != expected {
			return payloadMismatch(frame, expected, frame.Response.Hash)
		}
	case FrameTool:
		if frame.Tool == nil || frame.Request != nil || frame.Response != nil {
			return malformedFrame(frame, "tool frame has invalid payload")
		}
		expected := StableHash(struct {
			Index      int
			Call       llm.ToolCall
			Arguments  map[string]any
			Result     string
			NextPrompt string
			ShouldExit bool
			Audit      map[string]any
		}{
			frame.Tool.Index,
			frame.Tool.Call,
			frame.Tool.Arguments,
			frame.Tool.Result,
			frame.Tool.NextPrompt,
			frame.Tool.ShouldExit,
			frame.Tool.Audit,
		})
		if frame.Tool.Hash != expected {
			return payloadMismatch(frame, expected, frame.Tool.Hash)
		}
	default:
		return malformedFrame(frame, "unknown frame kind")
	}
	return nil
}

func frameContentHash(frame Frame) string {
	return StableHash(struct {
		Sequence int
		Kind     FrameKind
		Turn     int
		Request  *Request
		Response *Response
		Tool     *ToolResult
	}{frame.Sequence, frame.Kind, frame.Turn, frame.Request, frame.Response, frame.Tool})
}

func malformedFrame(frame Frame, reason string) *Divergence {
	return &Divergence{Sequence: frame.Sequence, Turn: frame.Turn, Kind: string(frame.Kind), Reason: reason}
}

func payloadMismatch(frame Frame, expected, actual string) *Divergence {
	return &Divergence{
		Sequence: frame.Sequence,
		Turn:     frame.Turn,
		Kind:     string(frame.Kind),
		Expected: expected,
		Actual:   actual,
		Reason:   "payload hash mismatch",
	}
}

type exactState struct {
	awaitingResponse bool
	pendingTools     []llm.ToolCall
	nextTool         int
	currentTurn      int
	maxTurn          int
	llmCalls         int
	toolCalls        int
	finalResponse    string
}

func (s *exactState) consume(frame Frame) *Divergence {
	if frame.Turn <= 0 {
		return malformedFrame(frame, "turn must be positive")
	}
	if frame.Turn > s.maxTurn {
		s.maxTurn = frame.Turn
	}
	switch frame.Kind {
	case FrameRequest:
		if s.awaitingResponse {
			return malformedFrame(frame, "request arrived before previous response")
		}
		if s.nextTool < len(s.pendingTools) {
			return malformedFrame(frame, "request arrived before all recorded tool calls completed")
		}
		if s.currentTurn > 0 && frame.Turn <= s.currentTurn {
			return malformedFrame(frame, "request turn is not increasing")
		}
		s.currentTurn = frame.Turn
		s.awaitingResponse = true
		s.pendingTools = nil
		s.nextTool = 0
		s.llmCalls++
	case FrameResponse:
		if !s.awaitingResponse || frame.Turn != s.currentTurn {
			return malformedFrame(frame, "response has no matching request")
		}
		s.awaitingResponse = false
		s.pendingTools = append([]llm.ToolCall(nil), frame.Response.ToolCalls...)
		s.nextTool = 0
		if len(s.pendingTools) == 0 {
			s.finalResponse = frame.Response.Content
		}
	case FrameTool:
		if s.awaitingResponse || frame.Turn != s.currentTurn {
			return malformedFrame(frame, "tool result has no matching response")
		}
		if s.nextTool >= len(s.pendingTools) {
			return malformedFrame(frame, "unexpected extra tool result")
		}
		expected := s.pendingTools[s.nextTool]
		actual := frame.Tool.Call
		if frame.Tool.Index != s.nextTool || expected.ID != actual.ID ||
			expected.Function.Name != actual.Function.Name ||
			expected.Function.Arguments != actual.Function.Arguments {
			return &Divergence{
				Sequence: frame.Sequence,
				Turn:     frame.Turn,
				Kind:     string(FrameTool),
				Expected: toolIdentity(expected, s.nextTool),
				Actual:   toolIdentity(actual, frame.Tool.Index),
				Reason:   "tool result does not match the response tool call",
			}
		}
		s.nextTool++
		s.toolCalls++
	}
	return nil
}

func (s *exactState) finish(sequence int) *Divergence {
	if s.awaitingResponse {
		return &Divergence{Sequence: sequence, Turn: s.currentTurn, Kind: "end", Reason: "bundle ended before llm response"}
	}
	if s.nextTool < len(s.pendingTools) {
		return &Divergence{Sequence: sequence, Turn: s.currentTurn, Kind: "end", Reason: "bundle ended before all tool calls completed"}
	}
	if s.llmCalls == 0 {
		return &Divergence{Sequence: sequence, Kind: "end", Reason: "bundle contains no llm calls"}
	}
	return nil
}

func toolIdentity(call llm.ToolCall, index int) string {
	return fmt.Sprintf("%d:%s:%s:%s", index, call.ID, call.Function.Name, StableHash(call.Function.Arguments))
}

func ValidateExact(result ExactResult) error {
	if result.Verified {
		return nil
	}
	if result.FirstDivergence == nil {
		return errors.New("exact replay was not verified")
	}
	var details []string
	if result.FirstDivergence.Sequence > 0 {
		details = append(details, fmt.Sprintf("sequence=%d", result.FirstDivergence.Sequence))
	}
	if result.FirstDivergence.Turn > 0 {
		details = append(details, fmt.Sprintf("turn=%d", result.FirstDivergence.Turn))
	}
	details = append(details, result.FirstDivergence.Reason)
	return errors.New(strings.Join(details, ": "))
}
