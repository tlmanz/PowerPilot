// macOS backend: IOKit. All IOKit calls for a device run on one locked OS
// thread, because input reports are delivered through that thread's run loop.
package hid

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <stdlib.h>
#include "hid_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

type Device struct {
	reqs      chan request
	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
}

type request struct {
	cmd     string
	timeout time.Duration
	reply   chan result
}

type result struct {
	data string
	err  error
}

// Open finds the first device matching vid/pid and starts its I/O thread.
func Open(vid, pid int) (*Device, error) {
	d := &Device{
		reqs:   make(chan request),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	opened := make(chan error, 1)
	go d.loop(vid, pid, opened)
	if err := <-opened; err != nil {
		return nil, err
	}
	return d, nil
}

// Exchange sends cmd and returns the reply up to (not including) the '\r'.
// On ErrTimeout, any partial reply is returned with the error.
func (d *Device) Exchange(cmd string, timeout time.Duration) (string, error) {
	req := request{cmd: cmd, timeout: timeout, reply: make(chan result, 1)}
	select {
	case d.reqs <- req:
	case <-d.done:
		return "", ErrClosed
	}
	r := <-req.reply
	return r.data, r.err
}

func (d *Device) Close() {
	d.closeOnce.Do(func() { close(d.done) })
	<-d.exited
}

func (d *Device) loop(vid, pid int, opened chan<- error) {
	defer close(d.exited)
	// Never unlocked: the thread is discarded with its run loop state when we return.
	runtime.LockOSThread()

	var status C.int
	h := C.upshid_open(C.int(vid), C.int(pid), &status)
	if h == nil {
		opened <- statusErr(status)
		return
	}
	defer C.upshid_close(h)
	opened <- nil

	for {
		select {
		case <-d.done:
			return
		case req := <-d.reqs:
			req.reply <- exchange(h, req)
		}
	}
}

func exchange(h *C.upshid_dev, req request) result {
	cmd := C.CString(req.cmd)
	defer C.free(unsafe.Pointer(cmd))
	var out [512]C.char
	var n C.int
	status := C.upshid_exchange(h, cmd, C.int(len(req.cmd)), &out[0], C.int(len(out)), &n, C.double(req.timeout.Seconds()))
	return result{data: C.GoStringN(&out[0], n), err: statusErr(status)}
}

func statusErr(s C.int) error {
	switch s {
	case C.UPSHID_OK:
		return nil
	case C.UPSHID_ERR_NOT_FOUND:
		return ErrNotFound
	case C.UPSHID_ERR_TIMEOUT:
		return ErrTimeout
	case C.UPSHID_ERR_REMOVED:
		return ErrRemoved
	}
	switch uint32(s) {
	case 0xe00002e2:
		return errors.New("macOS denied access to the UPS (kIOReturnNotPermitted)")
	case 0xe00002c5:
		return errors.New("UPS is in use by another program (kIOReturnExclusiveAccess)")
	}
	return fmt.Errorf("IOKit error 0x%08x", uint32(s))
}
