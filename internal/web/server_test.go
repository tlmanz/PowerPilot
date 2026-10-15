package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/logbook"
	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/update"
)

// newTestHandler uses a monitor that never runs, so no request reaches a UPS.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(time.Second, monitor.Device{RatedVA: 2000, RatedW: 1200}, store, monitor.Actions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h, err := Handler(mon, "127.0.0.1:8484", testOptions(t, &fakeUpdater{}))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func testOptions(t *testing.T, upd Updater) Options {
	t.Helper()
	logs := logbook.Open(t.TempDir(), func() int { return 90 }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return Options{WindowFile: filepath.Join(t.TempDir(), "window.json"), Updater: upd, Logs: logs}
}

// fakeUpdater offers v9.9.9 and never touches the program or restarts.
type fakeUpdater struct{ restarted chan struct{} }

func (f *fakeUpdater) Check(context.Context) (update.Status, error) {
	return update.Status{Current: "v1.0.0", Latest: "v9.9.9", Available: true}, nil
}

func (f *fakeUpdater) Apply(context.Context) (string, error) { return "v9.9.9", nil }

func (f *fakeUpdater) Restart() error {
	if f.restarted != nil {
		close(f.restarted)
	}
	return nil
}

func do(h http.Handler, method, path, host, origin, ctype, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuard(t *testing.T) {
	h := newTestHandler(t)
	const okHost, okOrigin, js = "127.0.0.1:8484", "http://127.0.0.1:8484", "application/json"
	cases := []struct {
		name                       string
		method, path, host, origin string
		ctype, body                string
		want                       int
	}{
		{"page loads", "GET", "/", okHost, "", "", "", 200},
		{"state loads via localhost", "GET", "/api/state", "localhost:8484", "", "", "", 200},
		{"DNS rebinding host", "GET", "/api/state", "evil.example:8484", "", "", "", 403},
		{"cross-site command", "POST", "/api/command", okHost, "https://evil.example", js, `{"id":"shutdown.cancel"}`, 403},
		{"null origin command", "POST", "/api/command", okHost, "null", js, `{"id":"shutdown.cancel"}`, 403},
		{"form-encoded command", "POST", "/api/command", okHost, okOrigin, "text/plain", `{"id":"shutdown.cancel"}`, 415},
		{"unknown command", "POST", "/api/command", okHost, okOrigin, js, `{"id":"S00R0000"}`, 400},
		{"bad shutdown delay", "POST", "/api/command", okHost, okOrigin, js, `{"id":"shutdown","minutes":0.25}`, 400},
		{"unknown field", "POST", "/api/command", okHost, okOrigin, js, `{"id":"test.quick","raw":"S00"}`, 400},
		{"valid command, no UPS", "POST", "/api/command", okHost, okOrigin, js, `{"id":"test.quick"}`, 503},
		{"bad settings", "PUT", "/api/settings", okHost, okOrigin, js, `{"low_battery_percent":1,"notify":true,"shutdown_after_minutes":5,"battery_ah":9,"log_retention_days":90}`, 400},
		{"good settings", "PUT", "/api/settings", okHost, okOrigin, js, `{"low_battery_percent":25,"notify":true,"shutdown_after_minutes":5,"battery_ah":9,"log_retention_days":90}`, 200},
		{"logs kept 0 days", "PUT", "/api/settings", okHost, okOrigin, js, `{"low_battery_percent":25,"notify":true,"shutdown_after_minutes":5,"battery_ah":9,"log_retention_days":0}`, 400},
		{"tiny window", "PUT", "/api/window", okHost, okOrigin, js, `{"width":10,"height":10}`, 400},
		{"cross-site window", "PUT", "/api/window", okHost, "https://evil.example", js, `{"width":1300,"height":800}`, 403},
		{"window size", "PUT", "/api/window", okHost, okOrigin, js, `{"width":1300,"height":800}`, 200},
		{"update check", "GET", "/api/update", okHost, "", "", "", 200},
		{"cross-site update", "POST", "/api/update", okHost, "https://evil.example", js, `{}`, 403},
		{"form-encoded update", "POST", "/api/update", okHost, okOrigin, "text/plain", `{}`, 415},
		{"log days", "GET", "/api/logs", okHost, "", "", "", 200},
		{"events for a day", "GET", "/api/logs/events?date=2026-10-15", okHost, "", "", "", 200},
		{"events, bad date", "GET", "/api/logs/events?date=../../etc/passwd", okHost, "", "", "", 400},
		{"readings for a day", "GET", "/api/logs/readings?date=2026-10-15", okHost, "", "", "", 200},
		{"export events", "GET", "/api/logs/export?kind=events&from=2026-10-01&to=2026-10-15", okHost, "", "", "", 200},
		{"export, unknown kind", "GET", "/api/logs/export?kind=settings&from=2026-10-01&to=2026-10-15", okHost, "", "", "", 400},
		{"export, backwards range", "GET", "/api/logs/export?kind=readings&from=2026-10-15&to=2026-10-01", okHost, "", "", "", 400},
		{"export via rebinding host", "GET", "/api/logs/export?kind=events&from=2026-10-01&to=2026-10-15", "evil.example:8484", "", "", "", 403},
	}
	for _, c := range cases {
		w := do(h, c.method, c.path, c.host, c.origin, c.ctype, c.body)
		if w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestApplyUpdateRestarts(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(time.Second, monitor.Device{}, store, monitor.Actions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	upd := &fakeUpdater{restarted: make(chan struct{})}
	h, err := Handler(mon, "127.0.0.1:8484", testOptions(t, upd))
	if err != nil {
		t.Fatal(err)
	}
	w := do(h, "POST", "/api/update", "127.0.0.1:8484", "http://127.0.0.1:8484", "application/json", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"v9.9.9"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	select {
	case <-upd.restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the service didn't restart after installing the update")
	}
}

func TestLogExport(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(time.Second, monitor.Device{}, store, monitor.Actions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	opt := testOptions(t, &fakeUpdater{})
	h, err := Handler(mon, "127.0.0.1:8484", opt)
	if err != nil {
		t.Fatal(err)
	}
	// Log one event through the logbook's own writer.
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { opt.Logs.Run(done); close(finished) }()
	when := time.Date(2026, 10, 14, 21, 5, 0, 0, time.Local)
	opt.Logs.Event(monitor.Event{Time: when, Kind: "utility_lost", Level: "alert", Message: "Utility power lost"})
	close(done)
	<-finished

	w := do(h, "GET", "/api/logs/export?kind=events&from=2026-10-14&to=2026-10-15", "127.0.0.1:8484", "", "", "")
	if w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="PowerPilot-events-2026-10-14-to-2026-10-15.csv"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	want := "time,level,kind,message\n2026-10-14 21:05:00,alert,utility_lost,Utility power lost\n"
	if got := w.Body.String(); got != want {
		t.Errorf("CSV =\n%s\nwant\n%s", got, want)
	}
}
