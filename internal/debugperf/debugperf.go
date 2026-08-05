package debugperf

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionID = "llm-stream-slow"

func Enabled() bool {
	return os.Getenv("COHORT_DEBUG_PERF") == "1"
}

func Event(runID string, hypothesisID string, location string, msg string, data map[string]any) {
	if !Enabled() {
		return
	}
	url, sid := debugEnv()
	if url == "" {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"sessionId":    sid,
		"runId":        firstNonEmpty(runID, "pre-fix"),
		"hypothesisId": hypothesisID,
		"location":     location,
		"msg":          "[DEBUG] " + msg,
		"data":         data,
		"ts":           time.Now().UnixMilli(),
	})
	if err != nil {
		return
	}
	go func() {
		client := http.Client{Timeout: 120 * time.Millisecond}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	}()
}

func Since(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func debugEnv() (string, string) {
	url := strings.TrimSpace(os.Getenv("DEBUG_SERVER_URL"))
	sid := strings.TrimSpace(os.Getenv("DEBUG_SESSION_ID"))
	if url != "" {
		return url, firstNonEmpty(sid, sessionID)
	}
	data, err := os.ReadFile(filepath.Join(".dbg", sessionID+".env"))
	if err != nil {
		return "", sessionID
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "DEBUG_SERVER_URL":
			url = strings.TrimSpace(value)
		case "DEBUG_SESSION_ID":
			sid = strings.TrimSpace(value)
		}
	}
	return url, firstNonEmpty(sid, sessionID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
