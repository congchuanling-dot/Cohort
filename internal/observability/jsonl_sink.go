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
	path    string
	events  chan []byte
	done    chan struct{}
	closeMu sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	file    *os.File
	errMu   sync.Mutex
	lastErr error
}

func NewJSONLSink(path string) *JSONLSink {
	sink := &JSONLSink{
		path:   filepath.Clean(path),
		events: make(chan []byte, 1024),
		done:   make(chan struct{}),
	}
	sink.wg.Add(1)
	go sink.writeLoop()
	return sink
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

	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.events <- data:
	default:
		// 观测日志是旁路能力。队列满时丢弃本条事件，避免阻塞 Agent 主流程。
	}
	return nil
}

func (s *JSONLSink) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	if !s.closed {
		s.closed = true
		close(s.events)
		close(s.done)
	}
	s.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return s.lastError()
	}
}

func (s *JSONLSink) writeLoop() {
	defer s.wg.Done()
	for data := range s.events {
		if err := s.write(data); err != nil {
			s.recordError(err)
		}
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			s.recordError(err)
		}
		s.file = nil
	}
}

func (s *JSONLSink) write(data []byte) error {
	if err := s.ensureFile(); err != nil {
		return err
	}
	_, err := s.file.Write(data)
	return err
}

func (s *JSONLSink) ensureFile() error {
	if s.file != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	s.file = file
	return nil
}

func (s *JSONLSink) recordError(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.lastErr == nil {
		s.lastErr = err
	}
}

func (s *JSONLSink) lastError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.lastErr
}
