package ups

import (
	"errors"
	"testing"
	"time"
)

func TestParseStatusRealReply(t *testing.T) {
	s, err := ParseStatus("(239.2 201.1 239.3 000 50.1 26.7 --.- 00001001")
	if err != nil {
		t.Fatal(err)
	}
	if s.InputVoltage != 239.2 || s.InputFaultVoltage != 201.1 || s.OutputVoltage != 239.3 ||
		s.LoadPercent != 0 || s.Frequency != 50.1 || s.BatteryVoltage != 26.7 {
		t.Fatalf("numbers: %+v", s)
	}
	if s.Temperature != nil {
		t.Fatalf("temperature should be unknown, got %v", *s.Temperature)
	}
	if s.OnBattery || s.BatteryLow || s.AVRActive || s.Fault || s.TestRunning || s.ShutdownActive {
		t.Fatalf("unexpected alarm bits: %+v", s)
	}
	if !s.LineInteractive || !s.BeeperOn {
		t.Fatalf("expected line-interactive with beeper on: %+v", s)
	}
	if s.Mode() != "line" {
		t.Fatalf("mode %q", s.Mode())
	}
}

func TestParseStatusRejectsEchoAndJunk(t *testing.T) {
	for _, r := range []string{"QS", "Q1", "", "(239.2 201.1 239.3 000 50.1 26.7 --.-", "(239.2 x 239.3 000 50.1 26.7 --.- 00001001", "(239.2 201.1 239.3 000 50.1 26.7 --.- 0000100x"} {
		if _, err := ParseStatus(r); err == nil {
			t.Errorf("ParseStatus(%q) should fail", r)
		}
	}
}

func TestParseRatings(t *testing.T) {
	r, err := ParseRatings("#230.0 008 24.00 50.0")
	if err != nil {
		t.Fatal(err)
	}
	if r != (Ratings{Voltage: 230, Current: 8, BatteryVoltage: 24, Frequency: 50}) {
		t.Fatalf("%+v", r)
	}
	if _, err := ParseRatings("F"); err == nil {
		t.Fatal("echo should fail")
	}
}

func TestMode(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{Status{OutputVoltage: 230}, "line"},
		{Status{OutputVoltage: 230, TestRunning: true}, "test"},
		{Status{OutputVoltage: 230, OnBattery: true}, "battery"},
		{Status{OutputVoltage: 0}, "standby"},
		{Status{OutputVoltage: 230, Fault: true}, "fault"},
		{Status{OutputVoltage: 0, Fault: true}, "fault"},
	}
	for _, c := range cases {
		if got := c.s.Mode(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.s, got, c.want)
		}
	}
}

func TestChargeOnMainsFloatRamp(t *testing.T) {
	var e ChargeEstimator
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	st := Status{OutputVoltage: 230, BatteryVoltage: 26.7} // 13.35 V per block
	steps := []struct {
		at   time.Duration
		want float64
	}{{0, 90}, {11 * time.Minute, 90}, {12 * time.Minute, 91}, {23 * time.Minute, 91}, {24 * time.Minute, 92}}
	for _, s := range steps {
		got, ok := e.Estimate(st, t0.Add(s.at))
		if !ok || got != s.want {
			t.Fatalf("at %v: got %v want %v", s.at, got, s.want)
		}
	}
	// Going on battery resets the ramp back to 90.
	e.Estimate(Status{OutputVoltage: 230, OnBattery: true, BatteryVoltage: 26.7}, t0.Add(25*time.Minute))
	if got, _ := e.Estimate(st, t0.Add(26*time.Minute)); got != 90 {
		t.Fatalf("after battery: got %v want 90", got)
	}
}

func TestChargeFormulas(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		s    Status
		want float64
	}{
		{"line full", Status{OutputVoltage: 230, BatteryVoltage: 27.2}, 100},
		{"line partial", Status{OutputVoltage: 230, BatteryVoltage: 25.0}, 47},
		{"line empty", Status{OutputVoltage: 230, BatteryVoltage: 23.0}, 0},
		{"battery light load", Status{OutputVoltage: 230, OnBattery: true, LoadPercent: 10, BatteryVoltage: 23.5}, 51},
		{"battery heavy load", Status{OutputVoltage: 230, OnBattery: true, LoadPercent: 50, BatteryVoltage: 23.0}, 52},
		{"battery above full", Status{OutputVoltage: 230, OnBattery: true, LoadPercent: 10, BatteryVoltage: 26.6}, 100},
		{"battery flat", Status{OutputVoltage: 230, OnBattery: true, LoadPercent: 10, BatteryVoltage: 20.0}, 0},
	}
	for _, c := range cases {
		var e ChargeEstimator
		got, ok := e.Estimate(c.s, now)
		if !ok || got != c.want {
			t.Errorf("%s: got %v (ok=%v) want %v", c.name, got, ok, c.want)
		}
	}
	var e ChargeEstimator
	if _, ok := e.Estimate(Status{OutputVoltage: 230, Fault: true, BatteryVoltage: 26.7}, now); ok {
		t.Error("fault mode should give no estimate")
	}
}

func TestEstimateRuntime(t *testing.T) {
	full, ok := EstimateRuntime(100, 1200, 24, 9)
	if !ok || full < 3 || full > 6 {
		t.Fatalf("full load: %v min", full)
	}
	half, _ := EstimateRuntime(100, 600, 24, 9)
	if half < 8 || half > 12 {
		t.Fatalf("half load: %v min", half)
	}
	halfCharge, _ := EstimateRuntime(50, 600, 24, 9)
	if halfCharge >= half {
		t.Fatalf("less charge should mean less runtime: %v vs %v", halfCharge, half)
	}
	if _, ok := EstimateRuntime(100, 0, 24, 9); ok {
		t.Fatal("zero load has no estimate")
	}
}

func TestBuild(t *testing.T) {
	ok := []struct {
		id   string
		p    Params
		want string
	}{
		{"beeper.on", Params{}, "Q"},
		{"beeper.off", Params{}, "Q"},
		{"alarm.mute", Params{}, "Q"},
		{"alarm.unmute", Params{}, "Q"},
		{"test.quick", Params{}, "T"},
		{"test.cancel", Params{}, "CT"},
		{"shutdown", Params{Minutes: 0.2}, "S.2R0000"},
		{"shutdown", Params{Minutes: 0.5, RestoreAfter: 1}, "S.5R0001"},
		{"shutdown", Params{Minutes: 1, RestoreAfter: 1}, "S01R0001"},
		{"shutdown", Params{Minutes: 10, RestoreAfter: 9999}, "S10R9999"},
		{"shutdown.cancel", Params{}, "C"},
	}
	for _, c := range ok {
		got, err := Build(c.id, c.p)
		if err != nil || got != c.want {
			t.Errorf("Build(%s, %+v) = %q, %v; want %q", c.id, c.p, got, err, c.want)
		}
	}
	bad := []struct {
		id string
		p  Params
	}{
		{"shutdown", Params{Minutes: 0.1}},
		{"shutdown", Params{Minutes: 0.25}},
		{"shutdown", Params{Minutes: 1.5}},
		{"shutdown", Params{Minutes: 11}},
		{"shutdown", Params{Minutes: 1, RestoreAfter: -1}},
		{"shutdown", Params{Minutes: 1, RestoreAfter: 10000}},
		{"test.deep", Params{}},
		{"S00R0000", Params{}},
	}
	for _, c := range bad {
		if _, err := Build(c.id, c.p); !errors.Is(err, ErrInvalidCommand) {
			t.Errorf("Build(%s, %+v) error = %v; want ErrInvalidCommand", c.id, c.p, err)
		}
	}
}
