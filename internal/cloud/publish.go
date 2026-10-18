package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tlmanz/PowerPilot/internal/logbook"
	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/ups"
)

const (
	stateEvery   = 5 * time.Second // as the ESP32: every 5 s, and on any change
	maxQueue     = 1440            // a day of readings; events alike
	recentCmdIDs = 32              // repeats the server may deliver
)

// Source is what Publish mode needs from the USB monitor.
type Source interface {
	Subscribe() (<-chan monitor.Update, func())
	Execute(id string, p ups.Params) (string, error)
	UpdateSettings(settings.Settings) error
}

// Publisher is Publish mode: this computer's USB UPS, sent to the server as
// a device, which also takes the server's commands for it.
//
// It's the monitor's Recorder too (alongside the logbook), so the server gets
// the same events and per-minute readings as the log on disk. Anything the
// server misses while it's out of reach waits here and goes up on reconnect.
type Publisher struct {
	c     *client
	store *settings.Store
	info  monitor.Device
	log   *slog.Logger
	mon   Source
	wake  chan struct{}

	mu       sync.Mutex
	events   []wireEvent
	readings []logbook.Reading
	minutes  logbook.Minutes
	seq      int
	sending  bool // the last send worked
	listen   bool // the commands stream is open
	problem  string
	cmdIDs   []string
}

// wireEvent is an event as the server takes it: with an id, so a repeat
// after a reconnect is kept once.
type wireEvent struct {
	ID string `json:"id"`
	monitor.Event
}

// NewPublisher sends this computer's UPS, described by info, as cfg's device.
// Attach the monitor before Run.
func NewPublisher(cfg Config, store *settings.Store, info monitor.Device, log *slog.Logger) *Publisher {
	return &Publisher{c: newClient(cfg), store: store, info: info, log: log, wake: make(chan struct{}, 1),
		problem: "connecting to the server"}
}

// Attach gives the publisher the monitor it publishes and runs commands on.
func (p *Publisher) Attach(mon Source) { p.mon = mon }

func (p *Publisher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Event and Sample are the monitor's Recorder; they're called with the
// monitor locked, so they only queue.
func (p *Publisher) Event(e monitor.Event) {
	p.mu.Lock()
	p.seq++
	p.events = append(p.events, wireEvent{ID: fmt.Sprintf("%s-%d-%d", p.c.device, e.Time.UnixMilli(), p.seq), Event: e})
	if len(p.events) > maxQueue {
		p.events = p.events[len(p.events)-maxQueue:]
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Publisher) Sample(s monitor.Sample, mode string) {
	p.mu.Lock()
	if r, ok := p.minutes.Add(s, mode); ok {
		p.readings = append(p.readings, r)
		if len(p.readings) > maxQueue {
			p.readings = p.readings[len(p.readings)-maxQueue:]
		}
	}
	p.mu.Unlock()
}

// LinkStatus is for the Cloud setting's status line.
func (p *Publisher) LinkStatus() (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sending && p.listen {
		return true, ""
	}
	return false, p.problem
}

func (p *Publisher) setLink(sending, listen *bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sending != nil {
		p.sending = *sending
	}
	if listen != nil {
		p.listen = *listen
	}
	if err != nil {
		if p.problem != err.Error() {
			p.log.Warn("publishing to the server", "err", err)
		}
		p.problem = err.Error()
	}
}

// Run publishes until done is closed.
func (p *Publisher) Run(done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-done; cancel() }()
	go p.commands(ctx)
	p.send(ctx)
}

// stateKey is what counts as a change worth sending straight away.
func stateKey(s monitor.Snapshot) string {
	k := fmt.Sprint(s.Connected, s.Mode, s.BatteryState, s.AlarmActive, s.AlarmMuted)
	if st := s.Status; st != nil {
		k += fmt.Sprint(st.OnBattery, st.BatteryLow, st.Fault, st.TestRunning, st.ShutdownActive, st.BeeperOn)
	}
	return k
}

// send posts the info, the state and the backlog. After a failure it waits a
// little longer each time before trying again.
func (p *Publisher) send(ctx context.Context) {
	updates, stop := p.mon.Subscribe()
	defer stop()
	tick := time.NewTicker(stateEvery)
	defer tick.Stop()
	var (
		latest   *monitor.Snapshot
		lastKey  string
		lastSent time.Time
		infoSent bool
		retryAt  time.Time
		wait     = time.Second
	)
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-updates:
			latest = &u.Snapshot
		case <-tick.C:
		case <-p.wake:
		}
		if time.Now().Before(retryAt) {
			continue
		}
		err := func() error {
			if !infoSent {
				if err := p.c.do(ctx, http.MethodPut, "/api/publish/"+p.c.device+"/info", p.info, nil); err != nil {
					return err
				}
				infoSent = true
			}
			if latest != nil {
				if k := stateKey(*latest); k != lastKey || time.Since(lastSent) >= stateEvery {
					if err := p.c.do(ctx, http.MethodPost, "/api/publish/"+p.c.device+"/state", latest, nil); err != nil {
						return err
					}
					lastKey, lastSent = k, time.Now()
				}
			}
			return p.flush(ctx)
		}()
		if ctx.Err() != nil {
			return
		}
		ok := err == nil
		p.setLink(&ok, nil, err)
		if ok {
			wait, retryAt = time.Second, time.Time{}
			continue
		}
		infoSent = false
		retryAt = time.Now().Add(wait)
		wait = min(wait*2, time.Minute)
	}
}

// flush sends the queued events and readings, oldest first, and drops what
// the server took.
func (p *Publisher) flush(ctx context.Context) error {
	p.mu.Lock()
	evs := slices.Clone(p.events[:min(len(p.events), 500)])
	rds := slices.Clone(p.readings[:min(len(p.readings), 500)])
	p.mu.Unlock()
	if len(evs) > 0 {
		if err := p.c.do(ctx, http.MethodPost, "/api/publish/"+p.c.device+"/events", evs, nil); err != nil {
			return err
		}
		p.mu.Lock()
		p.events = p.events[min(len(evs), len(p.events)):]
		p.mu.Unlock()
	}
	if len(rds) > 0 {
		if err := p.c.do(ctx, http.MethodPost, "/api/publish/"+p.c.device+"/readings", rds, nil); err != nil {
			return err
		}
		p.mu.Lock()
		p.readings = p.readings[min(len(rds), len(p.readings)):]
		p.mu.Unlock()
	}
	return nil
}

// commands follows the server's stream of config and commands for this device.
func (p *Publisher) commands(ctx context.Context) {
	var wait backoff
	for ctx.Err() == nil {
		start := time.Now()
		err := p.c.stream(ctx, "/api/publish/"+p.c.device+"/commands", func(event string, data []byte) {
			open := true
			p.setLink(nil, &open, nil)
			switch event {
			case "config":
				p.applyConfig(data)
			case "cmd":
				p.runCommand(ctx, data)
			}
		})
		if ctx.Err() != nil {
			return
		}
		closed := false
		p.setLink(nil, &closed, err)
		wait.wait(ctx, time.Since(start), nil)
	}
}

// applyConfig takes the server's battery settings: they're set on the server
// (from the phone, say) for this UPS.
func (p *Publisher) applyConfig(data []byte) {
	var c struct {
		BatteryAh         int `json:"battery_ah"`
		LowBatteryPercent int `json:"low_battery_percent"`
	}
	if json.Unmarshal(data, &c) != nil {
		return
	}
	s := p.store.Get()
	if s.BatteryAh == c.BatteryAh && s.LowBatteryPercent == c.LowBatteryPercent {
		return
	}
	s.BatteryAh, s.LowBatteryPercent = c.BatteryAh, c.LowBatteryPercent
	if err := s.Validate(); err != nil {
		p.log.Warn("ignoring the server's config", "err", err)
		return
	}
	if err := p.mon.UpdateSettings(s); err != nil {
		p.log.Warn("could not apply the server's config", "err", err)
	}
}

type command struct {
	CmdID  string `json:"cmd_id"`
	ID     string `json:"id"`
	Params struct {
		Minutes      float64 `json:"minutes"`
		RestoreAfter int     `json:"restore_after"`
	} `json:"params"`
	ExpiresAt int64 `json:"expires_at"` // Unix ms
}

type result struct {
	CmdID  string `json:"cmd_id"`
	Status string `json:"status"` // done, rejected, invalid, expired, failed
	Raw    string `json:"raw,omitempty"`
	Error  string `json:"error,omitempty"`
}

// runCommand runs a command from the server with the ESP32's rules: never
// late, never twice, and checked again here (Execute validates it).
func (p *Publisher) runCommand(ctx context.Context, data []byte) {
	var c command
	if json.Unmarshal(data, &c) != nil || c.CmdID == "" {
		return
	}
	p.mu.Lock()
	seen := slices.Contains(p.cmdIDs, c.CmdID)
	if !seen {
		p.cmdIDs = append(p.cmdIDs, c.CmdID)
		if len(p.cmdIDs) > recentCmdIDs {
			p.cmdIDs = p.cmdIDs[1:]
		}
	}
	p.mu.Unlock()
	if seen {
		return
	}

	res := result{CmdID: c.CmdID}
	if c.ExpiresAt != 0 && time.Now().UnixMilli() > c.ExpiresAt {
		res.Status, res.Error = "expired", "the command arrived after it expired"
	} else {
		raw, err := p.mon.Execute(c.ID, ups.Params{Minutes: c.Params.Minutes, RestoreAfter: c.Params.RestoreAfter})
		res.Raw = raw
		switch {
		case err == nil:
			res.Status = "done"
		case errors.Is(err, ups.ErrInvalidCommand):
			res.Status = "invalid"
		case strings.Contains(err.Error(), "rejected"):
			res.Status = "rejected"
		default:
			res.Status = "failed"
		}
		if err != nil {
			res.Error = err.Error()
		}
	}
	p.log.Info("command from the server", "id", c.ID, "status", res.Status)
	if err := p.c.do(ctx, http.MethodPost, "/api/publish/"+p.c.device+"/results", res, nil); err != nil {
		p.log.Warn("could not send a command's result", "err", err)
	}
}

// Recorders sends the monitor's events and readings to several recorders:
// the logbook and the publisher.
type Recorders []monitor.Recorder

func (rs Recorders) Event(e monitor.Event) {
	for _, r := range rs {
		r.Event(e)
	}
}

func (rs Recorders) Sample(s monitor.Sample, mode string) {
	for _, r := range rs {
		r.Sample(s, mode)
	}
}
