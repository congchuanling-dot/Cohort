package observability

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// JSONLSink 将观测事件以一行一个 JSON 的形式追加到本地文件。
type JSONLSink struct {
	path string
	mu   sync.Mutex
}

func NewJSONLSink(path string) *JSONLSink {
	return &JSONLSink{path: filepath.Clean(path)}
}

func (s *JSONLSink) Emit(ctx context.Context, event Event) error {
	if s == nil || s.path == "" {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	file, openErr := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	_, writeErr := file.Write(data)
	return writeErr
}

func (s *JSONLSink) Close(ctx context.Context) error {
	return nil
}
