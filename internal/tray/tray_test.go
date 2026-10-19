package tray

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

func reading(edit func(*monitor.Snapshot)) monitor.State {
	charge, runtime := 100.0, 45.0
	s := monitor.Snapshot{
		Connected: true, Mode: "line", BatteryState: "full", Charge: &charge, RuntimeMin: &runtime, LoadW: 372,
		Status: &ups.Status{InputVoltage: 230, OutputVoltage: 230, LoadPercent: 31, Frequency: 50, BatteryVoltage: 27.2, BeeperOn: true},
	}
	if edit != nil {
		edit(&s)
	}
	return monitor.State{Snapshot: s}
}

func TestViewOf(t *testing.T) {
	v := viewOf(reading(nil), true)
	want := [4]string{"On mains power", "Battery 100.0%, full", "Load 31.0% (372 W)", "Input 230.0 V · Output 230.0 V · 50.0 Hz"}
	if v.rows != want || v.title != "" || v.mute || v.glyph != (glyph{level: 20, badge: bolt, tone: good}) ||
		v.tooltip != "PowerPilot: On mains power · Battery 100.0%" {
		t.Fatalf("on mains: %+v", v)
	}

	outage := reading(func(s *monitor.Snapshot) {
		*s.Charge, s.Mode, s.BatteryState, s.OnBatterySec, s.AlarmActive = 62.4, "battery", "discharging", 300, true
		s.Status.OnBattery, s.Status.InputVoltage = true, 0
	})
	v = viewOf(outage, true)
	if v.rows[0] != "On battery for 5 min" || v.rows[1] != "Battery 62.4%, about 45 min left" || v.title != "62.4%" ||
		!v.mute || v.glyph != (glyph{level: 12, tone: warn}) {
		t.Fatalf("on battery: %+v", v)
	}
	outage.Snapshot.AlarmMuted = true
	if viewOf(outage, true).mute {
		t.Fatal("offers to mute an alarm already muted")
	}

	low := reading(func(s *monitor.Snapshot) { *s.Charge, s.BatteryState, s.Status.OnBattery = 8, "low", true })
	if v := viewOf(low, true); v.glyph != (glyph{level: 2, badge: bang, tone: alert}) || v.rows[0] != "Battery low" || v.title != "8.0%" {
		t.Fatalf("low battery: %+v", v)
	}

	gone := monitor.State{Snapshot: monitor.Snapshot{Mode: "offline", Error: "Cloud: dial tcp 10.0.0.2:443: connect: connection refused, and some more words to clip"}}
	if v := viewOf(gone, true); v.rows[0] != "UPS not connected" || len([]rune(v.rows[1])) != 60 || !strings.HasSuffix(v.rows[1], "…") ||
		v.rows[2] != "" || v.glyph.tone != off {
		t.Fatalf("disconnected: %+v", v)
	}
	if v := viewOf(monitor.State{}, false); v.rows[0] != "PowerPilot service not running" || v.rows[1] != "" || v.glyph.tone != off {
		t.Fatalf("no service: %+v", v)
	}
}

func TestIcons(t *testing.T) {
	g := glyph{level: 13, badge: bolt, tone: good}
	img, err := png.Decode(bytes.NewReader(pngOf(g.drawing(true), 32)))
	if err != nil || img.Bounds().Dx() != 32 {
		t.Fatalf("PNG: %v %v", err, img)
	}
	// The battery's body is drawn; the corners are clear.
	if _, _, _, a := img.At(3, 16).RGBA(); a == 0 {
		t.Error("no outline at the left edge")
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Error("corner not transparent")
	}

	ico := icoOf(g.drawing(false))
	var head [3]uint16
	binary.Read(bytes.NewReader(ico), binary.LittleEndian, &head)
	if head != [3]uint16{0, 1, 4} {
		t.Fatalf("ICO header %v", head)
	}
	for i, size := range []int{16, 24, 32, 48} {
		entry := ico[6+16*i:]
		n, off := binary.LittleEndian.Uint32(entry[8:]), binary.LittleEndian.Uint32(entry[12:])
		img, err := png.Decode(bytes.NewReader(ico[off : off+n]))
		if int(entry[0]) != size || err != nil || img.Bounds().Dx() != size {
			t.Errorf("ICO image %d: width byte %d, %v", i, entry[0], err)
		}
	}
}

func TestMenuSymbols(t *testing.T) {
	for name, s := range map[string]shape{
		"gauge": gaugeSymbol, "plug": plugSymbol, "mute": muteSymbol, "info": infoSymbol,
		"window": windowSymbol, "power": powerSymbol, "dot": dotSymbol,
	} {
		img := symbol(s, gray)(16)
		inked := 0
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 128 {
				inked++
			}
		}
		// Something drawn, and neither a blot nor a speck at menu size.
		if inked < 12 || inked > 16*16*2/3 {
			t.Errorf("%s: %d of 256 pixels inked", name, inked)
		}
	}
}

// fakeService serves /api/state and a stream that sends updates, then ends.
func fakeService(t *testing.T, updates ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device":{"app_version":"v1.2.0"},"snapshot":{"connected":true,"mode":"line","status":{"load_percent":12}},"history":[{"t":1}]}`)
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": ping\n\n")
		for _, u := range updates {
			fmt.Fprintf(w, "data: %s\n\n", u)
		}
	})
	mux.HandleFunc("POST /api/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "content type", http.StatusUnsupportedMediaType)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"ok":false,"error":"UPS is not connected"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFollow(t *testing.T) {
	srv := fakeService(t, `{"snapshot":{"connected":true,"mode":"battery","status":{"on_battery":true,"load_percent":40}}}`)
	type seen struct {
		st monitor.State
		ok bool
	}
	got := make(chan seen, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go follow(ctx, srv.URL, func(st monitor.State, ok bool) { got <- seen{st, ok} })

	next := func() seen {
		select {
		case s := <-got:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no update")
			return seen{}
		}
	}
	first := next()
	if !first.ok || first.st.Device.AppVersion != "v1.2.0" || first.st.History != nil || first.st.Snapshot.Status.LoadPercent != 12 {
		t.Fatalf("state: %+v", first)
	}
	update := next()
	if !update.ok || update.st.Snapshot.Mode != "battery" || update.st.Device.AppVersion != "v1.2.0" {
		t.Fatalf("update: %+v", update)
	}
	if lost := next(); lost.ok {
		t.Fatalf("the stream ended, but still ok: %+v", lost)
	}
	if again := next(); !again.ok {
		t.Fatal("didn't reconnect")
	}
}

func TestFollowShowsChanges(t *testing.T) {
	srv := fakeService(t)
	got := make(chan Status, 16)
	tr := Follow(Options{URL: srv.URL}, func(s Status) { got <- s })
	defer tr.Stop()
	next := func() Status {
		select {
		case s := <-got:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no status")
			return Status{}
		}
	}
	if s := next(); s.Tooltip != "PowerPilot" || len(s.Icon) == 0 {
		t.Fatalf("connecting: %+v", s)
	}
	if s := next(); s.Tooltip != "PowerPilot: On mains power" {
		t.Fatalf("reading: %q", s.Tooltip)
	}
	// The stream ends: the service is gone, then back with the same reading,
	// which isn't shown again.
	if s := next(); s.Tooltip != "PowerPilot: service not running" {
		t.Fatalf("lost: %q", s.Tooltip)
	}
	if s := next(); s.Tooltip != "PowerPilot: On mains power" {
		t.Fatalf("back: %q", s.Tooltip)
	}
	select {
	case s := <-got:
		if s.Tooltip == "PowerPilot: On mains power" {
			t.Fatalf("shown twice: %+v", s)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCommandError(t *testing.T) {
	srv := fakeService(t)
	if err := command(context.Background(), srv.URL, "alarm.mute"); err == nil || err.Error() != "UPS is not connected" {
		t.Fatalf("error %v", err)
	}
}

func TestRestartsOnceAfterAnUpdate(t *testing.T) {
	t.Setenv(restartedEnv, "")
	var restarts []string
	tr := newTray(Options{Version: "v1.1.0", Restart: func() error {
		restarts = append(restarts, os.Getenv(restartedEnv))
		if len(restarts) == 1 {
			return ErrNotNow // the window was open
		}
		return fmt.Errorf("exec failed")
	}})
	tr.checkVersion("v1.1.0") // the same
	tr.checkVersion("dev")
	tr.checkVersion("v1.2.0") // put off
	tr.checkVersion("v1.2.0") // fails
	tr.checkVersion("v1.2.0") // not again
	if len(restarts) != 2 || restarts[1] != "v1.2.0" {
		t.Fatalf("restarts %v", restarts)
	}

	// A process restarted for v1.2.0 that still isn't it doesn't loop.
	t.Setenv(restartedEnv, "v1.2.0")
	again := newTray(Options{Version: "v1.1.0", Restart: func() error { t.Fatal("restarted again"); return nil }})
	again.checkVersion("v1.2.0")
}
