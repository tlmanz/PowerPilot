// Linux backend: the kernel's hidraw nodes (/dev/hidrawN), plain file I/O.
package hid

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Device struct {
	mu sync.Mutex
	f  *os.File
}

// Open finds the hidraw node whose HID_ID matches vid/pid.
func Open(vid, pid int) (*Device, error) {
	want := fmt.Sprintf("HID_ID=%04X:%08X:%08X", 3, vid, pid) // bus 3 = USB
	nodes, _ := filepath.Glob("/sys/class/hidraw/hidraw*")
	for _, n := range nodes {
		uevent, err := os.ReadFile(filepath.Join(n, "device", "uevent"))
		if err != nil || !strings.Contains(strings.ToUpper(string(uevent)), want) {
			continue
		}
		path := "/dev/" + filepath.Base(n)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("no permission to open %s: install 99-powerpilot.rules (see README)", path)
		}
		if err != nil {
			return nil, err
		}
		return &Device{f: f}, nil
	}
	return nil, ErrNotFound
}

// Exchange sends cmd and returns the reply up to (not including) the '\r'.
// On ErrTimeout, any partial reply is returned with the error.
func (d *Device) Exchange(cmd string, timeout time.Duration) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return "", ErrClosed
	}
	d.drain()

	data := []byte(cmd)
	for off := 0; off < len(data); off += reportSize {
		// hidraw wants the report ID first; this device uses none, so 0.
		report := make([]byte, reportSize+1)
		copy(report[1:], data[off:min(off+reportSize, len(data))])
		if _, err := d.f.Write(report); err != nil {
			return "", ioErr(err)
		}
	}

	var rx []byte
	buf := make([]byte, 64)
	deadline := time.Now().Add(timeout)
	for {
		if i := bytes.IndexByte(rx, '\r'); i >= 0 {
			return clean(rx[:i]), nil
		}
		if err := d.f.SetReadDeadline(deadline); err != nil {
			return "", err
		}
		n, err := d.f.Read(buf)
		rx = append(rx, buf[:n]...)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return clean(rx), ErrTimeout
		}
		if err != nil {
			return clean(rx), ioErr(err)
		}
	}
}

// drain drops input left over from an earlier exchange.
func (d *Device) drain() {
	buf := make([]byte, 64)
	for i := 0; i < 64; i++ {
		d.f.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
		if _, err := d.f.Read(buf); err != nil {
			return
		}
	}
}

func (d *Device) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
}

func ioErr(err error) error {
	if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.EIO) {
		return ErrRemoved
	}
	return err
}

func clean(b []byte) string {
	return string(bytes.ReplaceAll(b, []byte{0}, nil))
}
