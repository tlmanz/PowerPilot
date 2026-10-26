// Package monitor polls the UPS, keeps recent history and events, applies the
// user's alert/shutdown settings, and fans updates out to dashboard clients.
package monitor

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/tlmanz/PowerPilot/internal/hid"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

const (
	historyWindow = time.Hour
	maxEvents     = 200
	// Consecutive failed polls before the UPS is reported as disconnected.
	maxFailures = 3
	// Beeper changes this soon after our own command are not reported again.
	beeperEcho = 10 * time.Second
)

var ErrDisconnected = errors.New("UPS is not connected")

type Device struct {
	Brand      string `json:"brand"`
	Model      string `json:"model"`
	Protocol   string `json:"protocol"`
	Connection string `json:"connection"`
	RatedVA    int    `json:"rated_va"`
	RatedW     int    `json:"rated_w"`
	AppVersion string `json:"app_version"`
}

type Snapshot struct {
	Connected    bool         `json:"connected"`
	Error        string       `json:"error,omitempty"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Status       *ups.Status  `json:"status,omitempty"`
	Ratings      *ups.Ratings `json:"ratings,omitempty"`
	Charge       *float64     `json:"charge,omitempty"`
	RuntimeMin   *float64     `json:"runtime_min,omitempty"`
	RuntimeLoadW float64      `json:"runtime_load_w,omitempty"`
	RuntimeCurve []Point      `json:"runtime_curve,omitempty"`
	LoadW        float64      `json:"load_w"`
	LoadVA       float64      `json:"load_va"`
	Mode         string       `json:"mode"`
	BatteryState string       `json:"battery_state"`
	OnBatterySec float64      `json:"on_battery_sec,omitempty"`
	AlarmActive  bool         `json:"alarm_active"`
	AlarmMuted   bool         `json:"alarm_muted"`
}

// Point is one (load %, runtime minutes) pair of the runtime curve.
type Point struct {
	Load    float64 `json:"load"`
	Minutes float64 `json:"min"`
}

type Sample struct {
	T      int64    `json:"t"`
	In     float64  `json:"in"`
	Out    float64  `json:"out"`
	Load   float64  `json:"load"`
	Batt   float64  `json:"batt"`
	Freq   float64  `json:"freq"`
	Charge *float64 `json:"charge,omitempty"`
}

// Event kinds the dashboard gives their own icons.
const (
	KindUtilityLost     = "utility_lost"
	KindUtilityRestored = "utility_restored"
	KindOnBattery       = "on_battery"
	KindCharging        = "battery_charging"
	KindLowBattery      = "low_battery"
	KindOverload        = "overload"
	KindSelfTest        = "self_test"
	KindFault           = "fault"
	KindAlarm           = "alarm"
	KindAlarmMuted      = "alarm_muted"
	KindAlarmUnmuted    = "alarm_unmuted"
	KindConnection      = "connection"
	KindShutdown        = "shutdown"
	KindInfo            = "info"
)

type Event struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Level   string    `json:"level"` // info, good, warn, alert
	Message string    `json:"message"`
	Test    *SelfTest `json:"test,omitempty"` // only on the event that ends a self-test
}

// SelfTest is what a self-test measured. powerpilot-server works out the
// battery's health from it (PowerPilotHardware docs/cloud.md, "Battery health").
type SelfTest struct {
	Result      string  `json:"result"` // passed, failed
	Seconds     int     `json:"seconds"`
	Load        float64 `json:"load"`         // the highest during it, %
	VoltsBefore float64 `json:"volts_before"` // on the last poll before it, on mains
	VoltsLow    float64 `json:"volts_low"`    // the lowest during it
}

type TestResult struct {
	Kind    string     `json:"kind"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	Result  string     `json:"result"` // running, passed, failed, cancelled
}

type State struct {
	Device     Device            `json:"device"`
	Snapshot   Snapshot          `json:"snapshot"`
	History    []Sample          `json:"history"`
	Events     []Event           `json:"events"`
	Settings   settings.Settings `json:"settings"`
	LastTest   *TestResult       `json:"last_test"`
	IntervalMS int64             `json:"interval_ms"`
}

type Update struct {
	Snapshot Snapshot           `json:"snapshot"`
	Sample   *Sample            `json:"sample,omitempty"`
	Events   []Event            `json:"events,omitempty"`
	Settings *settings.Settings `json:"settings,omitempty"`
	LastTest *TestResult        `json:"last_test,omitempty"`
}

type Actions struct {
	Notify   func(title, msg string) error
	ShutDown func() error
	// Log, when set, keeps events and readings beyond what's held in memory.
	Log Recorder
}

// Recorder keeps events and readings on disk (package logbook). Its methods
// are called with the monitor locked, so they must not block.
type Recorder interface {
	Event(Event)
	Sample(s Sample, mode string)
}

type Monitor struct {
	interval time.Duration
	device   Device
	store    *settings.Store
	actions  Actions
	log      *slog.Logger
	poke     chan struct{}

	devMu sync.Mutex
	dev   *hid.Device

	mu           sync.Mutex
	snap         Snapshot
	ratings      *ups.Ratings
	charge       ups.ChargeEstimator
	history      []Sample
	events       []Event
	pending      []Event
	notes        []note // notifications, sent with the next update
	pendSettings *settings.Settings
	lastTest     *TestResult
	testSawFault bool

	// The running self-test's measurements, as the ESP32 takes them.
	testStart  time.Time
	testBefore float64 // battery V on the poll before it
	testLow    float64
	testMax    float64   // the highest load, %
	nextTest   *SelfTest // goes on the next event: the one that ends the test

	failures     int
	everOnline   bool
	waitNoted    bool // "Waiting for the UPS" logged
	batterySince time.Time
	summaryAt    time.Time // last outage summary notification
	lowNotified  bool
	macShutdown  bool
	alarmMuted   bool
	beeperCmdAt  time.Time
	subs         map[chan Update]struct{}
}

func New(interval time.Duration, device Device, store *settings.Store, actions Actions, log *slog.Logger) *Monitor {
	return &Monitor{
		interval: interval,
		device:   device,
		store:    store,
		actions:  actions,
		log:      log,
		poke:     make(chan struct{}, 1),
		snap:     Snapshot{Mode: "offline", BatteryState: "unknown"},
		subs:     map[chan Update]struct{}{},
	}
}

// Run polls until done is closed, then releases the USB device.
func (m *Monitor) Run(done <-chan struct{}) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	defer m.dropDevice()
	for {
		m.poll()
		select {
		case <-done:
			return
		case <-t.C:
		case <-m.poke:
		}
	}
}

// Poke requests an immediate poll.
func (m *Monitor) Poke() {
	select {
	case m.poke <- struct{}{}:
	default:
	}
}

func (m *Monitor) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{
		Device:     m.device,
		Snapshot:   m.snap,
		History:    append([]Sample(nil), m.history...),
		Events:     append([]Event(nil), m.events...),
		Settings:   m.store.Get(),
		LastTest:   m.lastTest,
		IntervalMS: m.interval.Milliseconds(),
	}
}

// Subscribe returns a channel of updates and a function that ends the subscription.
func (m *Monitor) Subscribe() (<-chan Update, func()) {
	ch := make(chan Update, 8)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Monitor) UpdateSettings(s settings.Settings) error {
	if err := m.store.Set(s); err != nil {
		return err
	}
	m.mu.Lock()
	m.pendSettings = &s
	m.addEventLocked(KindInfo, "info", "Settings updated")
	m.mu.Unlock()
	m.Poke()
	return nil
}

// Note records an event that doesn't come from the UPS, such as an update.
func (m *Monitor) Note(level, msg string) {
	m.mu.Lock()
	m.addEventLocked(KindInfo, level, msg)
	m.mu.Unlock()
	m.Poke()
}

// alarmCondition reports whether the UPS has a reason to be beeping.
func alarmCondition(st ups.Status) bool {
	return st.OnBattery || st.BatteryLow || st.Fault || st.LoadPercent >= 100
}

// Execute sends a validated control command and returns the raw protocol string.
func (m *Monitor) Execute(id string, p ups.Params) (string, error) {
	raw, err := ups.Build(id, p)
	if err != nil {
		return "", err
	}
	m.devMu.Lock()
	dev := m.dev
	m.devMu.Unlock()
	if dev == nil {
		return "", ErrDisconnected
	}

	// The beeper command is a toggle, so (like ViewPower) only send it when
	// the current state differs from the one requested.
	if want, ok := ups.BeeperTarget(id); ok {
		reply, err := dev.Exchange("QS\r", 2*time.Second)
		if err != nil {
			return "", fmt.Errorf("reading beeper state: %w", err)
		}
		st, err := ups.ParseStatus(reply)
		if err != nil {
			return "", err
		}
		if id == "alarm.mute" && !alarmCondition(st) {
			return "", fmt.Errorf("%w: no alarm is sounding; use UPS Alarm to turn it off", ups.ErrInvalidCommand)
		}
		if st.BeeperOn == want {
			if id != "alarm.mute" {
				m.mu.Lock()
				m.alarmMuted = false
				m.mu.Unlock()
			}
			return "", nil
		}
	}

	if err := sendControl(dev, raw); err != nil {
		return "", err
	}

	m.mu.Lock()
	switch id {
	case "alarm.mute":
		m.alarmMuted = true
	case "alarm.unmute", "beeper.on", "beeper.off":
		m.alarmMuted = false
	}
	if _, ok := ups.BeeperTarget(id); ok {
		m.beeperCmdAt = time.Now()
	}
	m.addEventLocked(commandKind(id), "info", ups.Describe(id, p))
	switch {
	case ups.IsTest(id):
		m.lastTest = &TestResult{Kind: id, Started: time.Now(), Result: "running"}
		m.testSawFault = false
	case id == "test.cancel" && m.lastTest != nil && m.lastTest.Result == "running":
		now := time.Now()
		m.lastTest.Ended, m.lastTest.Result = &now, "cancelled"
	}
	m.mu.Unlock()
	m.log.Info("command sent", "id", id, "raw", raw)
	m.Poke()
	return raw, nil
}

// sendControl sends a control command; these are normally silent, and an
// echo or "(NAK" means the UPS refused it.
func sendControl(dev *hid.Device, raw string) error {
	reply, err := dev.Exchange(raw+"\r", 1500*time.Millisecond)
	switch {
	case errors.Is(err, hid.ErrTimeout) && reply == "":
		return nil
	case err != nil:
		return fmt.Errorf("sending %s: %w", raw, err)
	case reply == raw || reply == "(NAK":
		return fmt.Errorf("the UPS rejected %s (replied %q)", raw, reply)
	}
	return nil
}

func commandKind(id string) string {
	switch id {
	case "alarm.mute":
		return KindAlarmMuted
	case "alarm.unmute":
		return KindAlarmUnmuted
	case "beeper.on", "beeper.off":
		return KindAlarm
	case "test.quick", "test.cancel":
		return KindSelfTest
	case "shutdown", "shutdown.cancel":
		return KindShutdown
	}
	return KindInfo
}

func (m *Monitor) poll() {
	dev, err := m.device_()
	if err != nil {
		m.fail(err, true)
		return
	}
	reply, err := dev.Exchange("QS\r", 2*time.Second)
	if err != nil {
		m.fail(err, errors.Is(err, hid.ErrRemoved) || !errors.Is(err, hid.ErrTimeout))
		return
	}
	st, err := ups.ParseStatus(reply)
	if err != nil {
		m.fail(err, false)
		return
	}
	if m.ratings == nil {
		if r, err := dev.Exchange("F\r", 2*time.Second); err == nil {
			if rt, err := ups.ParseRatings(r); err == nil {
				m.mu.Lock()
				m.ratings = &rt
				m.mu.Unlock()
			}
		}
	}
	if m.update(st) {
		m.autoUnmute(dev, st)
	}
}

// autoUnmute turns the beeper back on once the condition it was muted for clears.
func (m *Monitor) autoUnmute(dev *hid.Device, st ups.Status) {
	if !st.BeeperOn {
		if err := sendControl(dev, "Q"); err != nil {
			m.log.Warn("auto-unmute failed", "err", err)
			return
		}
	}
	m.mu.Lock()
	m.alarmMuted = false
	m.beeperCmdAt = time.Now()
	m.addEventLocked(KindAlarmUnmuted, "info", "Alarm unmuted: the alarm condition cleared")
	m.mu.Unlock()
	m.Poke()
}

// device_ returns the open device, opening it if needed.
func (m *Monitor) device_() (*hid.Device, error) {
	m.devMu.Lock()
	defer m.devMu.Unlock()
	if m.dev != nil {
		return m.dev, nil
	}
	d, err := hid.Open(ups.VendorID, ups.ProductID)
	if err != nil {
		return nil, err
	}
	m.dev = d
	return d, nil
}

func (m *Monitor) dropDevice() {
	m.devMu.Lock()
	defer m.devMu.Unlock()
	if m.dev != nil {
		m.dev.Close()
		m.dev = nil
	}
}

func (m *Monitor) fail(err error, deviceGone bool) {
	if deviceGone {
		m.dropDevice()
	}
	m.mu.Lock()
	m.failures++
	if m.failures < maxFailures && m.snap.Connected {
		m.mu.Unlock()
		return
	}
	wasConnected := m.snap.Connected
	m.snap.Connected = false
	m.snap.Error = err.Error()
	m.snap.Mode = "offline"
	m.snap.UpdatedAt = time.Now()
	m.ratings = nil
	switch {
	case wasConnected:
		m.addEventLocked(KindConnection, "alert", "Lost connection to the UPS: "+err.Error())
		m.notifyLocked("UPS connection lost", err.Error())
	case !m.everOnline && !m.waitNoted:
		m.waitNoted = true
		m.addEventLocked(KindConnection, "warn", "Waiting for the UPS: "+err.Error())
	}
	m.broadcastLocked(nil)
	m.mu.Unlock()
}

// update records a reading. It reports whether a muted alarm should now be
// unmuted because its condition has cleared.
func (m *Monitor) update(st ups.Status) (unmute bool) {
	now := time.Now()
	cfg := m.store.Get()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = 0

	var prev *ups.Status
	if m.snap.Connected {
		prev = m.snap.Status
	}
	if !m.snap.Connected {
		if m.everOnline {
			m.addEventLocked(KindConnection, "good", "Reconnected to the UPS")
		} else {
			m.addEventLocked(KindConnection, "good", "Connected to the UPS")
		}
		m.everOnline = true
	}

	snap := Snapshot{
		Connected:   true,
		UpdatedAt:   now,
		Status:      &st,
		Ratings:     m.ratings,
		Mode:        st.Mode(),
		LoadVA:      st.LoadPercent / 100 * float64(m.device.RatedVA),
		LoadW:       st.LoadPercent / 100 * float64(m.device.RatedW),
		AlarmActive: alarmCondition(st),
	}
	if c, ok := m.charge.Estimate(st, now); ok {
		snap.Charge = &c
	}
	m.transitionsLocked(prev, st, snap.Charge, now)

	sample := Sample{T: now.UnixMilli(), In: st.InputVoltage, Out: st.OutputVoltage, Load: st.LoadPercent, Batt: st.BatteryVoltage, Freq: st.Frequency, Charge: snap.Charge}
	m.history = append(m.history, sample)
	if m.actions.Log != nil {
		m.actions.Log.Sample(sample, snap.Mode)
	}
	cut := now.Add(-historyWindow).UnixMilli()
	i := 0
	for i < len(m.history) && m.history[i].T < cut {
		i++
	}
	m.history = m.history[i:]

	if snap.Charge != nil && m.ratings != nil {
		m.runtimeLocked(&snap, *snap.Charge, cfg.BatteryAh, m.recentLoadLocked(now))
	}
	snap.BatteryState = batteryState(st, snap.Charge, cfg.LowBatteryPercent)
	if st.OnBattery {
		snap.OnBatterySec = now.Sub(m.batterySince).Seconds()
	}
	unmute = m.alarmMuted && !snap.AlarmActive
	snap.AlarmMuted = m.alarmMuted
	m.snap = snap

	m.policyLocked(st, snap, cfg, now)
	m.broadcastLocked(&sample)
	return unmute
}

// recentLoadLocked averages the last minute of load readings; this UPS's
// load figure flickers by several percent between polls.
func (m *Monitor) recentLoadLocked(now time.Time) float64 {
	cut := now.Add(-time.Minute).UnixMilli()
	sum, n := 0.0, 0
	for i := len(m.history) - 1; i >= 0 && m.history[i].T >= cut; i-- {
		sum += m.history[i].Load
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func (m *Monitor) transitionsLocked(prev *ups.Status, st ups.Status, charge *float64, now time.Time) {
	was := func(f func(ups.Status) bool) bool { return prev != nil && f(*prev) }
	onBatt := func(s ups.Status) bool { return s.OnBattery }

	if st.OnBattery && !was(onBatt) {
		m.batterySince, m.summaryAt = now, now
		m.lowNotified, m.macShutdown = false, false
		if !st.TestRunning {
			m.addEventLocked(KindUtilityLost, "alert", "Utility power lost")
			m.addEventLocked(KindOnBattery, "warn", "Running on battery")
			m.notifyLocked("Running on battery", "Mains power was lost. The UPS is powering your equipment.")
		}
	}
	if prev != nil && prev.OnBattery && !st.OnBattery && !prev.TestRunning {
		m.addEventLocked(KindUtilityRestored, "good", fmt.Sprintf("Utility power restored after %s", round(now.Sub(m.batterySince))))
		if charge == nil || *charge < 99.5 {
			m.addEventLocked(KindCharging, "info", "Battery charging")
		}
		m.notifyLocked("Mains power restored", "The UPS is back on mains power.")
	}
	if st.BatteryLow && !was(func(s ups.Status) bool { return s.BatteryLow }) {
		m.addEventLocked(KindLowBattery, "alert", "Battery low")
		m.notifyLocked("UPS battery low", "The UPS battery is nearly empty.")
	}
	overload := func(s ups.Status) bool { return s.LoadPercent >= 100 }
	if overload(st) && !was(overload) {
		m.addEventLocked(KindOverload, "alert", fmt.Sprintf("Overload: load at %.1f%%", st.LoadPercent))
		m.notifyLocked("UPS overload", "Connected equipment draws more than the UPS is rated for.")
	}
	if !overload(st) && was(overload) {
		m.addEventLocked(KindOverload, "good", "Overload cleared")
	}
	if st.Fault && !was(func(s ups.Status) bool { return s.Fault }) {
		m.addEventLocked(KindFault, "alert", "The UPS reports a fault")
		m.notifyLocked("UPS fault", "The UPS reports an internal fault.")
	}
	if !st.Fault && was(func(s ups.Status) bool { return s.Fault }) {
		m.addEventLocked(KindFault, "good", "UPS fault cleared")
	}
	if st.AVRActive != was(func(s ups.Status) bool { return s.AVRActive }) && prev != nil {
		if st.AVRActive {
			m.addEventLocked(KindInfo, "info", fmt.Sprintf("Voltage regulation (AVR) engaged at %.1f V input", st.InputVoltage))
		} else {
			m.addEventLocked(KindInfo, "info", "Voltage regulation (AVR) off")
		}
	}
	if st.ShutdownActive && !was(func(s ups.Status) bool { return s.ShutdownActive }) {
		m.addEventLocked(KindShutdown, "warn", "Outlet shutdown is counting down")
		m.notifyLocked("UPS shutdown pending", "The UPS will turn its outlets off shortly.")
	}
	if !st.ShutdownActive && was(func(s ups.Status) bool { return s.ShutdownActive }) {
		m.addEventLocked(KindShutdown, "info", "Outlet shutdown ended or was cancelled")
	}
	// Changes we caused were already logged by Execute.
	if prev != nil && st.BeeperOn != prev.BeeperOn && now.Sub(m.beeperCmdAt) > beeperEcho {
		m.addEventLocked(KindAlarm, "info", map[bool]string{true: "UPS alarm turned on", false: "UPS alarm turned off"}[st.BeeperOn])
	}

	// Battery tests: a test that ran without raising battery-low or a fault passed.
	if st.TestRunning {
		if !was(func(s ups.Status) bool { return s.TestRunning }) {
			// The poll before was on mains: the voltage the test's drop starts from.
			m.testStart, m.testBefore, m.testLow, m.testMax = now, st.BatteryVoltage, st.BatteryVoltage, st.LoadPercent
			if prev != nil {
				m.testBefore = prev.BatteryVoltage
			}
			m.addEventLocked(KindSelfTest, "info", "Battery self-test started")
			if m.lastTest == nil || m.lastTest.Result != "running" {
				m.lastTest = &TestResult{Kind: "test", Started: now, Result: "running"}
				m.testSawFault = false
			}
		}
		m.testLow, m.testMax = min(m.testLow, st.BatteryVoltage), max(m.testMax, st.LoadPercent)
		if st.BatteryLow || st.Fault {
			m.testSawFault = true
		}
	}
	if !st.TestRunning && was(func(s ups.Status) bool { return s.TestRunning }) && m.lastTest != nil && m.lastTest.Result == "running" {
		m.lastTest.Ended = &now
		m.nextTest = &SelfTest{Result: "passed", Seconds: int(math.Round(now.Sub(m.testStart).Seconds())),
			Load: math.Round(m.testMax), VoltsBefore: round1(m.testBefore), VoltsLow: round1(m.testLow)}
		if m.testSawFault {
			m.nextTest.Result = "failed"
			m.lastTest.Result = "failed"
			m.addEventLocked(KindSelfTest, "alert", "UPS self-test failed")
			m.notifyLocked("UPS self-test failed", "The battery reported low or a fault during the test.")
		} else {
			m.lastTest.Result = "passed"
			m.addEventLocked(KindSelfTest, "good", "UPS self-test completed (passed)")
		}
	}
}

// policyLocked applies the low-battery alert and the optional Mac shutdown.
func (m *Monitor) policyLocked(st ups.Status, snap Snapshot, cfg settings.Settings, now time.Time) {
	if !st.OnBattery || st.TestRunning {
		return
	}
	low := st.BatteryLow || (snap.Charge != nil && *snap.Charge < float64(cfg.LowBatteryPercent))
	if low && !m.lowNotified {
		m.lowNotified = true
		msg := "Battery charge is below your alert level"
		if snap.Charge != nil {
			msg = fmt.Sprintf("Battery charge is %.1f%%, below your %d%% alert level", *snap.Charge, cfg.LowBatteryPercent)
		}
		m.addEventLocked(KindLowBattery, "alert", msg)
		m.notifyLocked("UPS battery low", msg)
	}
	elapsed := now.Sub(m.batterySince)
	if now.Sub(m.summaryAt) >= SummaryEvery {
		m.summaryAt = now
		m.notifyLocked("On battery for "+Duration(elapsed), "")
	}
	if !cfg.ShutdownMac || m.macShutdown {
		return
	}
	if low || elapsed >= time.Duration(cfg.ShutdownAfterMinutes)*time.Minute {
		m.macShutdown = true
		m.addEventLocked(KindShutdown, "alert", fmt.Sprintf("Shutting down this Mac after %s on battery", round(elapsed)))
		m.notifyLocked("Shutting down this Mac", "The power outage has lasted too long.")
		if m.actions.ShutDown != nil {
			go func() {
				if err := m.actions.ShutDown(); err != nil {
					m.log.Error("mac shutdown failed", "err", err)
					m.mu.Lock()
					m.addEventLocked(KindShutdown, "alert", "Could not shut down this Mac: "+err.Error())
					m.mu.Unlock()
				}
			}()
		}
	}
}

func (m *Monitor) addEventLocked(kind, level, msg string) {
	e := Event{Time: time.Now(), Kind: kind, Level: level, Message: msg, Test: m.nextTest}
	m.nextTest = nil
	m.log.Info("event", "kind", kind, "level", level, "msg", msg)
	m.events = append(m.events, e)
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
	m.pending = append(m.pending, e)
	if m.actions.Log != nil {
		m.actions.Log.Event(e)
	}
}

// Restore seeds the recent events, e.g. from the log after a restart, so the
// dashboard doesn't start empty. Call it before Run.
func (m *Monitor) Restore(events []Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := append(append([]Event(nil), events...), m.events...)
	m.events = all[max(0, len(all)-maxEvents):]
}

type note struct{ title, msg string }

// notifyLocked queues a notification. It goes out with the next update, so
// it can summarize the reading that caused it.
func (m *Monitor) notifyLocked(title, msg string) {
	m.notes = append(m.notes, note{title, msg})
}

// sendNotesLocked posts the queued notifications, each with a summary of
// the current reading.
func (m *Monitor) sendNotesLocked() {
	notes := m.notes
	m.notes = nil
	if len(notes) == 0 || m.actions.Notify == nil || !m.store.Get().Notify {
		return
	}
	snap := m.snap
	go func() {
		for _, n := range notes {
			if err := m.actions.Notify(n.title, WithSummary(n.msg, snap)); err != nil {
				m.log.Warn("notification failed", "err", err)
			}
		}
	}()
}

func (m *Monitor) broadcastLocked(sample *Sample) {
	m.sendNotesLocked()
	u := Update{Snapshot: m.snap, Sample: sample, Events: m.pending, Settings: m.pendSettings, LastTest: m.lastTest}
	m.pending, m.pendSettings = nil, nil
	for ch := range m.subs {
		select {
		case ch <- u:
		default: // slow client: it catches up on the next update
		}
	}
}

// runtimeLocked fills the runtime estimate for loadPct. The UPS reports load
// in whole percent, so anything under 1% is treated as 1% rather than "infinite".
func (m *Monitor) runtimeLocked(snap *Snapshot, charge float64, batteryAh int, loadPct float64) {
	rated := float64(m.device.RatedW)
	pack := m.ratings.BatteryVoltage
	w := math.Max(loadPct, 1) / 100 * rated
	if rt, ok := ups.EstimateRuntime(charge, w, pack, float64(batteryAh)); ok {
		snap.RuntimeMin, snap.RuntimeLoadW = &rt, w
	}
	for load := 5.0; load <= 100; load += 5 {
		if rt, ok := ups.EstimateRuntime(charge, load/100*rated, pack, float64(batteryAh)); ok {
			snap.RuntimeCurve = append(snap.RuntimeCurve, Point{Load: load, Minutes: rt})
		}
	}
}

func batteryState(st ups.Status, charge *float64, lowPct int) string {
	switch {
	case st.BatteryLow:
		return "low"
	case st.OnBattery && charge != nil && *charge < float64(lowPct):
		return "low"
	case st.OnBattery:
		return "discharging"
	case charge != nil && *charge >= 99.5:
		return "full"
	}
	return "charging"
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func round(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(math.Round(d.Seconds())))
	}
	return d.Round(time.Second).String()
}
