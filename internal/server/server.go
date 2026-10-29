// Package server carries the Go ↔ JS bridge over loopback HTTP: it serves
// the UI, takes commands as POST /api/cmd and streams events as
// Server-Sent Events from /api/events. A per-launch token guards the API.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ashgate-lite/internal/lite"
)

// Service is the part of lite.Service the bridge needs.
type Service interface {
	Handle(ctx context.Context, window, cmd string, args json.RawMessage) (any, error)
	Events() (<-chan []byte, func())
	Theme() string
}

// Server serves one app instance on 127.0.0.1.
type Server struct {
	svc   Service
	ui    fs.FS
	token string
	addr  string
	files http.Handler
}

// New prepares a server for svc with the UI files in ui.
func New(svc Service, ui fs.FS) *Server {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return &Server{svc: svc, ui: ui, token: hex.EncodeToString(b[:]), files: http.FileServerFS(ui)}
}

// Start listens on a random loopback port and serves until ctx ends.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// URL is the page address for window ("main" or "manage").
func (s *Server) URL(window string) string {
	q := url.Values{"token": {s.token}, "window": {window}}
	return "http://" + s.addr + "/?" + q.Encode()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only this address: a page on another host name (DNS rebinding) is refused.
	if r.Host != s.addr {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case r.URL.Path == "/api/cmd":
		s.command(w, r)
	case r.URL.Path == "/api/events":
		s.stream(w, r)
	case r.URL.Path == "/" || r.URL.Path == "/index.html":
		s.index(w)
	case r.Method == http.MethodGet:
		w.Header().Set("Cache-Control", "no-cache")
		s.files.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// index writes the page with the saved theme already on <html>, so the
// first paint has the right colors.
func (s *Server) index(w http.ResponseWriter) {
	page, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	html := strings.Replace(string(page), `data-theme="light"`, `data-theme="`+s.svc.Theme()+`"`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
	_, _ = w.Write([]byte(html))
}

type request struct {
	ID   string          `json:"id"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args"`
}

type reply struct {
	ID     string         `json:"id"`
	OK     bool           `json:"ok"`
	Result any            `json:"result,omitempty"`
	Error  *lite.CmdError `json:"error,omitempty"`
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Ashgate-Token") != s.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	window := r.Header.Get("X-Ashgate-Window")
	if window != "manage" {
		window = "main"
	}
	res, err := s.svc.Handle(r.Context(), window, req.Cmd, req.Args)
	out := reply{ID: req.ID, OK: err == nil, Result: res}
	if err != nil {
		out.Error = asCmdError(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func asCmdError(err error) *lite.CmdError {
	if ce, ok := err.(*lite.CmdError); ok {
		return ce
	}
	return &lite.CmdError{Code: "INTERNAL", Message: err.Error()}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != s.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, stop := s.svc.Events()
	defer stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-events:
			if !ok {
				return
			}
			if _, err := w.Write(append(append([]byte("data: "), msg...), '\n', '\n')); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
