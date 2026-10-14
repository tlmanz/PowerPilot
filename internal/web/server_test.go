package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
)

// newTestHandler uses a monitor that never runs, so no request reaches a UPS.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(time.Second, monitor.Device{RatedVA: 2000, RatedW: 1200}, store, monitor.Actions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h, err := Handler(mon, "127.0.0.1:8484")
	if err != nil {
		t.Fatal(err)
	}
	return h
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
		{"bad settings", "PUT", "/api/settings", okHost, okOrigin, js, `{"low_battery_percent":1,"notify":true,"shutdown_after_minutes":5,"battery_ah":9}`, 400},
		{"good settings", "PUT", "/api/settings", okHost, okOrigin, js, `{"low_battery_percent":25,"notify":true,"shutdown_after_minutes":5,"battery_ah":9}`, 200},
	}
	for _, c := range cases {
		w := do(h, c.method, c.path, c.host, c.origin, c.ctype, c.body)
		if w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
}
