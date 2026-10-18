// Package web serves the dashboard UI and its JSON/SSE API on localhost.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/tlmanz/PowerPilot/internal/logbook"
	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/update"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

//go:embed static
var staticFS embed.FS

// Logo returns the app logo PNG (the Linux app-menu icon).
func Logo() []byte {
	b, _ := staticFS.ReadFile("static/assets/logo.png")
	return b
}

// Updater checks for and installs new releases (package update).
type Updater interface {
	Check(context.Context) (update.Status, error)
	Apply(context.Context) (string, error)
	Restart() error
}

// Source is where the dashboard's UPS comes from: the USB monitor, or a UPS
// on a powerpilot-server in Remote mode (package cloud).
type Source interface {
	State() monitor.State
	Subscribe() (<-chan monitor.Update, func())
	Execute(id string, p ups.Params) (string, error)
	Poke()
	UpdateSettings(settings.Settings) error
	Note(level, msg string)
}

// Logs is the event and reading history: the logbook on disk, or the
// server's in Remote mode.
type Logs interface {
	Days() ([]logbook.Day, int64)
	Events(date string) ([]monitor.Event, error)
	Readings(date string) ([]logbook.Reading, error)
	ExportEvents(w io.Writer, from, to string) error
	ExportReadings(w io.Writer, from, to string) error
	Prune()
}

// Options connects the dashboard API to the rest of the service.
type Options struct {
	WindowFile string // where the dashboard window reports its size
	Updater    Updater
	Logs       Logs
	Cloud      Cloud // the Cloud setting; nil leaves it out
}

type server struct {
	Options
	mon   Source
	hosts map[string]bool
}

// Handler returns the dashboard for a server listening on listenAddr.
func Handler(mon Source, listenAddr string, opt Options) (http.Handler, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, err
	}
	s := &server{Options: opt, mon: mon, hosts: map[string]bool{
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
	mux.HandleFunc("PUT /api/window", s.putWindow)
	mux.HandleFunc("GET /api/update", s.checkUpdate)
	mux.HandleFunc("POST /api/update", s.applyUpdate)
	mux.HandleFunc("GET /api/logs", s.logDays)
	mux.HandleFunc("GET /api/logs/events", s.logEvents)
	mux.HandleFunc("GET /api/logs/readings", s.logReadings)
	mux.HandleFunc("GET /api/logs/export", s.logExport)
	mux.HandleFunc("GET /api/cloud", s.getCloud)
	mux.HandleFunc("PUT /api/cloud", s.putCloud)
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
	s.Logs.Prune() // the retention period may have shrunk
	writeJSON(w, http.StatusOK, v)
}

func (s *server) logDays(w http.ResponseWriter, r *http.Request) {
	days, size := s.Logs.Days()
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "bytes": size})
}

func (s *server) logEvents(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if !logbook.IsDate(date) {
		writeError(w, http.StatusBadRequest, errors.New("date must look like 2026-10-15"))
		return
	}
	evs, err := s.Logs.Events(date)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, evs)
}

func (s *server) logReadings(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if !logbook.IsDate(date) {
		writeError(w, http.StatusBadRequest, errors.New("date must look like 2026-10-15"))
		return
	}
	rs, err := s.Logs.Readings(date)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// logExport downloads one kind of log for a range of dates as CSV.
func (s *server) logExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, from, to := q.Get("kind"), q.Get("from"), q.Get("to")
	export := map[string]func(io.Writer, string, string) error{
		"events":   s.Logs.ExportEvents,
		"readings": s.Logs.ExportReadings,
	}[kind]
	switch {
	case export == nil:
		writeError(w, http.StatusBadRequest, errors.New("kind must be events or readings"))
		return
	case !logbook.IsDate(from) || !logbook.IsDate(to) || from > to:
		writeError(w, http.StatusBadRequest, errors.New("from and to must be dates like 2026-10-15, from first"))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="PowerPilot-%s-%s-to-%s.csv"`, kind, from, to))
	export(w, from, to) // too late for an error status once rows are out
}

// putWindow remembers the size of PowerPilot's own window (Linux, Windows) so
// "powerpilot app" can open it at that size next time.
func (s *server) putWindow(w http.ResponseWriter, r *http.Request) {
	var v settings.Window
	if !decode(w, r, &v) {
		return
	}
	if err := v.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := settings.SaveWindow(s.WindowFile, v); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	st, err := s.Updater.Check(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// applyUpdate installs the latest release and answers, then restarts the
// service on it; the dashboard reloads when it sees the new version.
func (s *server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	v, err := s.Updater.Apply(r.Context())
	switch {
	case errors.Is(err, update.ErrBlocked):
		writeError(w, http.StatusConflict, err)
		return
	case err != nil:
		s.mon.Note("warn", "Update failed: "+err.Error())
		writeError(w, http.StatusBadGateway, err)
		return
	}
	s.mon.Note("info", "Installed PowerPilot "+v+", restarting")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v})
	go func() {
		time.Sleep(500 * time.Millisecond) // let the reply reach the dashboard
		if err := s.Updater.Restart(); err != nil {
			s.mon.Note("alert", "Could not restart after the update: "+err.Error())
		}
	}()
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
