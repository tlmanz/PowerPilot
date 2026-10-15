//go:build !darwin

// Package app shows the dashboard in its own window: a chromeless Edge,
// Chrome or Chromium app window when one is installed, else the default browser.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tlmanz/PowerPilot/internal/service"
	"github.com/tlmanz/PowerPilot/internal/settings"
)

// Run opens the dashboard window and returns; the browser owns the window.
func Run(url string) {
	// The page reports its size when resized; "?app" tells it this is our own
	// window, not a browser tab.
	size := settings.Window{Width: 1440, Height: 952}
	if saved, ok := settings.LoadWindow(service.WindowFile()); ok {
		size = saved
	}
	args := []string{
		"--app=" + strings.TrimSuffix(url, "/") + "/?app",
		fmt.Sprintf("--window-size=%d,%d", size.Width, size.Height),
		"--new-window",
	}
	// A profile of our own starts a separate browser process even when the
	// browser is already open, which otherwise ignores --window-size. It also
	// gives the window a stable app ID and the dashboard's icon on Linux.
	if dir, err := os.UserCacheDir(); err == nil {
		args = append(args, "--user-data-dir="+filepath.Join(dir, "PowerPilot", "browser"),
			"--no-first-run", "--no-default-browser-check")
	}
	for _, b := range browsers() {
		path, err := exec.LookPath(b)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args...)
		if cmd.Start() == nil {
			go cmd.Wait()
			return
		}
	}
	openDefault(url)
}

func browsers() []string {
	if runtime.GOOS != "windows" {
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"}
	}
	var paths []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		paths = append(paths,
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return paths
}

func openDefault(url string) {
	if runtime.GOOS == "windows" {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		return
	}
	exec.Command("xdg-open", url).Start()
}
