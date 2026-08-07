package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

type ServerStatus struct {
	Language  string `json:"language"`
	Command   string `json:"command"`
	Root      string `json:"root"`
	PID       int    `json:"pid,omitempty"`
	Running   bool   `json:"running"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type languageServer struct {
	language string
	command  string
	root     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser

	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[int64]chan rpcResponse
	opened   map[string]fileState
	nextID   atomic.Int64
	alive    bool
	restarts int
	lastErr  error
}

type fileState struct {
	Version int
	ModTime time.Time
}

type serverRegistry struct {
	mu      sync.Mutex
	servers map[string]*languageServer
	cache   map[string]queryCacheEntry
}

type queryCacheEntry struct {
	Result    QueryResult
	ExpiresAt time.Time
	ModTime   time.Time
}

var persistentServers = &serverRegistry{
	servers: map[string]*languageServer{},
	cache:   map[string]queryCacheEntry{},
}

func (d Diagnostics) languageServerQuery(ctx context.Context, language string, kind string, opts QueryOptions) (QueryResult, error) {
	command := d.languageServerCommand(language)
	if _, err := exec.LookPath(command); err != nil {
		return QueryResult{}, err
	}
	root, err := filepath.Abs(firstNonEmpty(d.Root, "."))
	if err != nil {
		return QueryResult{}, err
	}
	cacheKey, modTime := queryCacheKey(root, language, kind, opts)
	if cached, ok := persistentServers.cached(cacheKey, modTime); ok {
		return cached, nil
	}
	server, err := persistentServers.get(ctx, root, language, command)
	if err != nil {
		return QueryResult{}, err
	}
	result, err := server.query(ctx, kind, opts)
	if err != nil {
		if restartErr := persistentServers.restart(ctx, root, language, command); restartErr != nil {
			return result, fmt.Errorf("%v; restart failed: %w", err, restartErr)
		}
		server, _ = persistentServers.get(ctx, root, language, command)
		result, err = server.query(ctx, kind, opts)
	}
	if err == nil {
		persistentServers.storeCache(cacheKey, modTime, result)
	}
	return result, err
}

func (d Diagnostics) languageServerCommand(language string) string {
	if language == LanguagePython {
		return firstNonEmpty(d.PythonServerCommand, "pyright-langserver")
	}
	return firstNonEmpty(d.TypeScriptServerCommand, "typescript-language-server")
}

func (r *serverRegistry) get(ctx context.Context, root string, language string, command string) (*languageServer, error) {
	key := root + "\x00" + language
	r.mu.Lock()
	server := r.servers[key]
	if server != nil && server.isAlive() {
		r.mu.Unlock()
		return server, nil
	}
	if server != nil {
		server.stop()
		delete(r.servers, key)
	}
	server = &languageServer{
		language: language,
		command:  command,
		root:     root,
		pending:  map[int64]chan rpcResponse{},
		opened:   map[string]fileState{},
	}
	r.servers[key] = server
	r.mu.Unlock()
	if err := server.start(ctx); err != nil {
		r.mu.Lock()
		delete(r.servers, key)
		r.mu.Unlock()
		return nil, err
	}
	return server, nil
}

func (r *serverRegistry) restart(ctx context.Context, root string, language string, command string) error {
	key := root + "\x00" + language
	r.mu.Lock()
	old := r.servers[key]
	restarts := 0
	if old != nil {
		restarts = old.restarts + 1
		old.stop()
		delete(r.servers, key)
	}
	for key := range r.cache {
		if strings.HasPrefix(key, root+"\x00"+language+"\x00") {
			delete(r.cache, key)
		}
	}
	r.mu.Unlock()
	server, err := r.get(ctx, root, language, command)
	if err == nil {
		server.restarts = restarts
	}
	return err
}

func (r *serverRegistry) cached(key string, modTime time.Time) (QueryResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || time.Now().After(entry.ExpiresAt) || !entry.ModTime.Equal(modTime) {
		delete(r.cache, key)
		return QueryResult{}, false
	}
	return entry.Result, true
}

func (r *serverRegistry) storeCache(key string, modTime time.Time, result QueryResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = queryCacheEntry{Result: result, ModTime: modTime, ExpiresAt: time.Now().Add(3 * time.Second)}
}

func (s *languageServer) start(ctx context.Context) error {
	cmd := exec.Command(s.command, "--stdio")
	cmd.Dir = s.root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &boundedWriter{target: &stderr, limit: 16 * 1024}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.stdout = stdout
	s.alive = true
	s.lastErr = nil
	s.mu.Unlock()
	go s.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		if err != nil && stderr.Len() > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		s.markDead(err)
	}()
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rootURI := fileURI(s.root)
	_, err = s.request(initCtx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"workspaceFolders": []map[string]string{{
			"uri": rootURI, "name": filepath.Base(s.root),
		}},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition":      map[string]any{},
				"references":      map[string]any{},
				"hover":           map[string]any{},
				"documentSymbol":  map[string]any{},
				"synchronization": map[string]any{"didSave": true},
			},
			"workspace": map[string]any{"symbol": map[string]any{}},
		},
	})
	if err != nil {
		s.stop()
		return fmt.Errorf("initialize %s: %w", s.command, err)
	}
	return s.notify("initialized", map[string]any{})
}

func (s *languageServer) query(ctx context.Context, kind string, opts QueryOptions) (QueryResult, error) {
	result := QueryResult{
		Language: s.language,
		Kind:     kind,
		Position: opts.Position,
		Engine:   s.command,
		Command:  []string{s.command, "--stdio"},
		ExitCode: 0,
	}
	var method string
	var params any
	switch kind {
	case QueryDefinition, QueryReferences, QueryHover:
		position, err := parseSourcePosition(opts.Position)
		if err != nil {
			result.ExitCode = -1
			return result, err
		}
		path := position.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		path = filepath.Clean(path)
		if err := s.syncFile(path); err != nil {
			result.ExitCode = -1
			return result, err
		}
		line, character, err := lspPosition(path, position.Line, position.Column)
		if err != nil {
			result.ExitCode = -1
			return result, err
		}
		method = "textDocument/" + kind
		params = map[string]any{
			"textDocument": map[string]string{"uri": fileURI(path)},
			"position":     map[string]int{"line": line, "character": character},
		}
		if kind == QueryReferences {
			params.(map[string]any)["context"] = map[string]bool{"includeDeclaration": opts.IncludeDeclaration}
		}
	case QuerySymbols:
		target := strings.TrimSpace(firstNonEmpty(opts.Target, "."))
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			if err := s.syncFile(path); err != nil {
				return result, err
			}
			method = "textDocument/documentSymbol"
			params = map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}}
		} else {
			method = "workspace/symbol"
			params = map[string]string{"query": ""}
		}
	default:
		return result, fmt.Errorf("unsupported language server query kind %q", kind)
	}
	raw, err := s.request(ctx, method, params)
	if err != nil {
		result.ExitCode = -1
		return result, err
	}
	result.Output = formatLSPPayload(raw, s.root)
	if kind == QuerySymbols && (strings.TrimSpace(result.Output) == "[]" || strings.TrimSpace(result.Output) == "null") {
		result.ExitCode = -1
		return result, errors.New("language server returned no symbols")
	}
	result.OK = true
	return result, nil
}

func (s *languageServer) syncFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	state, opened := s.opened[path]
	s.mu.Unlock()
	languageID := "typescript"
	if s.language == LanguagePython {
		languageID = "python"
	} else if strings.HasSuffix(path, ".tsx") {
		languageID = "typescriptreact"
	} else if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".jsx") {
		languageID = "javascript"
	}
	if !opened {
		state = fileState{Version: 1, ModTime: info.ModTime()}
		err = s.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": fileURI(path), "languageId": languageID, "version": state.Version, "text": string(data),
			},
		})
	} else if !state.ModTime.Equal(info.ModTime()) {
		state.Version++
		state.ModTime = info.ModTime()
		err = s.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": fileURI(path), "version": state.Version},
			"contentChanges": []map[string]string{{"text": string(data)}},
		})
	}
	if err == nil {
		s.mu.Lock()
		s.opened[path] = state
		s.mu.Unlock()
	}
	return err
}

func (s *languageServer) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if !s.isAlive() {
		return nil, errors.New("language server is not running")
	}
	id := s.nextID.Add(1)
	responseCh := make(chan rpcResponse, 1)
	s.mu.Lock()
	s.pending[id] = responseCh
	s.mu.Unlock()
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := s.writeMessage(message); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return nil, fmt.Errorf("lsp error %d: %s", response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *languageServer) notify(method string, params any) error {
	return s.writeMessage(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *languageServer) writeMessage(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	stdin := s.stdin
	alive := s.alive
	s.mu.Unlock()
	if !alive || stdin == nil {
		return errors.New("language server is not running")
	}
	_, err = fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func (s *languageServer) readLoop(stdout io.Reader) {
	reader := bufio.NewReader(stdout)
	for {
		length := -1
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if key, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(value))
			}
		}
		if length < 0 || length > 32*1024*1024 {
			s.markDead(fmt.Errorf("invalid LSP content length %d", length))
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) == 0 {
			continue
		}
		var id int64
		if json.Unmarshal(envelope.ID, &id) != nil {
			continue
		}
		s.mu.Lock()
		pending := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if pending != nil {
			pending <- rpcResponse{Result: envelope.Result, Error: envelope.Error}
		}
	}
}

func (s *languageServer) isAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive && s.cmd != nil && s.cmd.Process != nil
}

func (s *languageServer) markDead(err error) {
	s.mu.Lock()
	if !s.alive {
		s.mu.Unlock()
		return
	}
	s.alive = false
	s.lastErr = err
	pending := s.pending
	s.pending = map[int64]chan rpcResponse{}
	s.mu.Unlock()
	for _, channel := range pending {
		channel <- rpcResponse{Error: &rpcError{Code: -32000, Message: "language server stopped"}}
	}
}

func (s *languageServer) stop() {
	if s.isAlive() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = s.request(shutdownCtx, "shutdown", nil)
		cancel()
		_ = s.notify("exit", nil)
	}
	s.mu.Lock()
	process := s.cmd
	s.alive = false
	s.mu.Unlock()
	if process != nil && process.Process != nil {
		_ = process.Process.Kill()
	}
}

func ServerStatuses(root string) []ServerStatus {
	root, _ = filepath.Abs(firstNonEmpty(root, "."))
	persistentServers.mu.Lock()
	defer persistentServers.mu.Unlock()
	var statuses []ServerStatus
	for _, language := range []string{LanguageTypeScript, LanguagePython} {
		key := root + "\x00" + language
		server := persistentServers.servers[key]
		status := ServerStatus{Language: language, Root: root}
		if server != nil {
			server.mu.Lock()
			status.Command = server.command
			status.Running = server.alive
			status.Restarts = server.restarts
			if server.cmd != nil && server.cmd.Process != nil {
				status.PID = server.cmd.Process.Pid
			}
			if server.lastErr != nil {
				status.LastError = server.lastErr.Error()
			}
			server.mu.Unlock()
		} else if language == LanguagePython {
			status.Command = "pyright-langserver"
		} else {
			status.Command = "typescript-language-server"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func RestartServer(ctx context.Context, root string, language string) error {
	root, _ = filepath.Abs(firstNonEmpty(root, "."))
	language = NormalizeLanguage(language)
	command := "typescript-language-server"
	if language == LanguagePython {
		command = "pyright-langserver"
	}
	if language != LanguageTypeScript && language != LanguagePython {
		return fmt.Errorf("persistent language server is not supported for %q", language)
	}
	return persistentServers.restart(ctx, root, language, command)
}

func CloseRoot(root string) {
	root, _ = filepath.Abs(firstNonEmpty(root, "."))
	persistentServers.mu.Lock()
	var servers []*languageServer
	for key, server := range persistentServers.servers {
		if strings.HasPrefix(key, root+"\x00") {
			servers = append(servers, server)
			delete(persistentServers.servers, key)
		}
	}
	persistentServers.mu.Unlock()
	for _, server := range servers {
		server.stop()
	}
}

func StopServer(root string, language string) error {
	root, _ = filepath.Abs(firstNonEmpty(root, "."))
	language = NormalizeLanguage(language)
	if language != LanguageTypeScript && language != LanguagePython && language != LanguageAll {
		return fmt.Errorf("persistent language server is not supported for %q", language)
	}
	persistentServers.mu.Lock()
	var servers []*languageServer
	for key, server := range persistentServers.servers {
		if !strings.HasPrefix(key, root+"\x00") {
			continue
		}
		if language != LanguageAll && server.language != language {
			continue
		}
		servers = append(servers, server)
		delete(persistentServers.servers, key)
	}
	persistentServers.mu.Unlock()
	for _, server := range servers {
		server.stop()
	}
	return nil
}

func queryCacheKey(root string, language string, kind string, opts QueryOptions) (string, time.Time) {
	target := firstNonEmpty(opts.Position, opts.Target)
	var modTime time.Time
	if parsed, err := parseSourcePosition(opts.Position); err == nil {
		path := parsed.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
	} else if opts.Target != "" {
		path := opts.Target
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
	}
	return strings.Join([]string{root, language, kind, target, strconv.FormatBool(opts.IncludeDeclaration)}, "\x00"), modTime
}

func lspPosition(path string, line int, column int) (int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(data), "\n")
	if line <= 0 || line > len(lines) {
		return 0, 0, fmt.Errorf("line %d is outside %s", line, path)
	}
	runes := []rune(lines[line-1])
	index := column - 1
	if index < 0 || index > len(runes) {
		return 0, 0, fmt.Errorf("column %d is outside line %d", column, line)
	}
	return line - 1, len(utf16.Encode(runes[:index])), nil
}

func fileURI(path string) string {
	absolute, _ := filepath.Abs(path)
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
}

func formatLSPPayload(raw json.RawMessage, root string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "null"
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	output := string(data)
	output = strings.ReplaceAll(output, fileURI(root)+"/", "")
	return output
}

type boundedWriter struct {
	target *bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	original := len(data)
	if w.target.Len() < w.limit {
		remaining := w.limit - w.target.Len()
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = w.target.Write(data)
	}
	return original, nil
}
