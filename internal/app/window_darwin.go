// Package app shows the dashboard in a native macOS window (WKWebView), and
// PowerPilot's menu-bar icon with its panel.
package app

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// Cocoa must run on the process's main thread, which is where main starts.
func init() { runtime.LockOSThread() }

// Run opens the dashboard window and blocks until the user quits.
// It must be called from the main goroutine.
func Run(url string) { run(url, false, true) }

// Tray is what the menu-bar icon needs from the rest of the app.
type Tray struct {
	Start   func() // follows the service, with SetTray, once the icon is there
	Summary func() // posts the latest reading as a notification
}

// RunWithTray is Run with a menu-bar icon: a click opens a panel with the
// latest reading (the service's tray.html), a right-click a short menu.
// Closing the window then leaves the app in the menu bar, and with show
// false it starts there.
func RunWithTray(url string, show bool, t Tray) {
	tray = t
	run(url, true, show)
}

var tray Tray

//export upsappTrayStart
func upsappTrayStart() { tray.Start() }

//export upsappTraySummary
func upsappTraySummary() { go tray.Summary() } // off Cocoa's thread: it runs a program

func run(url string, tray, show bool) {
	u := C.CString(url)
	defer C.free(unsafe.Pointer(u))
	C.upsapp_run(u, C.bool(tray), C.bool(show))
}

// SetTray shows the menu-bar icon's image (a template PNG), the title beside
// it and its tooltip. Safe from any goroutine.
func SetTray(icon []byte, title, tooltip string) {
	t, tip := C.CString(title), C.CString(tooltip)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(tip))
	var p unsafe.Pointer
	if len(icon) > 0 {
		p = unsafe.Pointer(&icon[0])
	}
	C.upsapp_set_tray(p, C.int(len(icon)), t, tip)
}

// Show opens the dashboard window, or brings it forward.
func Show() { C.upsapp_show() }

// Quit quits the app.
func Quit() { C.upsapp_quit() }

// InUse reports whether the dashboard window or the menu-bar panel is open.
func InUse() bool { return bool(C.upsapp_in_use()) }
