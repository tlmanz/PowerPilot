package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newStore(t *testing.T, edit func(*settings.Settings)) *settings.Store {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		s := store.Get()
		edit(&s)
		store.Set(s)
	}
	return store
}

func TestApplySetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloud.json")
	s := NewSettings(path, Config{Mode: ModeOff}, nil, func() monitor.Device { return monitor.Device{} })
	var checked Config
	s.check = func(_ context.Context, c Config) error { checked = c; return nil }

	st, err := s.Apply(context.Background(), Request{Mode: ModeRemote,
		Link: "powerpilot://pair?device=home&token=pp_secret&url=https%3A%2F%2Fups.example.com"})
	if err != nil || st.URL != "https://ups.example.com" || st.Device != "home" || !st.TokenSet || checked.Token != "pp_secret" {
		t.Fatalf("status %+v err %v checked %+v", st, err, checked)
	}
	if b, _ := json.Marshal(s.Status()); strings.Contains(string(b), "pp_secret") {
		t.Fatal("the token reached the dashboard")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("cloud.json mode %v", fi.Mode().Perm())
	}
	// Switching mode by hand keeps the saved token; a bare host gets https://.
	if _, err := s.Apply(context.Background(), Request{Mode: ModePublish, URL: "ups.example.com", Device: "desk"}); err != nil || checked.Token != "pp_secret" || checked.URL != "https://ups.example.com" {
		t.Fatalf("publish: %v %+v", err, checked)
	}
	if c, _ := Load(path); c.Mode != ModePublish || c.Device != "desk" || c.Token != "pp_secret" {
		t.Fatalf("saved %+v", c)
	}
	for _, bad := range []Request{
		{Mode: ModeRemote, Link: "https://example.com/?token=x"},
		{Mode: ModeRemote, Device: "Home!"},
		{Mode: "both"},
	} {
		if _, err := s.Apply(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	s.check = func(context.Context, Config) error { return errors.New("the server didn't accept the token") }
	if _, err := s.Apply(context.Background(), Request{Mode: ModeRemote, Device: "home"}); err == nil {
		t.Fatal("a failed check was saved")
	}
}

// fakeServer plays powerpilot-server's app API for device "home".
func fakeServer(t *testing.T, stream func(w http.ResponseWriter, flush func())) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer pp_t" {
				http.Error(w, `{"ok":false,"error":"missing or unknown token"}`, http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/devices/home/state", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device":{"brand":"Prolink","model":"PRO2000SFCU","app_version":"esp32-0.1.0"},
			"snapshot":{"connected":true,"mode":"line","battery_state":"full","status":{"on_battery":false}},
			"history":null,"events":null,"settings":{"low_battery_percent":20,"notify":true,"shutdown_after_minutes":5,"battery_ah":18,"log_retention_days":90},
			"last_test":{"kind":"test","started":"2026-10-20T10:00:00Z","ended":"2026-10-20T10:00:10Z","result":"passed"},"interval_ms":5000,"online":true}`)
	}))
	mux.HandleFunc("GET /api/devices/home/stream", auth(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		fmt.Fprint(w, ": ok\n\n")
		f.Flush()
		stream(w, f.Flush)
		<-r.Context().Done()
	}))
	mux.HandleFunc("POST /api/devices/home/command", auth(func(w http.ResponseWriter, r *http.Request) {
		var c struct{ ID string }
		json.NewDecoder(r.Body).Decode(&c)
		switch c.ID {
		case "test.quick":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"ok":false,"error":"UPS is not connected"}`)
		default:
			fmt.Fprint(w, `{"ok":true,"sent":"Q"}`)
		}
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteNotifiesAndShutsDown(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	srv := fakeServer(t, func(w http.ResponseWriter, flush func()) {
		// Mains power fails; two minutes on, still on battery.
		fmt.Fprintf(w, `data: {"snapshot":{"connected":true,"mode":"battery","battery_state":"discharging","on_battery_sec":120,"charge":87,"runtime_min":22.4,"load_w":372,"status":{"on_battery":true,"load_percent":31}},"events":[{"id":"e1","time":%q,"kind":"utility_lost","level":"alert","message":"Utility power lost"}],"online":true}`+"\n\n", now)
		flush()
	})
	notes := make(chan string, 10)
	shut := make(chan struct{}, 1)
	store := newStore(t, func(s *settings.Settings) { s.ShutdownMac, s.ShutdownAfterMinutes = true, 1 })
	actions := monitor.Actions{
		Notify:   func(title, msg string) error { notes <- title + "|" + msg; return nil },
		ShutDown: func() error { shut <- struct{}{}; return nil },
	}
	r := NewRemote(Config{URL: srv.URL, Device: "home", Token: "pp_t"}, store, actions, "v9", quiet)
	done := make(chan struct{})
	defer close(done)
	go r.Run(done)

	want := "Running on battery|home: Mains power was lost. The UPS is powering your equipment.\n" +
		"Battery 87.0%, about 22 min left · Load 31.0% (372 W)"
	for got := ""; got != want; {
		select {
		case got = <-notes:
		case <-time.After(5 * time.Second):
			t.Fatalf("no outage notification")
		}
	}
	select {
	case <-shut:
	case <-time.After(5 * time.Second):
		t.Fatal("no shutdown after a minute on battery")
	}

	st := r.State()
	if !st.Snapshot.Connected || st.Snapshot.Status == nil || !st.Snapshot.Status.OnBattery || st.Device.AppVersion != "v9" ||
		!strings.Contains(st.Device.Connection, "Cloud: home") || st.Settings.BatteryAh != 18 || len(st.Events) < 2 {
		t.Fatalf("state %+v", st)
	}

	if sent, err := r.Execute("beeper.off", ups.Params{}); err != nil || sent != "Q" {
		t.Fatalf("command: %q %v", sent, err)
	}
	if _, err := r.Execute("test.quick", ups.Params{}); !errors.Is(err, monitor.ErrDisconnected) || err.Error() != "UPS is not connected" {
		t.Fatalf("UPS gone: %v", err)
	}
	if _, err := r.Execute("shutdown", ups.Params{Minutes: 30}); !errors.Is(err, ups.ErrInvalidCommand) {
		t.Fatalf("bad command: %v", err)
	}
}

// Remote mode passes on the server's last self-test: from its state, then
// from the stream when one ends, and keeps it through later updates.
func TestRemoteLastTest(t *testing.T) {
	ended := time.Now().UTC().Truncate(time.Second)
	at := ended.Format(time.RFC3339)
	srv := fakeServer(t, func(w http.ResponseWriter, flush func()) {
		fmt.Fprintf(w, `data: {"snapshot":{"connected":true,"mode":"line","status":{}},"events":[{"id":"e2","time":%q,"kind":"self_test","level":"alert","message":"UPS self-test failed","test":{"result":"failed","seconds":6,"load":9,"volts_before":27.1,"volts_low":22.8}}],"last_test":{"kind":"test","started":%q,"ended":%q,"result":"failed"},"online":true}`+"\n\n", at, at, at)
		fmt.Fprint(w, `data: {"snapshot":{"connected":true,"mode":"line","status":{}},"online":true}`+"\n\n")
		flush()
	})
	r := NewRemote(Config{URL: srv.URL, Device: "home", Token: "pp_t"}, newStore(t, nil), monitor.Actions{}, "v9", quiet)
	updates, stop := r.Subscribe()
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go r.Run(done)

	var results []string
	for timeout := time.After(5 * time.Second); len(results) < 3; {
		select {
		case u := <-updates:
			if u.LastTest == nil {
				t.Fatalf("an update without last_test after %v", results)
			}
			results = append(results, u.LastTest.Result)
		case <-timeout:
			t.Fatalf("updates' last_test: %v", results)
		}
	}
	if strings.Join(results, ",") != "passed,failed,failed" {
		t.Fatalf("updates' last_test: %v", results)
	}
	st := r.State()
	if lt := st.LastTest; lt == nil || lt.Result != "failed" || lt.Ended == nil || !lt.Ended.Equal(ended) {
		t.Fatalf("state's last_test %+v", lt)
	}
	if e := st.Events[len(st.Events)-1]; e.Test == nil || e.Test.VoltsLow != 22.8 {
		t.Fatalf("the self-test's event lost its measurements: %+v", e)
	}
}

// A server from before battery health has no routes for it.
func TestRemoteBatteryOldServer(t *testing.T) {
	srv := fakeServer(t, func(http.ResponseWriter, func()) {})
	r := NewRemote(Config{URL: srv.URL, Device: "home", Token: "pp_t"}, newStore(t, nil), monitor.Actions{}, "v9", quiet)
	_, _, err := r.Battery(context.Background(), http.MethodGet, "/battery", nil)
	if err == nil || err.Error() != "this server doesn't work out battery health yet: update powerpilot-server" {
		t.Fatalf("old server: %v", err)
	}
}

func TestRemoteOutOfSight(t *testing.T) {
	notes := make(chan string, 10)
	store := newStore(t, nil)
	r := NewRemote(Config{URL: "http://127.0.0.1:1", Device: "home", Token: "pp_t"}, store,
		monitor.Actions{Notify: func(title, msg string) error { notes <- title; return nil }}, "v9", quiet)
	start := time.Now()
	r.mu.Lock()
	r.checkLocked(start)
	r.checkLocked(start.Add(lostGrace + time.Second))
	r.mu.Unlock()
	select {
	case got := <-notes:
		if got != "UPS out of contact" {
			t.Fatalf("note %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no out-of-contact notification")
	}
	if st := r.State(); st.Snapshot.Connected || !strings.Contains(st.Snapshot.Error, "server") {
		t.Fatalf("snapshot %+v", st.Snapshot)
	}
}

func TestRemoteSummaryDuringOutage(t *testing.T) {
	notes := make(chan string, 10)
	r := NewRemote(Config{URL: "http://127.0.0.1:1", Device: "home", Token: "pp_t"}, newStore(t, nil),
		monitor.Actions{Notify: func(title, msg string) error { notes <- title + "|" + msg; return nil }}, "v9", quiet)
	start := time.Now()
	r.mu.Lock()
	r.reachable, r.online = true, true
	r.snap = monitor.Snapshot{Connected: true, Mode: "battery", OnBatterySec: 120, Charge: new(64.0), RuntimeMin: new(14.2), LoadW: 372,
		Status: &ups.Status{OnBattery: true, LoadPercent: 31}}
	r.checkLocked(start)
	r.checkLocked(start.Add(monitor.SummaryEvery - time.Second))
	r.checkLocked(start.Add(monitor.SummaryEvery))
	r.mu.Unlock()
	want := "On battery for 7 min|Battery 64.0%, about 14 min left · Load 31.0% (372 W)"
	select {
	case got := <-notes:
		if got != want {
			t.Fatalf("note %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no summary notification")
	}
	select {
	case got := <-notes:
		t.Fatalf("unexpected note %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// fakeMonitor is the USB monitor as the publisher sees it.
type fakeMonitor struct {
	updates chan monitor.Update
	mu      sync.Mutex
	ran     []string
	saved   []settings.Settings
}

func (f *fakeMonitor) Subscribe() (<-chan monitor.Update, func()) { return f.updates, func() {} }
func (f *fakeMonitor) Execute(id string, p ups.Params) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, id)
	return "Q", nil
}
func (f *fakeMonitor) UpdateSettings(s settings.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, s)
	return nil
}

func TestPublisher(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]string{}
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.Method+" "+r.URL.Path] = append(got[r.Method+" "+r.URL.Path], string(b))
		mu.Unlock()
	}
	mux := http.NewServeMux()
	for _, route := range []string{"PUT /api/publish/desk/info", "POST /api/publish/desk/state", "POST /api/publish/desk/events",
		"POST /api/publish/desk/readings", "POST /api/publish/desk/results"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) { record(r); fmt.Fprint(w, `{"ok":true}`) })
	}
	soon, late := time.Now().Add(time.Minute).UnixMilli(), time.Now().Add(-time.Minute).UnixMilli()
	mux.HandleFunc("GET /api/publish/desk/commands", func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		fmt.Fprint(w, "event: config\ndata: {\"battery_ah\":18,\"low_battery_percent\":25}\n\n")
		for _, c := range []string{
			fmt.Sprintf(`{"cmd_id":"c1","id":"beeper.off","params":{"minutes":0,"restore_after":0},"expires_at":%d}`, soon),
			fmt.Sprintf(`{"cmd_id":"c1","id":"beeper.off","params":{"minutes":0,"restore_after":0},"expires_at":%d}`, soon), // a repeat
			fmt.Sprintf(`{"cmd_id":"c2","id":"test.quick","params":{"minutes":0,"restore_after":0},"expires_at":%d}`, late),
		} {
			fmt.Fprintf(w, "event: cmd\ndata: %s\n\n", c)
		}
		f.Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mon := &fakeMonitor{updates: make(chan monitor.Update, 4)}
	p := NewPublisher(Config{URL: srv.URL, Device: "desk", Token: "pp_t"}, newStore(t, nil), monitor.Device{Brand: "Prolink"}, quiet)
	p.Attach(mon)
	p.Event(monitor.Event{Time: time.Now(), Kind: "utility_lost", Level: "alert", Message: "Utility power lost"})
	// Battery health's two facts go up as the monitor and the logbook make them.
	p.Event(monitor.Event{Time: time.Now(), Kind: "self_test", Level: "good", Message: "UPS self-test completed (passed)",
		Test: &monitor.SelfTest{Result: "passed", Seconds: 10, Load: 9, VoltsBefore: 27.1, VoltsLow: 25.9}})
	base := time.Now().Truncate(time.Minute).Add(-2 * time.Minute).UnixMilli()
	p.Sample(monitor.Sample{T: base, In: 230}, "line")
	p.Sample(monitor.Sample{T: base + 2000, In: 0}, "battery")
	p.Sample(monitor.Sample{T: base + 4000, In: 0}, "battery")
	p.Sample(monitor.Sample{T: base + 60_000, In: 231}, "line") // starts the next minute: the first is done
	mon.updates <- monitor.Update{Snapshot: monitor.Snapshot{Connected: true, Mode: "line", BatteryState: "full"}}
	done := make(chan struct{})
	defer close(done)
	go p.Run(done)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		ready := len(got["POST /api/publish/desk/results"]) >= 2 && len(got["POST /api/publish/desk/readings"]) >= 1 &&
			len(got["POST /api/publish/desk/events"]) >= 1 && len(got["POST /api/publish/desk/state"]) >= 1 && len(got["PUT /api/publish/desk/info"]) >= 1
		mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not everything published: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if ev := got["POST /api/publish/desk/events"][0]; !strings.Contains(ev, `"id":"desk-`) || !strings.Contains(ev, "utility_lost") ||
		!strings.Contains(ev, `"message":"UPS self-test completed (passed)","test":{"result":"passed","seconds":10,"load":9,"volts_before":27.1,"volts_low":25.9}}`) ||
		strings.Count(ev, `"test":`) != 1 {
		t.Fatalf("events %s", ev)
	}
	if rd := got["POST /api/publish/desk/readings"][0]; !strings.Contains(rd, `"mode":"battery"`) || !strings.Contains(rd, `"batt_s":4}`) {
		t.Fatalf("readings %s", rd)
	}
	res := strings.Join(got["POST /api/publish/desk/results"], "\n")
	if !strings.Contains(res, `{"cmd_id":"c1","status":"done","raw":"Q"}`) || !strings.Contains(res, `"cmd_id":"c2","status":"expired"`) {
		t.Fatalf("results %s", res)
	}
	mon.mu.Lock()
	defer mon.mu.Unlock()
	if len(mon.ran) != 1 || mon.ran[0] != "beeper.off" {
		t.Fatalf("ran %v: the repeat or the expired command ran", mon.ran)
	}
	if len(mon.saved) != 1 || mon.saved[0].BatteryAh != 18 || mon.saved[0].LowBatteryPercent != 25 {
		t.Fatalf("config applied %+v", mon.saved)
	}
}
