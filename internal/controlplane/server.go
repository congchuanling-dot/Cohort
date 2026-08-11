package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	controlSessionCookie = "cohort_control_session"
	maxRequestBodyBytes  = 1 << 20
)

type ServerConfig struct {
	ProjectRoot string
	Listen      string
	StaticFS    fs.FS
	Catalog     *Catalog
	Snapshot    SnapshotProvider
	Projects    *ProjectRegistry
}

type SnapshotProvider func(context.Context, string) (any, error)

type Server struct {
	projectRoot   string
	listen        string
	staticFS      fs.FS
	catalog       *Catalog
	snapshot      SnapshotProvider
	projects      *ProjectRegistry
	operations    *OperationManager
	bootstrap     string
	session       string
	csrf          string
	origin        string
	bootstrapOnce sync.Once
}

type RunningServer struct {
	Address      string
	URL          string
	BootstrapURL string
	Server       *Server
	httpServer   *http.Server
	listener     net.Listener
}

func NewServer(config ServerConfig) (*Server, error) {
	if strings.TrimSpace(config.ProjectRoot) == "" {
		return nil, errors.New("control plane project root is required")
	}
	if config.Catalog == nil {
		return nil, errors.New("control plane action catalog is required")
	}
	listen := strings.TrimSpace(config.Listen)
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	if err := validateLoopbackListen(listen); err != nil {
		return nil, err
	}
	operations, err := NewOperationManager(config.ProjectRoot, config.Catalog)
	if err != nil {
		return nil, err
	}
	return &Server{
		projectRoot: config.ProjectRoot,
		listen:      listen,
		staticFS:    config.StaticFS,
		catalog:     config.Catalog,
		snapshot:    config.Snapshot,
		projects:    config.Projects,
		operations:  operations,
		bootstrap:   randomToken(24),
		session:     randomToken(32),
		csrf:        randomToken(24),
	}, nil
}

func (s *Server) Start(ctx context.Context) (*RunningServer, error) {
	if s == nil {
		return nil, errors.New("control plane server is nil")
	}
	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return nil, err
	}
	address := listener.Addr().String()
	s.origin = "http://" + address
	httpServer := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}
	running := &RunningServer{
		Address: address, URL: s.origin,
		BootstrapURL: s.origin + "/#token=" + url.QueryEscape(s.bootstrap),
		Server:       s, httpServer: httpServer, listener: listener,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		s.operations.Close()
	}()
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return running, nil
}

func (r *RunningServer) Close(ctx context.Context) error {
	if r == nil || r.httpServer == nil {
		return nil
	}
	if r.Server != nil && r.Server.operations != nil {
		r.Server.operations.Close()
	}
	return r.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/auth/bootstrap", s.handleBootstrap)
	mux.Handle("/api/v1/auth/session", s.requireSession(http.HandlerFunc(s.handleSession)))
	mux.Handle("/api/v1/catalog", s.requireSession(http.HandlerFunc(s.handleCatalog)))
	mux.Handle("/api/v1/snapshot", s.requireSession(http.HandlerFunc(s.handleSnapshot)))
	mux.Handle("/api/v1/projects", s.requireSession(http.HandlerFunc(s.handleProjects)))
	mux.Handle("/api/v1/actions/", s.requireSession(http.HandlerFunc(s.handleAction)))
	mux.Handle("/api/v1/operations", s.requireSession(http.HandlerFunc(s.handleOperations)))
	mux.Handle("/api/v1/operations/", s.requireSession(http.HandlerFunc(s.handleOperation)))
	mux.Handle("/api/v1/events", s.requireSession(http.HandlerFunc(s.handleEventStream)))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeControlError(w, http.StatusNotFound, "API endpoint not found")
	})
	mux.Handle("/", s.staticHandler())
	return s.securityHeaders(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "project_root": s.projectRoot,
	})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.validOrigin(r) {
		writeControlError(w, http.StatusForbidden, "invalid origin")
		return
	}
	token := strings.TrimSpace(r.Header.Get("X-Cohort-Bootstrap"))
	if token == "" || !constantTimeEqual(token, s.bootstrap) {
		writeControlError(w, http.StatusUnauthorized, "invalid or expired bootstrap token")
		return
	}
	accepted := false
	s.bootstrapOnce.Do(func() {
		accepted = true
		s.bootstrap = ""
	})
	if !accepted {
		writeControlError(w, http.StatusUnauthorized, "bootstrap token already used")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: controlSessionCookie, Value: s.session, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: 12 * 60 * 60,
	})
	writeControlJSON(w, http.StatusOK, map[string]any{"csrf_token": s.csrf})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrf_token":    s.csrf,
		"project_root":  s.projectRoot,
	})
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{"actions": s.catalog.List()})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.snapshot == nil {
		writeControlError(w, http.StatusNotImplemented, "dashboard snapshot is unavailable")
		return
	}
	snapshot, err := s.snapshot(r.Context(), s.projectRoot)
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.projects == nil {
		writeControlJSON(w, http.StatusOK, map[string]any{"projects": []ProjectRecord{}})
		return
	}
	projects, err := s.projects.List()
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.validMutation(r, w) {
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/v1/actions/")
	actionID, operationName, found := strings.Cut(suffix, "/")
	if !found || operationName != "execute" || actionID == "" || strings.Contains(actionID, "/") {
		writeControlError(w, http.StatusNotFound, "action endpoint not found")
		return
	}
	var request struct {
		Input        map[string]any `json:"input"`
		Confirmation string         `json:"confirmation"`
	}
	if err := decodeControlJSON(w, r, &request); err != nil {
		return
	}
	operation, err := s.operations.Start(context.Background(), actionID, ActionRequest{
		ProjectRoot:  s.projectRoot,
		Actor:        "local-user",
		Input:        request.Input,
		Confirmation: request.Confirmation,
	})
	if err != nil {
		writeControlError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeControlJSON(w, http.StatusAccepted, operation)
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	operations, err := s.operations.List(100)
	if err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]any{"operations": operations})
}

func (s *Server) handleOperation(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/v1/operations/")
	id, action, hasAction := strings.Cut(suffix, "/")
	if id == "" || strings.Contains(id, "/") {
		writeControlError(w, http.StatusBadRequest, "invalid operation id")
		return
	}
	if r.Method == http.MethodGet && !hasAction {
		operation, err := s.operations.Load(id)
		if err != nil {
			writeControlError(w, http.StatusNotFound, err.Error())
			return
		}
		writeControlJSON(w, http.StatusOK, operation)
		return
	}
	if r.Method == http.MethodPost && hasAction && action == "cancel" {
		if !s.validMutation(r, w) {
			return
		}
		operation, err := s.operations.Cancel(id)
		if err != nil {
			writeControlError(w, http.StatusConflict, err.Error())
			return
		}
		writeControlJSON(w, http.StatusAccepted, operation)
		return
	}
	writeControlError(w, http.StatusNotFound, "operation endpoint not found")
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeControlError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.operations.Subscribe(64)
	defer unsubscribe()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(controlSessionCookie)
		if err != nil || !constantTimeEqual(cookie.Value, s.session) {
			writeControlError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validMutation(r *http.Request, w http.ResponseWriter) bool {
	if !s.validOrigin(r) {
		writeControlError(w, http.StatusForbidden, "invalid origin")
		return false
	}
	if !constantTimeEqual(strings.TrimSpace(r.Header.Get("X-CSRF-Token")), s.csrf) {
		writeControlError(w, http.StatusForbidden, "invalid CSRF token")
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		writeControlError(w, http.StatusUnsupportedMediaType, "application/json is required")
		return false
	}
	return true
}

func (s *Server) validOrigin(r *http.Request) bool {
	return constantTimeEqual(strings.TrimSuffix(r.Header.Get("Origin"), "/"), s.origin)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) staticHandler() http.Handler {
	if s.staticFS == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Cohort Control Center frontend is not embedded", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(s.staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "." || clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(s.staticFS, clean); err != nil {
			cloned := r.Clone(r.Context())
			cloned.URL.Path = "/"
			fileServer.ServeHTTP(w, cloned)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func decodeControlJSON(w http.ResponseWriter, r *http.Request, target any) error {
	reader := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeControlError(w, http.StatusBadRequest, err.Error())
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain one JSON value")
		}
		writeControlError(w, http.StatusBadRequest, err.Error())
		return err
	}
	return nil
}

func writeControlJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeControlError(w http.ResponseWriter, status int, message string) {
	writeControlJSON(w, status, map[string]any{"error": message})
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("control plane may only listen on loopback")
	}
	return nil
}

func randomToken(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func constantTimeEqual(left string, right string) bool {
	if len(left) != len(right) || left == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
