package replay

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cohort/internal/llm"
)

const (
	SchemaVersion    = 1
	ReplayDirName    = "replay"
	ManifestFileName = "manifest.json"
	FramesFileName   = "frames.jsonl"
)

type Mode string

const (
	ModeExact Mode = "exact"
	ModeFork  Mode = "fork"
)

type Replayability string

const (
	ReplayabilityExactOnly Replayability = "exact_only"
	ReplayabilityForkable  Replayability = "forkable"
)

type Manifest struct {
	SchemaVersion     int           `json:"schema_version"`
	SessionID         string        `json:"session_id"`
	RunID             string        `json:"run_id"`
	CreatedAt         time.Time     `json:"created_at"`
	CompletedAt       time.Time     `json:"completed_at,omitempty"`
	Status            string        `json:"status"`
	Replayability     Replayability `json:"replayability"`
	ReplayBlockReason string        `json:"replay_block_reason,omitempty"`
	Provider          string        `json:"provider,omitempty"`
	Model             string        `json:"model,omitempty"`
	WorkingDirectory  string        `json:"working_directory"`
	SystemPromptHash  string        `json:"system_prompt_hash"`
	ToolSchemaHash    string        `json:"tool_schema_hash"`
	InputHash         string        `json:"input_hash"`
	PrefixHash        string        `json:"prefix_hash"`
	Git               GitBaseline   `json:"git"`
	FrameCount        int           `json:"frame_count"`
	FramesHash        string        `json:"frames_hash,omitempty"`
	FinalStatus       string        `json:"final_status,omitempty"`
	Error             string        `json:"error,omitempty"`
}

type GitBaseline struct {
	Available  bool   `json:"available"`
	Root       string `json:"root,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
	TreeHash   string `json:"tree_hash,omitempty"`
	StatusHash string `json:"status_hash,omitempty"`
	Dirty      bool   `json:"dirty,omitempty"`
}

type FrameKind string

const (
	FrameRequest  FrameKind = "llm_request"
	FrameResponse FrameKind = "llm_response"
	FrameTool     FrameKind = "tool_result"
)

type Frame struct {
	Sequence int         `json:"sequence"`
	Kind     FrameKind   `json:"kind"`
	Turn     int         `json:"turn"`
	Time     time.Time   `json:"time"`
	Request  *Request    `json:"request,omitempty"`
	Response *Response   `json:"response,omitempty"`
	Tool     *ToolResult `json:"tool,omitempty"`
	Hash     string      `json:"hash"`
}

type Request struct {
	System   string           `json:"system"`
	Messages []llm.Message    `json:"messages"`
	Tools    []llm.ToolSchema `json:"tools"`
	Hash     string           `json:"hash"`
}

type Response struct {
	Content   string         `json:"content,omitempty"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	Usage     llm.Usage      `json:"usage,omitempty"`
	Raw       string         `json:"raw,omitempty"`
	Hash      string         `json:"hash"`
}

type ToolResult struct {
	Index      int            `json:"index"`
	Call       llm.ToolCall   `json:"call"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     string         `json:"result"`
	NextPrompt string         `json:"next_prompt,omitempty"`
	ShouldExit bool           `json:"should_exit,omitempty"`
	Audit      map[string]any `json:"audit,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Hash       string         `json:"hash"`
}

type RecorderConfig struct {
	SessionDir       string
	SessionID        string
	RunID            string
	Provider         string
	Model            string
	WorkingDirectory string
	SystemPrompt     string
	Tools            []llm.ToolSchema
	Input            string
	PrefixMessages   []llm.Message
}

type Recorder struct {
	mu           sync.Mutex
	dir          string
	manifestPath string
	framesPath   string
	manifest     Manifest
	frameHashes  []string
	failed       error
}

func NewRecorder(cfg RecorderConfig) (*Recorder, error) {
	if strings.TrimSpace(cfg.SessionDir) == "" || strings.TrimSpace(cfg.RunID) == "" {
		return nil, errors.New("replay recorder requires session directory and run id")
	}
	dir := filepath.Join(cfg.SessionDir, ReplayDirName, cfg.RunID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	git := inspectGitBaseline(cfg.WorkingDirectory)
	replayability := ReplayabilityForkable
	blockReason := ""
	if !git.Available {
		replayability = ReplayabilityExactOnly
		blockReason = "working directory is not a git repository"
	} else if git.Dirty {
		replayability = ReplayabilityExactOnly
		blockReason = "working tree was dirty when the run started"
	}
	r := &Recorder{
		dir:          dir,
		manifestPath: filepath.Join(dir, ManifestFileName),
		framesPath:   filepath.Join(dir, FramesFileName),
		manifest: Manifest{
			SchemaVersion:     SchemaVersion,
			SessionID:         cfg.SessionID,
			RunID:             cfg.RunID,
			CreatedAt:         time.Now().UTC(),
			Status:            "recording",
			Replayability:     replayability,
			ReplayBlockReason: blockReason,
			Provider:          strings.TrimSpace(cfg.Provider),
			Model:             strings.TrimSpace(cfg.Model),
			WorkingDirectory:  filepath.Clean(cfg.WorkingDirectory),
			SystemPromptHash:  StableHash(cfg.SystemPrompt),
			ToolSchemaHash:    StableHash(cfg.Tools),
			InputHash:         StableHash(cfg.Input),
			PrefixHash:        StableHash(cfg.PrefixMessages),
			Git:               git,
		},
	}
	if err := r.writeManifest(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

func (r *Recorder) RecordRequest(turn int, system string, messages []llm.Message, tools []llm.ToolSchema) {
	req := Request{
		System:   system,
		Messages: append([]llm.Message(nil), messages...),
		Tools:    append([]llm.ToolSchema(nil), tools...),
	}
	req.Hash = StableHash(struct {
		System   string
		Messages []llm.Message
		Tools    []llm.ToolSchema
	}{req.System, req.Messages, req.Tools})
	r.appendFrame(Frame{Kind: FrameRequest, Turn: turn, Request: &req})
}

func (r *Recorder) RecordResponse(turn int, response *llm.Response) {
	if response == nil {
		r.markFailed(errors.New("cannot record nil llm response"))
		return
	}
	recorded := Response{
		Content:   response.Content,
		ToolCalls: append([]llm.ToolCall(nil), response.ToolCalls...),
		Usage:     response.Usage,
		Raw:       response.Raw,
	}
	recorded.Hash = StableHash(struct {
		Content   string
		ToolCalls []llm.ToolCall
		Usage     llm.Usage
		Raw       string
	}{recorded.Content, recorded.ToolCalls, recorded.Usage, recorded.Raw})
	r.appendFrame(Frame{Kind: FrameResponse, Turn: turn, Response: &recorded})
}

func (r *Recorder) RecordTool(turn, index int, call llm.ToolCall, args map[string]any, result, nextPrompt string, shouldExit bool, audit map[string]any, duration time.Duration) {
	tool := ToolResult{
		Index:      index,
		Call:       call,
		Arguments:  cloneMap(args),
		Result:     result,
		NextPrompt: nextPrompt,
		ShouldExit: shouldExit,
		Audit:      cloneMap(audit),
		DurationMS: duration.Milliseconds(),
	}
	tool.Hash = StableHash(struct {
		Index      int
		Call       llm.ToolCall
		Arguments  map[string]any
		Result     string
		NextPrompt string
		ShouldExit bool
		Audit      map[string]any
	}{tool.Index, tool.Call, tool.Arguments, tool.Result, tool.NextPrompt, tool.ShouldExit, tool.Audit})
	r.appendFrame(Frame{Kind: FrameTool, Turn: turn, Tool: &tool})
}

func (r *Recorder) Complete(finalStatus string, runErr error) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifest.CompletedAt = time.Now().UTC()
	r.manifest.FinalStatus = finalStatus
	if runErr != nil {
		r.manifest.Error = runErr.Error()
	}
	if r.failed != nil {
		r.manifest.Status = "incomplete"
		r.manifest.Error = r.failed.Error()
	} else {
		r.manifest.Status = "complete"
	}
	r.manifest.FrameCount = len(r.frameHashes)
	r.manifest.FramesHash = StableHash(r.frameHashes)
	return r.writeManifest()
}

func (r *Recorder) appendFrame(frame Frame) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return
	}
	frame.Sequence = len(r.frameHashes) + 1
	frame.Time = time.Now().UTC()
	frame.Hash = StableHash(struct {
		Sequence int
		Kind     FrameKind
		Turn     int
		Request  *Request
		Response *Response
		Tool     *ToolResult
	}{frame.Sequence, frame.Kind, frame.Turn, frame.Request, frame.Response, frame.Tool})
	data, err := json.Marshal(frame)
	if err != nil {
		r.failed = err
		return
	}
	file, err := os.OpenFile(r.framesPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		r.failed = err
		return
	}
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		r.failed = err
		return
	}
	r.frameHashes = append(r.frameHashes, frame.Hash)
	r.manifest.FrameCount = len(r.frameHashes)
	if err := r.writeManifest(); err != nil {
		r.failed = err
	}
}

func (r *Recorder) markFailed(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed == nil {
		r.failed = err
	}
}

func (r *Recorder) writeManifest() error {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := r.manifestPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.manifestPath)
}

func LoadBundle(sessionRoot, sessionID, runID string) (Manifest, []Frame, error) {
	dir := filepath.Join(sessionRoot, sessionID, ReplayDirName, runID)
	manifestData, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return Manifest{}, nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return Manifest{}, nil, err
	}
	file, err := os.Open(filepath.Join(dir, FramesFileName))
	if err != nil {
		return Manifest{}, nil, err
	}
	defer file.Close()
	var frames []Frame
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64<<20)
	for scanner.Scan() {
		var frame Frame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return Manifest{}, nil, fmt.Errorf("decode replay frame %d: %w", len(frames)+1, err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, frames, nil
}

func StableHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", value))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func inspectGitBaseline(cwd string) GitBaseline {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return GitBaseline{}
	}
	run := func(args ...string) (string, error) {
		command := exec.Command("git", append([]string{"-C", cwd}, args...)...)
		output, err := command.Output()
		return strings.TrimSpace(string(output)), err
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return GitBaseline{}
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return GitBaseline{}
	}
	tree, err := run("rev-parse", "HEAD^{tree}")
	if err != nil {
		return GitBaseline{}
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return GitBaseline{}
	}
	lines := strings.Split(status, "\n")
	sort.Strings(lines)
	return GitBaseline{
		Available:  true,
		Root:       filepath.Clean(root),
		HeadCommit: head,
		TreeHash:   tree,
		StatusHash: StableHash(lines),
		Dirty:      strings.TrimSpace(status) != "",
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		return input
	}
	return output
}
