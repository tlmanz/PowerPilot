// Package ups implements the Megatec-derived "PMV" protocol that Voltronic's
// ViewPower uses for this UPS (it answers "M" with "V"). Behaviour follows
// ViewPower's PMVProcessor unless a comment says otherwise.
package ups

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	VendorID  = 0x0665
	ProductID = 0x5161
)

var ErrInvalidCommand = errors.New("invalid command")

// Ratings is the F reply, e.g. "#230.0 008 24.00 50.0".
type Ratings struct {
	Voltage        float64 `json:"voltage"`
	Current        float64 `json:"current"`
	BatteryVoltage float64 `json:"battery_voltage"`
	Frequency      float64 `json:"frequency"`
}

// Status is the QS reply, e.g. "(239.2 239.2 239.2 000 50.1 26.7 --.- 00001001".
type Status struct {
	InputVoltage      float64  `json:"input_voltage"`
	InputFaultVoltage float64  `json:"input_fault_voltage"`
	OutputVoltage     float64  `json:"output_voltage"`
	LoadPercent       float64  `json:"load_percent"`
	Frequency         float64  `json:"frequency"`
	BatteryVoltage    float64  `json:"battery_voltage"`
	Temperature       *float64 `json:"temperature"`

	OnBattery       bool `json:"on_battery"`       // b7 utility fail
	BatteryLow      bool `json:"battery_low"`      // b6
	AVRActive       bool `json:"avr_active"`       // b5 boost/buck (ViewPower ignores it)
	Fault           bool `json:"fault"`            // b4
	LineInteractive bool `json:"line_interactive"` // b3
	TestRunning     bool `json:"test_running"`     // b2
	ShutdownActive  bool `json:"shutdown_active"`  // b1 (ViewPower ignores it)
	BeeperOn        bool `json:"beeper_on"`        // b0
}

func ParseStatus(reply string) (Status, error) {
	if !strings.HasPrefix(reply, "(") {
		return Status{}, fmt.Errorf("unexpected status reply %q", reply)
	}
	f := strings.Fields(reply[1:])
	if len(f) != 8 || len(f[7]) != 8 {
		return Status{}, fmt.Errorf("unexpected status reply %q", reply)
	}
	var s Status
	for i, p := range []*float64{&s.InputVoltage, &s.InputFaultVoltage, &s.OutputVoltage, &s.LoadPercent, &s.Frequency, &s.BatteryVoltage} {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return Status{}, fmt.Errorf("status field %d in %q: %w", i+1, reply, err)
		}
		*p = v
	}
	// This model reports "--.-" for temperature.
	if t, err := strconv.ParseFloat(f[6], 64); err == nil {
		s.Temperature = &t
	}
	bits := f[7]
	if strings.Trim(bits, "01") != "" {
		return Status{}, fmt.Errorf("bad status bits in %q", reply)
	}
	b := func(i int) bool { return bits[i] == '1' }
	s.OnBattery, s.BatteryLow, s.AVRActive, s.Fault = b(0), b(1), b(2), b(3)
	s.LineInteractive, s.TestRunning, s.ShutdownActive, s.BeeperOn = b(4), b(5), b(6), b(7)
	return s, nil
}

func ParseRatings(reply string) (Ratings, error) {
	if !strings.HasPrefix(reply, "#") {
		return Ratings{}, fmt.Errorf("unexpected ratings reply %q", reply)
	}
	f := strings.Fields(reply[1:])
	if len(f) != 4 {
		return Ratings{}, fmt.Errorf("unexpected ratings reply %q", reply)
	}
	var r Ratings
	for i, p := range []*float64{&r.Voltage, &r.Current, &r.BatteryVoltage, &r.Frequency} {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return Ratings{}, fmt.Errorf("ratings field %d in %q: %w", i+1, reply, err)
		}
		*p = v
	}
	return r, nil
}

// Mode is ViewPower's work mode: standby (outlets off), line, test, battery or fault.
func (s Status) Mode() string {
	m := "battery"
	switch {
	case s.OutputVoltage < 20 && !s.Fault:
		m = "standby"
	case !s.OnBattery && !s.TestRunning:
		m = "line"
	case !s.OnBattery:
		m = "test"
	}
	if s.Fault {
		m = "fault"
	}
	return m
}

// ChargeEstimator reproduces ViewPower's battery capacity estimate. It keeps
// state because a battery floating at 13.3–13.5 V per block on mains starts
// at 90% and is credited 1% per 12 minutes, up to 100%.
type ChargeEstimator struct {
	ramp     float64
	rampTick time.Time
}

// Estimate returns the charge in percent; ok is false in fault mode, where
// ViewPower reports 0% (we show "unknown" instead).
func (e *ChargeEstimator) Estimate(s Status, now time.Time) (pct float64, ok bool) {
	v := blockVoltage(s.BatteryVoltage)
	switch s.Mode() {
	case "line", "standby":
		switch {
		case v < 11.6:
			return 0, true
		case v >= 13.5:
			return 100, true
		case v >= 13.3:
			if e.rampTick.IsZero() {
				e.ramp, e.rampTick = 90, now
			} else if now.Sub(e.rampTick) >= 12*time.Minute {
				e.ramp, e.rampTick = math.Min(100, e.ramp+1), now
			}
			return e.ramp, true
		}
		return math.Floor(90 * (v - 11.6) / 1.7), true
	case "battery", "test":
		e.ramp, e.rampTick = 90, time.Time{}
		full, span := 13.2, 3.0
		if s.LoadPercent >= 20 {
			full, span = 12.7, 2.5
		}
		switch {
		case v > full:
			return 100, true
		case v > 10.2:
			return math.Floor(100 * (v - 10.2) / span), true
		}
		return 0, true
	}
	return 0, false
}

// blockVoltage converts pack voltage to volts per 12 V block, as ViewPower does.
func blockVoltage(v float64) float64 {
	switch {
	case v < 15:
		return v
	case v <= 30:
		return v / 2
	case v <= 45:
		return v / 3
	case v <= 60:
		return v / 4
	}
	return v
}

// Runtime model constants. ViewPower computes no runtime for this protocol, so
// this is our own Peukert estimate for sealed lead-acid batteries.
const (
	peukertExponent    = 1.15
	ratedHours         = 20.0 // battery Ah ratings are 20-hour rates
	inverterEfficiency = 0.85
	maxRuntimeMinutes  = 8 * 60
)

// EstimateRuntime returns minutes of backup at loadW for a battery pack of
// packVoltage volts and batteryAh amp-hours at chargePct.
func EstimateRuntime(chargePct, loadW, packVoltage, batteryAh float64) (float64, bool) {
	if loadW <= 0 || packVoltage <= 0 || batteryAh <= 0 {
		return 0, false
	}
	amps := loadW / (packVoltage * inverterEfficiency)
	hours := ratedHours * math.Pow(batteryAh/(amps*ratedHours), peukertExponent)
	return math.Min(hours*60*chargePct/100, maxRuntimeMinutes), true
}

// Params carries the arguments of a control command.
type Params struct {
	Minutes      float64 `json:"minutes,omitempty"`       // shutdown delay
	RestoreAfter int     `json:"restore_after,omitempty"` // minutes until outlets restart; 0 keeps them off
}

// Build returns the protocol string for a control command.
func Build(id string, p Params) (string, error) {
	switch id {
	case "beeper.on", "beeper.off", "alarm.mute", "alarm.unmute":
		return "Q", nil // a toggle; the caller checks the current state first
	case "test.quick":
		return "T", nil
	case "test.cancel":
		return "CT", nil
	case "shutdown":
		delay, err := shutdownDelay(p.Minutes)
		if err != nil {
			return "", err
		}
		if p.RestoreAfter < 0 || p.RestoreAfter > 9999 {
			return "", fmt.Errorf("%w: restart delay must be 0 to 9,999 minutes", ErrInvalidCommand)
		}
		return fmt.Sprintf("S%sR%04d", delay, p.RestoreAfter), nil
	case "shutdown.cancel":
		return "C", nil
	}
	return "", fmt.Errorf("%w: unknown command %q", ErrInvalidCommand, id)
}

// shutdownDelay formats minutes as ".2"–".9" or "01"–"10".
func shutdownDelay(min float64) (string, error) {
	tenths := math.Round(min * 10)
	switch {
	case min >= 0.2 && min < 1 && math.Abs(min*10-tenths) < 1e-9:
		return fmt.Sprintf(".%d", int(tenths)), nil
	case min >= 1 && min <= 10 && min == math.Trunc(min):
		return fmt.Sprintf("%02d", int(min)), nil
	}
	return "", fmt.Errorf("%w: shutdown delay must be 0.2–0.9 or 1–10 minutes", ErrInvalidCommand)
}

// BeeperTarget reports the beeper state a beeper command asks for. Muting is
// the beeper turned off until the current alarm condition clears.
func BeeperTarget(id string) (on, ok bool) {
	switch id {
	case "beeper.on", "alarm.unmute":
		return true, true
	case "beeper.off", "alarm.mute":
		return false, true
	}
	return false, false
}

func IsTest(id string) bool { return id == "test.quick" }

// Describe phrases a sent command for the event log.
func Describe(id string, p Params) string {
	switch id {
	case "beeper.on":
		return "UPS alarm turned on"
	case "beeper.off":
		return "UPS alarm turned off"
	case "alarm.mute":
		return "Alarm muted until the current condition clears"
	case "alarm.unmute":
		return "Alarm unmuted"
	case "test.quick":
		return "Started a 10-second battery self-test"
	case "test.cancel":
		return "Cancelled the battery self-test"
	case "shutdown":
		when := fmt.Sprintf("%g minutes", p.Minutes)
		if p.Minutes < 1 {
			when = fmt.Sprintf("%d seconds", int(math.Round(p.Minutes*60)))
		} else if p.Minutes == 1 {
			when = "1 minute"
		}
		if p.RestoreAfter == 0 {
			return "Outlets will turn off in " + when + " and stay off"
		}
		return fmt.Sprintf("Outlets will turn off in %s and restart after %d min", when, p.RestoreAfter)
	case "shutdown.cancel":
		return "Cancelled outlet shutdown / turned outlets on"
	}
	return id
}
