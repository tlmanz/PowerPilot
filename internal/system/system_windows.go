// Windows: a tray balloon via PowerShell, shutdown.exe, and MessageBoxW.
package system

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var procMessageBox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

const createNoWindow = 0x08000000

// hidden runs a console tool without flashing a window.
func hidden(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

// Notify shows a tray balloon. The text travels in environment variables so
// it can never be interpreted as PowerShell code.
func Notify(title, msg string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms, System.Drawing;` +
		`$n = New-Object System.Windows.Forms.NotifyIcon;` +
		`$n.Icon = [System.Drawing.SystemIcons]::Information; $n.Visible = $true;` +
		`$n.ShowBalloonTip(10000, $env:PP_TITLE, $env:PP_MSG, 'Info');` +
		`Start-Sleep -Seconds 10; $n.Dispose()`
	cmd := hidden("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "PP_TITLE="+title, "PP_MSG="+msg)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// ShutDown starts a normal Windows shutdown with a one-minute warning.
func ShutDown() error {
	return hidden("shutdown.exe", "/s", "/t", "60", "/c", "PowerPilot: the power outage has lasted too long").Run()
}

// Alert shows a modal message box.
func Alert(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	const mbIconInformation = 0x40
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconInformation)
}
