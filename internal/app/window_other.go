//go:build !darwin

// Package app shows the dashboard in its own window: a chromeless Edge,
// Chrome or Chromium app window when one is installed, else the default browser.
package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Run opens the dashboard window and returns; the browser owns the window.
func Run(url string) {
	for _, b := range browsers() {
		path, err := exec.LookPath(b)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, "--app="+url, "--window-size=1440,952", "--new-window")
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
