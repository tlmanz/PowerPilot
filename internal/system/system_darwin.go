// Package system wraps the few OS actions the monitor needs:
// notifications, a clean computer shutdown, and a message dialog.
package system

import (
	"fmt"
	"os/exec"
	"strings"
)

// Notify posts a Notification Center banner.
func Notify(title, msg string) error {
	script := fmt.Sprintf("display notification %s with title %s", quote(msg), quote(title))
	return exec.Command("/usr/bin/osascript", "-e", script).Run()
}

// ShutDown asks macOS for a normal shutdown, so apps get a chance to save.
// The first call prompts for permission to control System Events.
func ShutDown() error {
	return exec.Command("/usr/bin/osascript", "-e", `tell application "System Events" to shut down`).Run()
}

// Alert shows a modal message (used by install/uninstall when there's no terminal).
func Alert(title, msg string) {
	script := fmt.Sprintf("display alert %s message %s", quote(title), quote(msg))
	exec.Command("/usr/bin/osascript", "-e", script).Run()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
