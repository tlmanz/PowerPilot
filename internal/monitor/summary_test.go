package monitor

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

func ptr(v float64) *float64 { return &v }

func TestSnapshotText(t *testing.T) {
	mains := ups.Status{InputVoltage: 228.7, OutputVoltage: 228.7, LoadPercent: 31, Frequency: 50.1, BatteryVoltage: 27.1}
	battery := mains
	battery.InputVoltage, battery.OnBattery = 0, true
	fault := mains
	fault.Fault = true
	heavy := mains
	heavy.LoadPercent = 104

	for _, c := range []struct {
		name                          string
		snap                          Snapshot
		headline, battery, load, more string
	}{
		{"disconnected", Snapshot{Mode: "offline"}, "UPS not connected", "", "", ""},
		{"full on mains", Snapshot{Connected: true, Status: &mains, Mode: "line", Charge: ptr(100), BatteryState: "full", LoadW: 372, RuntimeMin: ptr(40)},
			"On mains power", "Battery 100.0%, full", "Load 31.0% (372 W)", "Input 228.7 V · Output 228.7 V · 50.1 Hz"},
		{"charging", Snapshot{Connected: true, Status: &mains, Mode: "line", Charge: ptr(92.5), BatteryState: "charging", LoadW: 372},
			"On mains power", "Battery 92.5%, charging", "Load 31.0% (372 W)", ""},
		{"outage", Snapshot{Connected: true, Status: &battery, Mode: "battery", Charge: ptr(87), BatteryState: "discharging", LoadW: 372, RuntimeMin: ptr(83.6), OnBatterySec: 754},
			"On battery for 12 min", "Battery 87.0%, about 1 h 23 min left", "Load 31.0% (372 W)", "Input 0.0 V · Output 228.7 V · 50.1 Hz"},
		{"outage just began", Snapshot{Connected: true, Status: &battery, Mode: "battery", Charge: ptr(87), OnBatterySec: 4},
			"On battery", "Battery 87.0%", "Load 31.0% (0 W)", ""},
		{"low", Snapshot{Connected: true, Status: &battery, Mode: "battery", Charge: ptr(14), BatteryState: "low", RuntimeMin: ptr(0.5)},
			"Battery low", "Battery 14.0%, about under a minute left", "", ""},
		{"fault, charge unknown", Snapshot{Connected: true, Status: &fault, Mode: "fault"}, "UPS fault", "Battery 27.1 V", "", ""},
		{"overload", Snapshot{Connected: true, Status: &heavy, Mode: "line", LoadW: 1248}, "UPS overloaded", "", "Load 104.0% (1,248 W)", ""},
	} {
		s := c.snap
		if got := s.Headline(); got != c.headline {
			t.Errorf("%s: headline %q, want %q", c.name, got, c.headline)
		}
		if got := s.BatteryLine(); c.battery != "" && got != c.battery {
			t.Errorf("%s: battery %q, want %q", c.name, got, c.battery)
		}
		if got := s.LoadLine(); c.load != "" && got != c.load {
			t.Errorf("%s: load %q, want %q", c.name, got, c.load)
		}
		if got := s.PowerLine(); c.more != "" && got != c.more {
			t.Errorf("%s: power %q, want %q", c.name, got, c.more)
		}
	}
	if got := (Snapshot{}).Summary(); got != "" {
		t.Errorf("disconnected summary %q", got)
	}
	if got := WithSummary("Mains power was lost.", Snapshot{}); got != "Mains power was lost." {
		t.Errorf("message without a reading %q", got)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:                "under a minute",
		12*time.Minute + 59*time.Second: "12 min",
		time.Hour:                       "1 h",
		83 * time.Minute:                "1 h 23 min",
		1500 * time.Hour:                "1,500 h",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1200: "1,200", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}

// newNotingMonitor records each notification as "title|message".
func newNotingMonitor(t *testing.T) (*Monitor, chan string) {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	notes := make(chan string, 32)
	actions := Actions{Notify: func(title, msg string) error { notes <- title + "|" + msg; return nil }}
	m := New(time.Second, Device{RatedVA: 2000, RatedW: 1200}, store, actions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ratings = &ups.Ratings{Voltage: 230, Current: 8, BatteryVoltage: 24, Frequency: 50}
	return m, notes
}

func nextNote(t *testing.T, notes <-chan string, title string) string {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case n := <-notes:
			if strings.HasPrefix(n, title+"|") {
				return strings.TrimPrefix(n, title+"|")
			}
		case <-timeout:
			t.Fatalf("no %q notification", title)
		}
	}
}

func TestNotificationsSummarizeTheReading(t *testing.T) {
	m, notes := newNotingMonitor(t)
	m.update(onLine)
	m.update(onBattery)
	// The summary is of the reading that went on battery, not the one before.
	msg := nextNote(t, notes, "Running on battery")
	first, summary, _ := strings.Cut(msg, "\n")
	if first != "Mains power was lost. The UPS is powering your equipment." ||
		!strings.HasPrefix(summary, "Battery 76.0%, about ") || !strings.HasSuffix(summary, " left · Load 10.0% (120 W)") {
		t.Fatalf("message %q", msg)
	}
}

func TestSummaryRepeatsDuringOutage(t *testing.T) {
	m, notes := newNotingMonitor(t)
	m.update(onLine)
	m.update(onBattery)
	nextNote(t, notes, "Running on battery")

	m.update(onBattery) // too soon for another
	m.batterySince = m.batterySince.Add(-SummaryEvery)
	m.summaryAt = m.summaryAt.Add(-SummaryEvery)
	m.update(onBattery)
	if msg := nextNote(t, notes, "On battery for 5 min"); !strings.HasPrefix(msg, "Battery 76.0%, about ") {
		t.Fatalf("summary %q", msg)
	}
	select {
	case n := <-notes:
		t.Fatalf("unexpected notification %q", n)
	case <-time.After(100 * time.Millisecond):
	}
}
