package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cohort/internal/replay"
	"cohort/internal/session"
)

type replayExperimentSummary struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	ForkTurn    int     `json:"fork_turn"`
	Trials      int     `json:"trials"`
	SuccessRate float64 `json:"success_rate"`
	ProofHash   string  `json:"proof_hash"`
	ReportPath  string  `json:"report_path"`
}

// replayTurnDetail 是单个 turn 的原文明细，供 Time Machine 展开排查“这一步到底发生了什么”。
// 原文本就落盘在 frames.jsonl，这里只是把 LoadBundle 已读出的内容按 turn 透传给前端。
type replayTurnDetail struct {
	Turn      int                  `json:"turn"`
	Requests  []replayRequestView  `json:"requests,omitempty"`
	Responses []replayResponseView `json:"responses,omitempty"`
	Tools     []replayToolView     `json:"tools,omitempty"`
}

type replayRequestView struct {
	Sequence     int                 `json:"sequence"`
	System       string              `json:"system,omitempty"`
	MessageCount int                 `json:"message_count"`
	Messages     []replayMessageView `json:"messages,omitempty"`
	ToolCount    int                 `json:"tool_count"`
}

type replayMessageView struct {
	Role    string `json:"role"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type replayResponseView struct {
	Sequence      int    `json:"sequence"`
	Content       string `json:"content,omitempty"`
	ToolCallCount int    `json:"tool_call_count"`
	Raw           string `json:"raw,omitempty"`
}

type replayToolView struct {
	Sequence   int    `json:"sequence"`
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// buildTurnDetail 从 frames 中筛出指定 turn 的原文。includeRaw 为假时剥离最臃肿的
// 原始流式载荷（Response.Raw），其内容已由 Content 覆盖，默认不返回以控体积并减少敏感暴露。
func buildTurnDetail(frames []replay.Frame, turn int, includeRaw bool) replayTurnDetail {
	detail := replayTurnDetail{Turn: turn}
	for _, frame := range frames {
		if frame.Turn != turn {
			continue
		}
		switch frame.Kind {
		case replay.FrameRequest:
			if frame.Request == nil {
				continue
			}
			view := replayRequestView{
				Sequence:     frame.Sequence,
				System:       frame.Request.System,
				MessageCount: len(frame.Request.Messages),
				ToolCount:    len(frame.Request.Tools),
			}
			for _, message := range frame.Request.Messages {
				view.Messages = append(view.Messages, replayMessageView{
					Role:    string(message.Role),
					Name:    message.Name,
					Content: message.Content,
				})
			}
			detail.Requests = append(detail.Requests, view)
		case replay.FrameResponse:
			if frame.Response == nil {
				continue
			}
			view := replayResponseView{
				Sequence:      frame.Sequence,
				Content:       frame.Response.Content,
				ToolCallCount: len(frame.Response.ToolCalls),
			}
			if includeRaw {
				view.Raw = frame.Response.Raw
			}
			detail.Responses = append(detail.Responses, view)
		case replay.FrameTool:
			if frame.Tool == nil {
				continue
			}
			arguments := ""
			if len(frame.Tool.Arguments) > 0 {
				if encoded, err := json.Marshal(frame.Tool.Arguments); err == nil {
					arguments = string(encoded)
				}
			}
			detail.Tools = append(detail.Tools, replayToolView{
				Sequence:   frame.Sequence,
				Index:      frame.Tool.Index,
				Name:       frame.Tool.Call.Function.Name,
				Arguments:  arguments,
				Result:     frame.Tool.Result,
				DurationMS: frame.Tool.DurationMS,
			})
		}
	}
	return detail
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/replays/"), "/"), "/")
	if len(parts) != 2 || !safeReplayID(parts[0]) || !safeReplayID(parts[1]) {
		writeControlError(w, http.StatusBadRequest, "expected /api/v1/replays/{session_id}/{run_id}")
		return
	}
	sessionRoot := filepath.Join(s.projectRoot, session.DefaultRootDir)
	manifest, frames, err := replay.LoadBundle(sessionRoot, parts[0], parts[1])
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeControlError(w, http.StatusNotFound, "replay bundle not found")
			return
		}
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exact, err := replay.ExactReplay(sessionRoot, parts[0], parts[1])
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	experiments, err := loadReplayExperimentSummaries(sessionRoot, parts[0], parts[1])
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := map[string]any{
		"manifest":    manifest,
		"exact_proof": exact,
		"experiments": experiments,
	}
	// 原文明细体积大，默认不返回；只有显式指定 ?turn=N 时才透传该 turn 的原文，
	// 天然把响应限制在单个 turn。?include_raw=true 才额外附带原始流式载荷。
	if raw := strings.TrimSpace(r.URL.Query().Get("turn")); raw != "" {
		turn, convErr := strconv.Atoi(raw)
		if convErr != nil || turn <= 0 {
			writeControlError(w, http.StatusBadRequest, "turn must be a positive integer")
			return
		}
		includeRaw := r.URL.Query().Get("include_raw") == "true"
		payload["turn_detail"] = buildTurnDetail(frames, turn, includeRaw)
	}
	writeControlJSON(w, http.StatusOK, payload)
}

func loadReplayExperimentSummaries(sessionRoot, sessionID, runID string) ([]replayExperimentSummary, error) {
	root := filepath.Join(sessionRoot, sessionID, replay.ReplayDirName, runID, "experiments")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []replayExperimentSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	summaries := make([]replayExperimentSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !safeReplayID(entry.Name()) {
			continue
		}
		reportPath := filepath.Join(root, entry.Name(), "report.json")
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		var report replay.ExperimentReport
		if err := json.Unmarshal(data, &report); err != nil {
			continue
		}
		summaries = append(summaries, replayExperimentSummary{
			ID:          report.ID,
			CreatedAt:   report.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			ForkTurn:    report.ForkTurn,
			Trials:      len(report.Trials),
			SuccessRate: report.SuccessRate,
			ProofHash:   report.ProofHash,
			ReportPath:  filepath.ToSlash(filepath.Join("experiments", entry.Name(), "report.json")),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt > summaries[j].CreatedAt
	})
	return summaries, nil
}

func safeReplayID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
