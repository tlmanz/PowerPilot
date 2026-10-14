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

const label = "io.github.tlmanz.powerpilot"

// Labels used by earlier builds, removed on install.
var legacyLabels = []string{"local.upsmonitor"}

// Install registers "exe serve" as a per-user LaunchAgent and starts it.
func Install(exe string, _ []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, l := range append(legacyLabels, label) {
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
	if out, err := exec.Command("launchctl", "bootstrap", domain(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall stops the agent and removes it.
func Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stop(label)
	if err := os.Remove(plistPath(home, label)); err != nil && !os.IsNotExist(err) {
		return err
	}
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
