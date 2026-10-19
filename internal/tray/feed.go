package tray

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

// The service pings its stream every 25 s; silence for longer means it's gone.
const streamIdle = time.Minute

// follow keeps up with the service at base until ctx ends, as the dashboard
// does: its whole state, then its stream of updates. It calls show after
// every change, with ok false while the service can't be reached.
func follow(ctx context.Context, base string, show func(st monitor.State, ok bool)) {
	wait := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		session(ctx, base, show)
		if ctx.Err() != nil {
			return
		}
		show(monitor.State{}, false)
		if time.Since(start) > time.Minute {
			wait = time.Second // it was up a while: likely a restart, so try again soon
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 15*time.Second)
	}
}

// session loads the state, then follows the stream until it breaks.
func session(ctx context.Context, base string, show func(monitor.State, bool)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := load(ctx, base)
	if err != nil {
		return err
	}
	show(st, true)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/stream", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream: %s", resp.Status)
	}
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for lines.Scan() {
		idle.Reset(streamIdle)
		data, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}
		var u monitor.Update
		if json.Unmarshal([]byte(data), &u) != nil {
			continue
		}
		st.Snapshot = u.Snapshot
		if u.Settings != nil {
			st.Settings = *u.Settings
		}
		show(st, true)
	}
	if err := lines.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func load(ctx context.Context, base string) (monitor.State, error) {
	var st monitor.State
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/state", nil)
	if err != nil {
		return st, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("state: %s", resp.Status)
	}
	err = json.NewDecoder(resp.Body).Decode(&st)
	st.History, st.Events = nil, nil // the tray shows only the latest reading
	return st, err
}

// command sends a dashboard command, such as "alarm.mute", to the service.
func command(ctx context.Context, base, id string) error {
	body, _ := json.Marshal(map[string]string{"id": id})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/command", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var reply struct{ Error string }
	if json.NewDecoder(resp.Body).Decode(&reply) == nil && reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return fmt.Errorf("the service replied %s", resp.Status)
}
