// Package web serves the dashboard UI and its JSON/SSE API on localhost.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

//go:embed static
var staticFS embed.FS

// Logo returns the app logo PNG (the Linux app-menu icon).
func Logo() []byte {
	b, _ := staticFS.ReadFile("static/assets/logo.png")
	return b
}

type server struct {
	mon   *monitor.Monitor
	hosts map[string]bool
}

// Handler returns the dashboard for a server listening on listenAddr.
func Handler(mon *monitor.Monitor, listenAddr string) (http.Handler, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, err
	}
	s := &server{mon: mon, hosts: map[string]bool{
		listenAddr:          true,
		"localhost:" + port: true,
		"127.0.0.1:" + port: true,
		"[::1]:" + port:     true,
	}}
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("POST /api/command", s.command)
	mux.HandleFunc("POST /api/refresh", s.refresh)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	return s.guard(mux), nil
}

// guard rejects requests other websites could make: a foreign Host header
// (DNS rebinding) or a cross-origin / non-JSON write (CSRF).
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Scheme != "http" || !s.hosts[u.Host] {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.State())
}

func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	updates, cancel := s.mon.Subscribe()
	defer cancel()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case u := <-updates:
			data, err := json.Marshal(u)
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

type commandRequest struct {
	ID string `json:"id"`
	ups.Params
}

func (s *server) command(w http.ResponseWriter, r *http.Request) {
	var req commandRequest
	if !decode(w, r, &req) {
		return
	}
	raw, err := s.mon.Execute(req.ID, req.Params)
	switch {
	case errors.Is(err, ups.ErrInvalidCommand):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, monitor.ErrDisconnected):
		writeError(w, http.StatusServiceUnavailable, err)
	case err != nil:
		writeError(w, http.StatusBadGateway, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": raw})
	}
}

func (s *server) refresh(w http.ResponseWriter, r *http.Request) {
	s.mon.Poke()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var v settings.Settings
	if !decode(w, r, &v) {
		return
	}
	if err := v.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.mon.UpdateSettings(v); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}
