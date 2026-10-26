package monitor

import (
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

type fakeActions struct {
	notes     chan string
	shutdowns chan struct{}
}

func newTestMonitor(t *testing.T, cfg settings.Settings) (*Monitor, fakeActions) {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(cfg); err != nil {
		t.Fatal(err)
	}
	fa := fakeActions{notes: make(chan string, 32), shutdowns: make(chan struct{}, 4)}
	actions := Actions{
		Notify:   func(title, msg string) error { fa.notes <- title; return nil },
		ShutDown: func() error { fa.shutdowns <- struct{}{}; return nil },
	}
	m := New(time.Second, Device{RatedVA: 2000, RatedW: 1200}, store, actions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ratings = &ups.Ratings{Voltage: 230, Current: 8, BatteryVoltage: 24, Frequency: 50}
	return m, fa
}

var (
	onLine    = ups.Status{InputVoltage: 230, OutputVoltage: 230, BatteryVoltage: 27.0, LineInteractive: true, BeeperOn: true}
	onBattery = ups.Status{InputVoltage: 0, OutputVoltage: 230, LoadPercent: 10, BatteryVoltage: 25.0, OnBattery: true, LineInteractive: true, BeeperOn: true}
)

func messages(m *Monitor) string {
	var b strings.Builder
	for _, e := range m.State().Events {
		b.WriteString(e.Message + "\n")
	}
	return b.String()
}

func waitFor(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q notification", want)
		}
	}
}

func TestPowerLossAndRestore(t *testing.T) {
	m, fa := newTestMonitor(t, settings.Default())
	m.update(onLine)
	m.update(onBattery)
	waitFor(t, fa.notes, "Running on battery")
	if s := m.State().Snapshot; s.Mode != "battery" || s.BatteryState != "discharging" {
		t.Fatalf("snapshot: mode %q battery %q", s.Mode, s.BatteryState)
	}
	restored := onLine
	restored.BatteryVoltage = 26.0 // partly drained, so it reports charging
	m.update(restored)
	waitFor(t, fa.notes, "Mains power restored")
	for _, want := range []string{"Connected to the UPS", "Utility power lost", "Running on battery", "Utility power restored", "Battery charging"} {
		if !strings.Contains(messages(m), want) {
			t.Errorf("missing event %q in:\n%s", want, messages(m))
		}
	}
	select {
	case <-fa.shutdowns:
		t.Fatal("Mac shutdown must not run while it is disabled")
	default:
	}
}

func TestMacShutdownOnLowBattery(t *testing.T) {
	cfg := settings.Default()
	cfg.ShutdownMac = true
	cfg.ShutdownAfterMinutes = 60
	m, fa := newTestMonitor(t, cfg)
	m.update(onLine)
	low := onBattery
	low.BatteryLow = true
	m.update(low)
	select {
	case <-fa.shutdowns:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a Mac shutdown on low battery")
	}
	// Only once per outage.
	m.update(low)
	select {
	case <-fa.shutdowns:
		t.Fatal("shutdown requested twice")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLowBatteryAlertUsesThreshold(t *testing.T) {
	cfg := settings.Default()
	cfg.LowBatteryPercent = 50
	m, fa := newTestMonitor(t, cfg)
	m.update(onLine)
	weak := onBattery
	weak.BatteryVoltage = 23.0 // 11.5 V per block at 10% load: 43% by ViewPower's formula
	m.update(weak)
	waitFor(t, fa.notes, "UPS battery low")
	if got := m.State().Snapshot.BatteryState; got != "low" {
		t.Fatalf("battery state %q", got)
	}
}

func TestMutedAlarmUnmutesWhenConditionClears(t *testing.T) {
	m, _ := newTestMonitor(t, settings.Default())
	m.update(onLine)
	m.alarmMuted = true
	if m.update(onBattery) {
		t.Fatal("must stay muted while the alarm condition lasts")
	}
	if s := m.State().Snapshot; !s.AlarmActive || !s.AlarmMuted {
		t.Fatalf("snapshot alarm active=%v muted=%v", s.AlarmActive, s.AlarmMuted)
	}
	if !m.update(onLine) {
		t.Fatal("expected an unmute once mains returned")
	}
}

func TestHistoryRecordsCharge(t *testing.T) {
	m, _ := newTestMonitor(t, settings.Default())
	m.update(onLine)
	h := m.State().History
	if len(h) != 1 || h[0].Charge == nil || *h[0].Charge != 100 {
		t.Fatalf("history: %+v", h)
	}
}

func TestSelfTestResult(t *testing.T) {
	m, _ := newTestMonitor(t, settings.Default())
	m.update(onLine)
	inTest := onLine
	inTest.TestRunning = true
	m.update(inTest)
	m.update(onLine)
	lt := m.State().LastTest
	if lt == nil || lt.Result != "passed" {
		t.Fatalf("last test: %+v", lt)
	}

	m.update(inTest)
	failing := inTest
	failing.BatteryLow = true
	m.update(failing)
	m.update(onLine)
	if lt := m.State().LastTest; lt.Result != "failed" {
		t.Fatalf("last test: %+v", lt)
	}
}

func TestSelfTestMeasured(t *testing.T) {
	m, _ := newTestMonitor(t, settings.Default())
	before := onLine
	before.BatteryVoltage, before.LoadPercent = 27.1, 8
	m.update(before)
	inTest := onLine
	inTest.TestRunning, inTest.BatteryVoltage, inTest.LoadPercent = true, 26.04, 9
	m.update(inTest)
	m.testStart = m.testStart.Add(-10 * time.Second) // as if the test ran 10 s
	inTest.BatteryVoltage, inTest.LoadPercent = 25.93, 7
	m.update(inTest)
	m.update(before)

	evs := m.State().Events
	var tests []Event
	for _, e := range evs {
		if e.Test != nil {
			tests = append(tests, e)
		}
	}
	want := SelfTest{Result: "passed", Seconds: 10, Load: 9, VoltsBefore: 27.1, VoltsLow: 25.9}
	if len(tests) != 1 || *tests[0].Test != want || tests[0] != evs[len(evs)-1] {
		t.Fatalf("measured %+v in events %+v", tests, evs)
	}
	if e := tests[0]; e.Kind != KindSelfTest || e.Level != "good" || e.Message != "UPS self-test completed (passed)" {
		t.Errorf("ending event %+v", e)
	}

	// A failed test: the start comes from the poll before, still on mains.
	m.update(inTest)
	failing := inTest
	failing.BatteryLow, failing.BatteryVoltage = true, 23.0
	m.update(failing)
	m.update(before)
	last := m.State().Events[len(m.State().Events)-1]
	if last.Level != "alert" || last.Message != "UPS self-test failed" || last.Test == nil ||
		last.Test.Result != "failed" || last.Test.VoltsBefore != 27.1 || last.Test.VoltsLow != 23 || last.Test.Load != 7 {
		t.Fatalf("failed test event %+v (%+v)", last, last.Test)
	}
}

type fakeRecorder struct {
	events []Event
	modes  []string
}

func (r *fakeRecorder) Event(e Event)                { r.events = append(r.events, e) }
func (r *fakeRecorder) Sample(_ Sample, mode string) { r.modes = append(r.modes, mode) }

func TestRecorderGetsEventsAndSamples(t *testing.T) {
	m, _ := newTestMonitor(t, settings.Default())
	rec := &fakeRecorder{}
	m.actions.Log = rec
	m.Restore([]Event{{Kind: KindInfo, Level: "info", Message: "from yesterday"}})
	m.update(onLine)
	m.update(onBattery)
	if got := strings.Join(rec.modes, ","); got != "line,battery" {
		t.Errorf("sample modes = %s", got)
	}
	var logged []string
	for _, e := range rec.events {
		logged = append(logged, e.Message)
	}
	if !strings.Contains(strings.Join(logged, "|"), "Utility power lost") || slices.Contains(logged, "from yesterday") {
		t.Errorf("logged events: %q", logged)
	}
	if first := m.State().Events[0].Message; first != "from yesterday" {
		t.Errorf("restored events should come first, got %q", first)
	}
}
