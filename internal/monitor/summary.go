package monitor

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// SummaryEvery is how often a summary notification repeats during an outage.
const SummaryEvery = 5 * time.Minute

// Headline names the UPS's state in a few words, e.g. "On battery for 12 min".
func (s Snapshot) Headline() string {
	st := s.Status
	switch {
	case !s.Connected || st == nil:
		return "UPS not connected"
	case st.Fault:
		return "UPS fault"
	case st.LoadPercent >= 100:
		return "UPS overloaded"
	case st.BatteryLow || s.BatteryState == "low":
		return "Battery low"
	case st.TestRunning:
		return "Self-test running"
	case s.Mode == "standby":
		return "Outlets off"
	case st.OnBattery && s.OnBatterySec >= 60:
		return "On battery for " + Duration(time.Duration(s.OnBatterySec*float64(time.Second)))
	case st.OnBattery:
		return "On battery"
	}
	return "On mains power"
}

// BatteryLine is the charge and what it means now: the runtime left on
// battery, or whether it's charging on mains.
func (s Snapshot) BatteryLine() string {
	st := s.Status
	if !s.Connected || st == nil {
		return ""
	}
	if s.Charge == nil {
		return fmt.Sprintf("Battery %.1f V", st.BatteryVoltage)
	}
	line := fmt.Sprintf("Battery %.1f%%", *s.Charge)
	switch {
	case st.OnBattery && s.RuntimeMin != nil:
		return line + ", about " + Duration(time.Duration(*s.RuntimeMin*float64(time.Minute))) + " left"
	case st.OnBattery:
		return line
	case s.BatteryState == "full":
		return line + ", full"
	case s.BatteryState == "charging":
		return line + ", charging"
	}
	return line
}

// LoadLine is the load, e.g. "Load 31.0% (372 W)".
func (s Snapshot) LoadLine() string {
	if !s.Connected || s.Status == nil {
		return ""
	}
	return fmt.Sprintf("Load %.1f%% (%s W)", s.Status.LoadPercent, commas(int(math.Round(s.LoadW))))
}

// PowerLine is the input and output, e.g. "Input 229.0 V · Output 229.0 V · 50.0 Hz".
func (s Snapshot) PowerLine() string {
	st := s.Status
	if !s.Connected || st == nil {
		return ""
	}
	return fmt.Sprintf("Input %.1f V · Output %.1f V · %.1f Hz", st.InputVoltage, st.OutputVoltage, st.Frequency)
}

// Summary is the battery and load in one line, for notifications; empty
// while the UPS isn't connected.
func (s Snapshot) Summary() string {
	return join(" · ", s.BatteryLine(), s.LoadLine())
}

// WithSummary adds s's summary to a notification's message, on a line of its own.
func WithSummary(msg string, s Snapshot) string {
	return join("\n", msg, s.Summary())
}

// Duration says d in whole minutes, or hours and minutes: "12 min", "1 h 5 min".
func Duration(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m < 1:
		return "under a minute"
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m%60 == 0:
		return fmt.Sprintf("%s h", commas(m/60))
	}
	return fmt.Sprintf("%s h %d min", commas(m/60), m%60)
}

// commas groups n's digits in thousands: 1200 is "1,200".
func commas(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

func join(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
