// Package app shows the dashboard in a native macOS window (WKWebView).
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
func Run(url string) {
	u := C.CString(url)
	defer C.free(unsafe.Pointer(u))
	C.upsapp_run(u)
}
