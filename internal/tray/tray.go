// Package tray shows PowerPilot in the system tray: on macOS an icon in the
// menu bar whose panel the app draws (package app), on Windows and Linux an
// icon with a menu. The icon follows the UPS's battery, and the latest reading
// can go out as a notification. Like the dashboard, it follows the service's
// API, so it works the same in every Cloud mode.
package tray

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

// Options connect the tray to the service and to the rest of the app.
type Options struct {
	URL     string // the service's dashboard, e.g. http://127.0.0.1:8484
	Version string // this program's, to notice an update
	Open    func() // shows the dashboard, from the menu
	Quit    func() // ends the program, from the menu; Run's default returns from Run
	Notify  func(title, msg string) error
	// Restart, when set, runs the tray again on the program the service runs
	// once PowerPilot was updated. It returns only if it didn't: ErrNotNow
	// puts it off until the next update from the service.
	Restart func() error
	Log     *slog.Logger
}

// ErrNotNow is Options.Restart putting a restart off.
var ErrNotNow = errors.New("not now")

// restartedEnv names the service version a restart was for, so a program
// that doesn't match it after all doesn't restart again and again.
const restartedEnv = "POWERPILOT_TRAY_RESTARTED"

// Restarted reports whether this process is a tray restarted after an
// update; it may have to wait for the old one to let go of the icon.
func Restarted() bool { return os.Getenv(restartedEnv) != "" }

// Available reports why a tray icon can't be shown: on Linux, there's no
// desktop session bus to reach the panel through.
func Available() error {
	if runtime.GOOS != "linux" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return nil
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "bus")); err == nil {
			return nil
		}
	}
	return errors.New("no desktop session (D-Bus session bus) to show the tray icon in")
}

// Status is the tray icon as the app draws it on macOS.
type Status struct {
	Icon    []byte // a PNG template image, for the menu bar to tint
	Title   string // beside the icon: the charge, when it matters
	Tooltip string
}

// Follow follows the service for an icon drawn elsewhere, the app's
// menu-bar icon on macOS: show gets each change, from a goroutine of its own.
func Follow(opt Options, show func(Status)) *Tray {
	t := newTray(opt)
	var shown *view
	t.render = func(v view) {
		if shown != nil && v.glyph == shown.glyph && v.title == shown.title && v.tooltip == shown.tooltip {
			return
		}
		shown = &v
		show(Status{Icon: t.icon(v.glyph), Title: v.title, Tooltip: v.tooltip})
	}
	t.start()
	return t
}

// Tray follows the service and shows its latest reading.
type Tray struct {
	opt    Options
	ctx    context.Context
	cancel context.CancelFunc
	render func(view) // draws a view; only the goroutine following the service calls it

	mu   sync.Mutex
	view view // the latest

	icons        map[glyph][]byte // only render uses it
	restartedFor string
}

func newTray(opt Options) *Tray {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Tray{opt: opt, ctx: ctx, cancel: cancel, view: connecting, icons: map[glyph][]byte{}, restartedFor: os.Getenv(restartedEnv)}
}

// start shows the first view and follows the service.
func (t *Tray) start() {
	t.render(connecting)
	go follow(t.ctx, t.opt.URL, t.update)
}

// Stop stops following the service.
func (t *Tray) Stop() { t.cancel() }

func (t *Tray) update(st monitor.State, ok bool) {
	v := viewOf(st, ok)
	t.mu.Lock()
	t.view = v
	t.mu.Unlock()
	t.render(v)
	if ok {
		t.checkVersion(st.Device.AppVersion)
	}
}

// icon is g as the tray takes it: a template PNG on macOS, which the menu
// bar tints for light and dark mode; in color elsewhere, an .ico on Windows.
func (t *Tray) icon(g glyph) []byte {
	icon := t.icons[g]
	if icon == nil {
		switch runtime.GOOS {
		case "darwin":
			icon = pngOf(g.drawing(true), 36) // 18 pt, sharp on Retina
		case "windows":
			icon = icoOf(g.drawing(false))
		default:
			icon = pngOf(g.drawing(false), 32)
		}
		t.icons[g] = icon
	}
	return icon
}

// Summary shows the latest reading as a notification.
func (t *Tray) Summary() {
	t.mu.Lock()
	v := t.view
	t.mu.Unlock()
	var lines []string
	for _, r := range v.rows[1:] {
		if r != "" {
			lines = append(lines, r)
		}
	}
	if err := t.opt.Notify("PowerPilot: "+v.rows[0], strings.Join(lines, "\n")); err != nil {
		t.opt.Log.Warn("tray: notification failed", "err", err)
	}
}

// MuteAlarm silences the UPS's beeper until the alarm condition clears.
func (t *Tray) MuteAlarm() {
	if err := command(t.ctx, t.opt.URL, "alarm.mute"); err != nil {
		t.opt.Notify("Couldn't mute the alarm", err.Error())
	}
}

// checkVersion restarts the tray on the program the service runs, once
// PowerPilot was updated: on Windows that also lets go of the old program,
// so the next update can replace it.
func (t *Tray) checkVersion(service string) {
	if t.opt.Restart == nil || service == t.opt.Version || service == t.restartedFor ||
		!strings.HasPrefix(service, "v") || !strings.HasPrefix(t.opt.Version, "v") {
		return // the same, a development build, or tried already
	}
	os.Setenv(restartedEnv, service)
	err := t.opt.Restart()
	if errors.Is(err, ErrNotNow) {
		return
	}
	t.restartedFor = service
	t.opt.Log.Warn("tray: could not restart after the update", "version", service, "err", err)
}
