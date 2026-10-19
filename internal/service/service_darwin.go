package service

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	label = "io.github.tlmanz.powerpilot"
	// The menu-bar icon, a LaunchAgent of its own: the service has no UI.
	trayLabel = label + ".tray"
)

// Labels used by earlier builds, removed on install.
var legacyLabels = []string{"local.upsmonitor"}

// Install registers "exe serve" and the menu-bar icon as per-user
// LaunchAgents and starts them.
func Install(exe string, _ []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, l := range append(legacyLabels, label, trayLabel) {
		stop(l)
	}
	for _, l := range legacyLabels {
		os.Remove(plistPath(home, l))
	}

	path := plistPath(home, label)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(home, "Library", "Logs", "PowerPilot.log")
	plist := fmt.Sprintf(plistTemplate, label, escape(exe), escape(logPath), escape(logPath))
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	if err := bootstrap(path); err != nil {
		return err
	}
	addTray(home, exe) // an extra: the app shows the icon too while it's open
	return nil
}

// Refresh adds the menu-bar icon's LaunchAgent, which builds before it
// lacked, after an update, and starts it.
func Refresh(exe string, _ []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, plist := trayAgent(home, exe)
	if have, err := os.ReadFile(path); err == nil && string(have) == plist {
		return nil // already there; if it isn't running, it was quit
	}
	stop(trayLabel)
	return addTray(home, exe)
}

// addTray writes the menu-bar icon's LaunchAgent and starts it.
func addTray(home, exe string) error {
	path, plist := trayAgent(home, exe)
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	return bootstrap(path)
}

// trayAgent is the menu-bar icon's LaunchAgent, its path and contents. It
// runs "exe tray" at login, and again only if it crashes: Quit lasts until
// the next login, or until the app is opened.
func trayAgent(home, exe string) (path, plist string) {
	logPath := filepath.Join(home, "Library", "Logs", "PowerPilot.log")
	return plistPath(home, trayLabel), fmt.Sprintf(trayPlistTemplate, trayLabel, escape(exe), escape(logPath), escape(logPath))
}

func bootstrap(path string) error {
	if out, err := exec.Command("launchctl", "bootstrap", domain(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall stops the agents, and the app's menu-bar icon, and removes them.
func Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, l := range []string{label, trayLabel} {
		stop(l)
		if err := os.Remove(plistPath(home, l)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	stopTray()
	return nil
}

func domain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func plistPath(home, l string) string {
	return filepath.Join(home, "Library", "LaunchAgents", l+".plist")
}

// stop boots the agent out and waits for it to go; bootout returns early.
func stop(l string) {
	target := domain() + "/" + l
	if exec.Command("launchctl", "bootout", target).Run() != nil {
		return
	}
	for i := 0; i < 20 && exec.Command("launchctl", "print", target).Run() == nil; i++ {
		time.Sleep(250 * time.Millisecond)
	}
}

func escape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

// trayPlistTemplate keeps the menu-bar icon to the logged-in (Aqua) session.
const trayPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>tray</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`
