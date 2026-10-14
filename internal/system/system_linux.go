// Linux: notify-send for banners, systemd for shutdown, zenity for dialogs.
package system

import (
	"fmt"
	"os"
	"os/exec"
)

func Notify(title, msg string) error {
	return exec.Command("notify-send", "--app-name=PowerPilot", title, msg).Run()
}

// ShutDown powers off through systemd (allowed for the active desktop user).
func ShutDown() error {
	return exec.Command("systemctl", "poweroff").Run()
}

// Alert shows a dialog when zenity is available, otherwise prints and notifies.
func Alert(title, msg string) {
	if exec.Command("zenity", "--info", "--no-markup", "--title="+title, "--text="+msg).Run() == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg)
	Notify(title, msg)
}
