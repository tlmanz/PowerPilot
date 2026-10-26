// Package cloud links PowerPilot to a powerpilot-server (the PowerPilotHardware
// repo's server/ and docs/cloud.md). The two repos share no code; they agree
// on JSON.
//
// Remote mode shows a UPS from the server instead of USB. Publish mode keeps
// the USB UPS and also sends it to the server as a device.
package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

const (
	ModeOff     = "off"
	ModeRemote  = "remote"
	ModePublish = "publish"
)

// Config is the Cloud setting. It's kept apart from settings.json because it
// holds a token: cloud.json, readable by this user only.
type Config struct {
	Mode   string `json:"mode"`
	URL    string `json:"url,omitempty"`
	Device string `json:"device,omitempty"`
	Token  string `json:"token,omitempty"`
}

var deviceRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Load reads the Cloud setting; without a file, the cloud is off.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Mode: ModeOff}, nil
	}
	if err != nil {
		return Config{Mode: ModeOff}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{Mode: ModeOff}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Mode != ModeRemote && c.Mode != ModePublish {
		c.Mode = ModeOff
	}
	return c, nil
}

func save(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseLink reads a pairing link from the server's admin page:
// powerpilot://pair?url=...&token=...[&device=...].
func ParseLink(link string) (Config, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "powerpilot" || u.Host != "pair" {
		return Config{}, errors.New("that isn't a PowerPilot pairing link (powerpilot://pair?...)")
	}
	q := u.Query()
	return Config{URL: q.Get("url"), Token: q.Get("token"), Device: q.Get("device")}, nil
}

// serverURL checks a server address. A bare host name gets https://.
func serverURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the server address must look like https://ups.example.com")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Request is a change to the Cloud setting from the dashboard. A pairing link
// fills in the address, the device and the token; an empty token keeps the
// saved one.
type Request struct {
	Mode   string `json:"mode"`
	Link   string `json:"link,omitempty"`
	URL    string `json:"url,omitempty"`
	Device string `json:"device,omitempty"`
	Token  string `json:"token,omitempty"`
}

// Status is the Cloud setting as the dashboard sees it: never the token.
type Status struct {
	Mode      string `json:"mode"`
	URL       string `json:"url,omitempty"`
	Device    string `json:"device,omitempty"`
	TokenSet  bool   `json:"token_set"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// Link is the running mode's connection to the server.
type Link interface {
	LinkStatus() (connected bool, problem string)
}

// Settings is the Cloud setting on disk, and the running link, for the dashboard.
type Settings struct {
	path  string
	link  Link
	check func(context.Context, Config) error // asks the server whether the setting works

	mu  sync.Mutex
	cfg Config
}

// NewSettings serves the setting in path. link is the running mode's
// connection (nil when off); info is this computer's UPS, for Publish mode.
func NewSettings(path string, cfg Config, link Link, info func() monitor.Device) *Settings {
	return &Settings{path: path, cfg: cfg, link: link, check: func(ctx context.Context, c Config) error { return checkServer(ctx, c, info) }}
}

func (s *Settings) Status() Status {
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()
	st := Status{Mode: c.Mode, URL: c.URL, Device: c.Device, TokenSet: c.Token != ""}
	if s.link != nil && c.Mode != ModeOff {
		st.Connected, st.Error = s.link.LinkStatus()
	}
	return st
}

// Apply checks a new setting with the server and saves it. The service then
// restarts to use it.
func (s *Settings) Apply(ctx context.Context, r Request) (Status, error) {
	s.mu.Lock()
	cur := s.cfg
	s.mu.Unlock()

	next := cur
	next.Mode = r.Mode
	if r.Link != "" {
		l, err := ParseLink(r.Link)
		if err != nil {
			return Status{}, err
		}
		next.URL, next.Token = l.URL, l.Token
		if l.Device != "" {
			next.Device = l.Device
		}
	}
	if r.URL != "" {
		next.URL = r.URL
	}
	if r.Device != "" {
		next.Device = r.Device
	}
	if r.Token != "" {
		next.Token = strings.TrimSpace(r.Token)
	}

	switch next.Mode {
	case ModeOff:
	case ModeRemote, ModePublish:
		u, err := serverURL(next.URL)
		if err != nil {
			return Status{}, err
		}
		next.URL = u
		switch {
		case !deviceRe.MatchString(next.Device):
			return Status{}, errors.New("the device ID is lowercase letters, digits and hyphens, like home")
		case next.Token == "":
			return Status{}, errors.New("paste the pairing link, or the token from the server's admin page")
		}
		if err := s.check(ctx, next); err != nil {
			return Status{}, err
		}
	default:
		return Status{}, errors.New("mode must be off, remote or publish")
	}
	if err := save(s.path, next); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	s.cfg = next
	s.mu.Unlock()
	return Status{Mode: next.Mode, URL: next.URL, Device: next.Device, TokenSet: next.Token != "", Connected: next.Mode != ModeOff}, nil
}

// checkServer tries the setting the way the mode will use it, and explains
// what's wrong in the user's terms.
func checkServer(ctx context.Context, c Config, info func() monitor.Device) error {
	cl := newClient(c)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var err error
	if c.Mode == ModeRemote {
		err = cl.do(ctx, http.MethodGet, cl.devicePath("/state"), nil, nil)
	} else {
		err = cl.do(ctx, http.MethodPut, "/api/publish/"+c.Device+"/info", info(), nil)
	}
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
		return errors.New("the server didn't accept the token: pair again from its admin page")
	case errors.As(err, &ae) && ae.Status == http.StatusForbidden && c.Mode == ModeRemote:
		return errors.New("this token can only publish: Remote mode needs one made for \"Computer: view a UPS\"")
	case errors.As(err, &ae) && ae.Status == http.StatusForbidden:
		return fmt.Errorf("this token is for another device than %s", c.Device)
	case errors.As(err, &ae) && ae.Status == http.StatusNotFound && c.Mode == ModeRemote:
		return fmt.Errorf("the server has no UPS called %s yet", c.Device)
	}
	return err
}

// client talks to the server's API with a bearer token.
type client struct {
	base, device, token string
	http                *http.Client
	streams             *http.Client
}

func newClient(c Config) *client {
	return &client{base: c.URL, device: c.Device, token: c.Token,
		// A command waits up to 90 s for the device.
		http:    &http.Client{Timeout: 100 * time.Second},
		streams: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second}},
	}
}

func (c *client) devicePath(p string) string { return "/api/devices/" + c.device + p }

// apiError is the server's error answer.
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func (c *client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do sends a request. out is decoded from JSON, or copied if it's an io.Writer.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach the server: %w", errors.Unwrap(err))
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = "the server answered " + res.Status
		}
		return &apiError{Status: res.StatusCode, Msg: e.Error}
	}
	switch out := out.(type) {
	case nil:
		return nil
	case io.Writer:
		_, err = io.Copy(out, res.Body)
		return err
	default:
		return json.NewDecoder(res.Body).Decode(out)
	}
}

// relay sends a JSON body as it is and returns the server's answer as it is.
func (c *client) relay(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var b any
	if body != nil {
		b = json.RawMessage(body)
	}
	req, err := c.request(ctx, method, path, b)
	if err != nil {
		return 0, nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("can't reach the server: %w", errors.Unwrap(err))
	}
	defer res.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, reply, err
}

// stream reads a server-sent event stream, calling fn for each message,
// until it ends. The server pings every 25 s, so a minute of silence means
// the connection is dead.
func (c *client) stream(ctx context.Context, path string, fn func(event string, data []byte)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.streams.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach the server: %w", errors.Unwrap(err))
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return &apiError{Status: res.StatusCode, Msg: "the server answered " + res.Status}
	}
	quiet := time.AfterFunc(time.Minute, cancel)
	defer quiet.Stop()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var event string
	var data []byte
	for sc.Scan() {
		quiet.Reset(time.Minute)
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if data != nil {
				fn(event, data)
			}
			event, data = "", nil
		case line[0] == ':': // a comment: the server's ping
		case bytes.HasPrefix(line, []byte("event:")):
			event = strings.TrimSpace(string(line[6:]))
		case bytes.HasPrefix(line, []byte("data:")):
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(line[5:], []byte(" "))...)
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return errors.New("the server closed the stream")
}

// backoff waits between reconnects: 1 s, doubling to 30 s, and back to 1 s
// after a connection that lasted.
type backoff struct{ d time.Duration }

func (b *backoff) wait(ctx context.Context, lasted time.Duration, wake <-chan struct{}) {
	if b.d == 0 || lasted > time.Minute {
		b.d = time.Second
	}
	select {
	case <-ctx.Done():
	case <-wake:
	case <-time.After(b.d):
	}
	b.d = min(b.d*2, 30*time.Second)
}
