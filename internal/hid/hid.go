// Package hid exchanges '\r'-terminated text commands with a USB HID device
// that talks in fixed 8-byte reports, like the UPS's USB-serial bridge.
// Each OS has its own backend: IOKit (macOS), hidraw (Linux), hid.dll (Windows).
package hid

import "errors"

var (
	ErrNotFound    = errors.New("UPS not found on USB")
	ErrTimeout     = errors.New("timed out waiting for reply")
	ErrRemoved     = errors.New("UPS was unplugged")
	ErrClosed      = errors.New("device closed")
	ErrUnsupported = errors.New("USB HID access is not supported on this platform")
)

// reportSize is the bridge's fixed report length.
const reportSize = 8
