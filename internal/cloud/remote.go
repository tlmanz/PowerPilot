package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/tlmanz/PowerPilot/internal/logbook"
	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

const (
	historyWindow = time.Hour
	maxEvents     = 200
	// How long the UPS can be out of sight before it's worth a notification:
	// a quick reboot or a WiFi blip shouldn't alert anyone (as the server does).
	lostGrace = 2 * time.Minute
	// Events older than this, replayed after the UPS reconnects, are logged
	// but don't pop up a notification.
	notifyMaxAge = 10 * time.Minute
)

// Remote is Remote mode: the dashboard's UPS is one on the server, mirrored
// here. The dashboard, desktop notifications and the shutdown of this
// computer work as with USB; the UPS's own monitor (on the ESP32 or another
// PowerPilot) has already turned its readings into events.
type Remote struct {
	c       *client
	store   *settings.Store
	actions monitor.Actions
	version string
	host    string
	log     *slog.Logger
	poke    chan struct{}

	mu        sync.Mutex
	device    monitor.Device
	snap      monitor.Snapshot
	history   []monitor.Sample
	events    []monitor.Event
	remote    *settings.Settings  // the UPS's settings on the server
	lastTest  *monitor.TestResult // the latest self-test, from the server's battery health
	online    bool                // the UPS is sending to the server
	reachable bool                // the server answers
	problem   string
	lostAt    time.Time // when the UPS went out of sight
	lostNoted bool
	onBattery time.Time // when this outage started, as far as we know
	summaryAt time.Time // last outage summary notification
	shutDown  bool      // this computer's shutdown already started
	subs      map[chan monitor.Update]struct{}
}

// NewRemote mirrors cfg's device. actions are this computer's notifications
// and shutdown; version is PowerPilot's.
func NewRemote(cfg Config, store *settings.Store, actions monitor.Actions, version string, log *slog.Logger) *Remote {
	host := cfg.URL
	if u, err := url.Parse(cfg.URL); err == nil {
		host = u.Host
	}
	return &Remote{c: newClient(cfg), store: store, actions: actions, version: version, host: host, log: log,
		poke: make(chan struct{}, 1), problem: "connecting to the server",
		snap: monitor.Snapshot{Mode: "offline", BatteryState: "unknown"}, subs: map[chan monitor.Update]struct{}{}}
}

// The server's GET .../state and its stream's updates: PowerPilot's own
// types, plus whether the UPS is online.
type serverState struct {
	Device   monitor.Device      `json:"device"`
	Snapshot monitor.Snapshot    `json:"snapshot"`
	History  []monitor.Sample    `json:"history"`
	Events   []monitor.Event     `json:"events"`
	Settings settings.Settings   `json:"settings"`
	LastTest *monitor.TestResult `json:"last_test"`
	Online   bool                `json:"online"`
}

type serverUpdate struct {
	Snapshot monitor.Snapshot    `json:"snapshot"`
	Sample   *monitor.Sample     `json:"sample"`
	Events   []monitor.Event     `json:"events"`
	Settings *settings.Settings  `json:"settings"`
	LastTest *monitor.TestResult `json:"last_test"` // only when a self-test ends
	Online   bool                `json:"online"`
}

// Run follows the server until done is closed, reconnecting as needed.
func (r *Remote) Run(done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-done; cancel() }()
	go r.watch(ctx)
	var wait backoff
	for ctx.Err() == nil {
		start := time.Now()
		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		r.lost(err)
		wait.wait(ctx, time.Since(start), r.poke)
	}
}

// session loads the whole state, then follows the stream until it breaks.
func (r *Remote) session(ctx context.Context) error {
	var st serverState
	if err := r.c.do(ctx, http.MethodGet, r.c.devicePath("/state"), nil, &st); err != nil {
		return err
	}
	r.load(st)
	return r.c.stream(ctx, r.c.devicePath("/stream"), func(_ string, data []byte) {
		var u serverUpdate
		if err := json.Unmarshal(data, &u); err == nil {
			r.apply(u)
		}
	})
}

func (r *Remote) load(st serverState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reachable {
		r.log.Info("following the UPS on the server", "device", r.c.device, "server", r.host)
	}
	r.device, r.snap, r.history, r.lastTest, r.online = st.Device, st.Snapshot, st.History, st.LastTest, st.Online
	r.events = append(st.Events, r.localSince(st.Events)...)
	s := st.Settings
	r.remote = &s
	r.reachable, r.problem = true, ""
	r.checkLocked(time.Now())
	r.broadcastLocked(nil, nil, true)
}

// localSince keeps this computer's own events (notes, its shutdown) that
// the server's list doesn't have.
func (r *Remote) localSince(server []monitor.Event) []monitor.Event {
	var last time.Time
	if n := len(server); n > 0 {
		last = server[n-1].Time
	}
	var out []monitor.Event
	for _, e := range r.events {
		if e.Kind == monitor.KindInfo || e.Kind == monitor.KindShutdown {
			if e.Time.After(last) {
				out = append(out, e)
			}
		}
	}
	return out
}

func (r *Remote) apply(u serverUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.snap, r.online = u.Snapshot, u.Online
	if u.Sample != nil {
		r.history = append(r.history, *u.Sample)
		cut := now.Add(-historyWindow).UnixMilli()
		i := 0
		for i < len(r.history) && r.history[i].T < cut {
			i++
		}
		r.history = r.history[i:]
	}
	for _, e := range u.Events {
		r.appendLocked(e)
		r.notifyEvent(e, now)
	}
	if u.Settings != nil {
		s := *u.Settings
		r.remote = &s
	}
	if u.LastTest != nil {
		r.lastTest = u.LastTest
	}
	r.checkLocked(now)
	r.broadcastLocked(u.Sample, u.Events, u.Settings != nil)
}

func (r *Remote) lost(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reachable {
		r.log.Warn("lost the server", "err", err)
	}
	r.reachable, r.problem = false, err.Error()
	r.checkLocked(time.Now())
	r.broadcastLocked(nil, nil, false)
}

// watch notices the UPS staying out of sight, and keeps the shutdown timer
// running between updates.
func (r *Remote) watch(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.mu.Lock()
			r.checkLocked(now)
			r.mu.Unlock()
		}
	}
}

// checkLocked applies this computer's policy: tell the user when the UPS has
// been out of sight for a while, and shut this computer down during a long
// outage, as the USB monitor does.
func (r *Remote) checkLocked(now time.Time) {
	inSight := r.reachable && r.online
	switch {
	case !inSight && r.lostAt.IsZero():
		r.lostAt = now
	case !inSight && !r.lostNoted && now.Sub(r.lostAt) >= lostGrace:
		r.lostNoted = true
		why := "the UPS isn't sending to the server"
		if !r.reachable {
			why = "the server can't be reached"
		}
		r.addLocalLocked(monitor.KindConnection, "alert", fmt.Sprintf("Lost contact with %s: %s", r.c.device, why))
		r.notify("UPS out of contact", fmt.Sprintf("%s: %s.", r.c.device, why))
	case inSight:
		if r.lostNoted {
			r.addLocalLocked(monitor.KindConnection, "good", "Back in contact with "+r.c.device)
			r.notify("UPS back in contact", r.c.device+" is reporting again.")
		}
		r.lostAt, r.lostNoted = time.Time{}, false
	}

	st := r.snap.Status
	if !inSight || !r.snap.Connected || st == nil || !st.OnBattery || st.TestRunning {
		r.onBattery, r.shutDown = time.Time{}, false
		return
	}
	if r.onBattery.IsZero() {
		r.onBattery = now.Add(-time.Duration(r.snap.OnBatterySec * float64(time.Second)))
		r.summaryAt = now
	}
	if now.Sub(r.summaryAt) >= monitor.SummaryEvery {
		r.summaryAt = now
		r.notify("On battery for "+monitor.Duration(now.Sub(r.onBattery)), "")
	}
	cfg := r.store.Get()
	if !cfg.ShutdownMac || r.shutDown {
		return
	}
	low := st.BatteryLow || r.snap.BatteryState == "low"
	elapsed := now.Sub(r.onBattery)
	if !low && elapsed < time.Duration(cfg.ShutdownAfterMinutes)*time.Minute {
		return
	}
	r.shutDown = true
	r.addLocalLocked(monitor.KindShutdown, "alert", fmt.Sprintf("Shutting down this computer after %s on battery", elapsed.Round(time.Second)))
	r.notify("Shutting down this computer", "The power outage has lasted too long.")
	if r.actions.ShutDown != nil {
		go func() {
			if err := r.actions.ShutDown(); err != nil {
				r.log.Error("shutdown failed", "err", err)
				r.mu.Lock()
				r.addLocalLocked(monitor.KindShutdown, "alert", "Could not shut down this computer: "+err.Error())
				r.mu.Unlock()
			}
		}()
	}
}

// eventNotes are the notifications for the UPS's events, with the titles the
// USB monitor uses. An empty text means the event's own message.
var eventNotes = map[string]struct{ title, text string }{
	monitor.KindUtilityLost:     {"Running on battery", "Mains power was lost. The UPS is powering your equipment."},
	monitor.KindUtilityRestored: {"Mains power restored", "The UPS is back on mains power."},
	monitor.KindLowBattery:      {"UPS battery low", ""},
	monitor.KindOverload:        {"UPS overload", ""},
	monitor.KindFault:           {"UPS fault", ""},
	monitor.KindSelfTest:        {"UPS self-test failed", ""},
	monitor.KindShutdown:        {"UPS shutdown pending", ""},
	monitor.KindConnection:      {"UPS connection lost", ""},
}

// notifyEvent pops up a notification for an event from the UPS: power
// changes, and anything at alert level (or an outlet shutdown starting).
func (r *Remote) notifyEvent(e monitor.Event, now time.Time) {
	n, ok := eventNotes[e.Kind]
	if !ok || now.Sub(e.Time) > notifyMaxAge {
		return
	}
	power := e.Kind == monitor.KindUtilityLost || e.Kind == monitor.KindUtilityRestored
	if !power && e.Level != "alert" && !(e.Kind == monitor.KindShutdown && e.Level == "warn") {
		return
	}
	text := n.text
	if text == "" {
		text = e.Message
	}
	r.notify(n.title, r.c.device+": "+text)
}

// notify pops up a notification with a summary of the UPS's reading. r.mu is held.
func (r *Remote) notify(title, msg string) {
	if r.actions.Notify == nil || !r.store.Get().Notify {
		return
	}
	msg = monitor.WithSummary(msg, r.shownLocked())
	go func() {
		if err := r.actions.Notify(title, msg); err != nil {
			r.log.Warn("notification failed", "err", err)
		}
	}()
}

func (r *Remote) appendLocked(e monitor.Event) {
	r.events = append(r.events, e)
	if len(r.events) > maxEvents {
		r.events = r.events[len(r.events)-maxEvents:]
	}
}

// addLocalLocked records an event of this computer's own and shows it.
func (r *Remote) addLocalLocked(kind, level, msg string) {
	e := monitor.Event{Time: time.Now(), Kind: kind, Level: level, Message: msg}
	r.log.Info("event", "kind", kind, "level", level, "msg", msg)
	r.appendLocked(e)
	r.broadcastLocked(nil, []monitor.Event{e}, false)
}

// shownLocked is the snapshot as the dashboard should show it: not connected
// while the UPS is out of sight.
func (r *Remote) shownLocked() monitor.Snapshot {
	s := r.snap
	switch {
	case !r.reachable:
		s.Connected, s.Mode, s.Error = false, "offline", "Cloud: "+r.problem
	case !r.online:
		s.Connected, s.Mode, s.Error = false, "offline", "The UPS isn't sending to the server"
	}
	return s
}

func (r *Remote) settingsLocked() settings.Settings {
	s := r.store.Get()
	if r.remote != nil { // the UPS's own: they shape its battery figures
		s.BatteryAh, s.LowBatteryPercent, s.LogRetentionDays = r.remote.BatteryAh, r.remote.LowBatteryPercent, r.remote.LogRetentionDays
	}
	return s
}

func (r *Remote) broadcastLocked(sample *monitor.Sample, events []monitor.Event, withSettings bool) {
	u := monitor.Update{Snapshot: r.shownLocked(), Sample: sample, Events: events, LastTest: r.lastTest}
	if withSettings {
		s := r.settingsLocked()
		u.Settings = &s
	}
	for ch := range r.subs {
		select {
		case ch <- u:
		default: // a slow dashboard catches up on the next update
		}
	}
}

// LinkStatus is for the Cloud setting's status line.
func (r *Remote) LinkStatus() (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case !r.reachable:
		return false, r.problem
	case !r.online:
		return true, "the UPS isn't sending to the server"
	}
	return true, ""
}

// The dashboard's source (web.Source), as the USB monitor provides it.

func (r *Remote) State() monitor.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.device
	if d.Brand == "" && d.Model == "" {
		d.Brand = "UPS " + r.c.device
	}
	// PowerPilot's own version: the dashboard reloads when it changes.
	d.AppVersion = r.version
	d.Connection = fmt.Sprintf("Cloud: %s on %s", r.c.device, r.host)
	return monitor.State{
		Device:     d,
		Snapshot:   r.shownLocked(),
		History:    append([]monitor.Sample(nil), r.history...),
		Events:     append([]monitor.Event(nil), r.events...),
		Settings:   r.settingsLocked(),
		LastTest:   r.lastTest,
		IntervalMS: 5000,
	}
}

func (r *Remote) Subscribe() (<-chan monitor.Update, func()) {
	ch := make(chan monitor.Update, 8)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

// Poke asks the server to send its state again, and reconnects now if it's down.
func (r *Remote) Poke() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
	go r.c.do(context.Background(), http.MethodPost, r.c.devicePath("/refresh"), struct{}{}, nil)
}

// kindError keeps the server's message but matches the error the dashboard
// API maps to a status code.
type kindError struct {
	msg  string
	kind error
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

// Execute sends a command through the server, which waits for the UPS.
func (r *Remote) Execute(id string, p ups.Params) (string, error) {
	if _, err := ups.Build(id, p); err != nil {
		return "", err
	}
	body := map[string]any{"id": id}
	if p.Minutes != 0 {
		body["minutes"] = p.Minutes
	}
	if p.RestoreAfter != 0 {
		body["restore_after"] = p.RestoreAfter
	}
	var out struct {
		Sent string `json:"sent"`
	}
	err := r.c.do(context.Background(), http.MethodPost, r.c.devicePath("/command"), body, &out)
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.Status == http.StatusBadRequest:
		return "", &kindError{ae.Msg, ups.ErrInvalidCommand}
	case errors.As(err, &ae) && ae.Status == http.StatusServiceUnavailable:
		return "", &kindError{ae.Msg, monitor.ErrDisconnected}
	case err != nil:
		return "", err
	}
	return out.Sent, nil
}

// UpdateSettings saves them on the server, which sends the UPS its share,
// and here, for this computer's notifications and shutdown.
func (r *Remote) UpdateSettings(s settings.Settings) error {
	if err := r.c.do(context.Background(), http.MethodPut, r.c.devicePath("/settings"), s, nil); err != nil {
		return fmt.Errorf("saving on the server: %w", err)
	}
	if err := r.store.Set(s); err != nil {
		return err
	}
	r.mu.Lock()
	r.remote = &s
	r.addLocalLocked(monitor.KindInfo, "info", "Settings updated")
	r.broadcastLocked(nil, nil, true)
	r.mu.Unlock()
	return nil
}

// Note records an event of this computer's, such as an update.
func (r *Remote) Note(level, msg string) {
	r.mu.Lock()
	r.addLocalLocked(monitor.KindInfo, level, msg)
	r.mu.Unlock()
}

// Battery relays the dashboard's battery health requests (web.Battery) to the
// server's routes for this UPS: path is /battery or /battery/replaced. The
// server works it out from the UPS's readings (PowerPilotHardware
// docs/cloud.md, "Battery health"); bodies and replies pass through.
func (r *Remote) Battery(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, reply, err := r.c.relay(ctx, method, r.c.devicePath(path), body)
	if err != nil || status < 300 {
		return status, reply, err
	}
	var e struct {
		Error string `json:"error"`
	}
	switch {
	case json.Unmarshal(reply, &e) == nil && e.Error != "":
		return status, reply, nil // the server's own words, such as an invalid date
	case status == http.StatusNotFound:
		// A server from before battery health answers its unknown routes with a bare 404.
		return 0, nil, errors.New("this server doesn't work out battery health yet: update powerpilot-server")
	}
	return 0, nil, fmt.Errorf("the server answered %d %s", status, http.StatusText(status))
}

// Logs are the UPS's logs on the server (web.Logs).
func (r *Remote) Logs() *RemoteLogs { return &RemoteLogs{c: r.c} }

// RemoteLogs reads the UPS's day logs from the server.
type RemoteLogs struct{ c *client }

func (l *RemoteLogs) get(path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return l.c.do(ctx, http.MethodGet, l.c.devicePath(path), nil, out)
}

func (l *RemoteLogs) Days() ([]logbook.Day, int64) {
	var out struct {
		Days  []logbook.Day `json:"days"`
		Bytes int64         `json:"bytes"`
	}
	l.get("/logs", nil, &out)
	if out.Days == nil {
		out.Days = []logbook.Day{}
	}
	return out.Days, out.Bytes
}

func (l *RemoteLogs) Events(date string) ([]monitor.Event, error) {
	out := []monitor.Event{}
	return out, l.get("/logs/events", url.Values{"date": {date}}, &out)
}

func (l *RemoteLogs) Readings(date string) ([]logbook.Reading, error) {
	out := []logbook.Reading{}
	return out, l.get("/logs/readings", url.Values{"date": {date}}, &out)
}

func (l *RemoteLogs) ExportEvents(w io.Writer, from, to string) error {
	return l.get("/logs/export", url.Values{"kind": {"events"}, "from": {from}, "to": {to}}, w)
}

func (l *RemoteLogs) ExportReadings(w io.Writer, from, to string) error {
	return l.get("/logs/export", url.Values{"kind": {"readings"}, "from": {from}, "to": {to}}, w)
}

// Prune does nothing: the server keeps the UPS's logs.
func (l *RemoteLogs) Prune() {}
