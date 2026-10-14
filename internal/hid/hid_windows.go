// Windows backend: setupapi.dll + hid.dll through the syscall package.
package hid

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	modHid      = syscall.NewLazyDLL("hid.dll")
	modSetupapi = syscall.NewLazyDLL("setupapi.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procHidDGetHidGuid        = modHid.NewProc("HidD_GetHidGuid")
	procHidDGetAttributes     = modHid.NewProc("HidD_GetAttributes")
	procHidDSetOutputReport   = modHid.NewProc("HidD_SetOutputReport")
	procHidDGetPreparsedData  = modHid.NewProc("HidD_GetPreparsedData")
	procHidDFreePreparsedData = modHid.NewProc("HidD_FreePreparsedData")
	procHidPGetCaps           = modHid.NewProc("HidP_GetCaps")
	procGetClassDevs          = modSetupapi.NewProc("SetupDiGetClassDevsW")
	procEnumDeviceInterfaces  = modSetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procGetInterfaceDetail    = modSetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procDestroyDeviceInfoList = modSetupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procCreateEvent           = modKernel32.NewProc("CreateEventW")
	procGetOverlappedResult   = modKernel32.NewProc("GetOverlappedResult")
)

const (
	digcfPresent          = 0x02
	digcfDeviceInterface  = 0x10
	hidpStatusSuccess     = 0x00110000
	errDeviceNotConnected = syscall.Errno(1167)
	errInvalidHandle      = syscall.Errno(6)
)

type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

type interfaceData struct {
	Size     uint32
	Class    guid
	Flags    uint32
	Reserved uintptr
}

type hidAttributes struct {
	Size                         uint32
	VendorID, ProductID, Version uint16
}

type hidCaps struct {
	Usage, UsagePage                                                       uint16
	InputReportByteLength, OutputReportByteLength, FeatureReportByteLength uint16
	Reserved                                                               [17]uint16
	Counts                                                                 [10]uint16
}

type Device struct {
	mu            sync.Mutex
	h             syscall.Handle
	event         syscall.Handle
	inLen, outLen int
}

// Open finds the first HID interface whose vendor and product IDs match.
func Open(vid, pid int) (*Device, error) {
	var g guid
	procHidDGetHidGuid.Call(uintptr(unsafe.Pointer(&g)))
	info, _, err := procGetClassDevs.Call(uintptr(unsafe.Pointer(&g)), 0, 0, digcfPresent|digcfDeviceInterface)
	if syscall.Handle(info) == syscall.InvalidHandle {
		return nil, fmt.Errorf("listing HID devices: %w", err)
	}
	defer procDestroyDeviceInfoList.Call(info)

	for i := 0; ; i++ {
		data := interfaceData{Size: uint32(unsafe.Sizeof(interfaceData{}))}
		ok, _, _ := procEnumDeviceInterfaces.Call(info, 0, uintptr(unsafe.Pointer(&g)), uintptr(i), uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			return nil, ErrNotFound
		}
		if path, err := interfacePath(info, &data); err == nil {
			if d := tryOpen(path, vid, pid); d != nil {
				return d, nil
			}
		}
	}
}

func interfacePath(info uintptr, data *interfaceData) (string, error) {
	var size uint32
	procGetInterfaceDetail.Call(info, uintptr(unsafe.Pointer(data)), 0, 0, uintptr(unsafe.Pointer(&size)), 0)
	if size < 8 {
		return "", errors.New("no interface detail")
	}
	// SP_DEVICE_INTERFACE_DETAIL_DATA_W: a DWORD cbSize, then the UTF-16 path.
	buf := make([]uint16, size/2+1)
	cb := uint32(8)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		cb = 6
	}
	*(*uint32)(unsafe.Pointer(&buf[0])) = cb
	ok, _, err := procGetInterfaceDetail.Call(info, uintptr(unsafe.Pointer(data)), uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0, 0)
	if ok == 0 {
		return "", err
	}
	return syscall.UTF16ToString(buf[2:]), nil
}

func tryOpen(path string, vid, pid int) *Device {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil
	}
	attrs := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
	if ok, _, _ := procHidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&attrs))); ok == 0 ||
		int(attrs.VendorID) != vid || int(attrs.ProductID) != pid {
		syscall.CloseHandle(h)
		return nil
	}

	// Report lengths include the leading report-ID byte (9 for this device).
	inLen, outLen := reportSize+1, reportSize+1
	var pre uintptr
	if ok, _, _ := procHidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&pre))); ok != 0 {
		var caps hidCaps
		if st, _, _ := procHidPGetCaps.Call(pre, uintptr(unsafe.Pointer(&caps))); st == hidpStatusSuccess {
			inLen, outLen = int(caps.InputReportByteLength), int(caps.OutputReportByteLength)
		}
		procHidDFreePreparsedData.Call(pre)
	}
	ev, _, _ := procCreateEvent.Call(0, 1, 0, 0) // manual-reset, unsignalled
	if ev == 0 {
		syscall.CloseHandle(h)
		return nil
	}
	return &Device{h: h, event: syscall.Handle(ev), inLen: inLen, outLen: outLen}
}

// Exchange sends cmd and returns the reply up to (not including) the '\r'.
// On ErrTimeout, any partial reply is returned with the error.
func (d *Device) Exchange(cmd string, timeout time.Duration) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.h == 0 {
		return "", ErrClosed
	}
	for i := 0; i < 64; i++ {
		if _, err := d.read(5 * time.Millisecond); err != nil {
			break // nothing stale left
		}
	}

	data := []byte(cmd)
	for off := 0; off < len(data); off += reportSize {
		report := make([]byte, max(d.outLen, reportSize+1)) // [0] is report ID 0
		copy(report[1:], data[off:min(off+reportSize, len(data))])
		if ok, _, err := procHidDSetOutputReport.Call(uintptr(d.h), uintptr(unsafe.Pointer(&report[0])), uintptr(len(report))); ok == 0 {
			return "", ioErr(err)
		}
	}

	var rx []byte
	deadline := time.Now().Add(timeout)
	for {
		if i := bytes.IndexByte(rx, '\r'); i >= 0 {
			return clean(rx[:i]), nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			return clean(rx), ErrTimeout
		}
		report, err := d.read(left)
		if err != nil {
			return clean(rx), err
		}
		if len(report) > 1 {
			rx = append(rx, report[1:]...) // drop the report-ID byte
		}
	}
}

// read waits up to timeout for one input report.
func (d *Device) read(timeout time.Duration) ([]byte, error) {
	buf := make([]byte, max(d.inLen, reportSize+1))
	ov := syscall.Overlapped{HEvent: d.event}
	var n uint32
	err := syscall.ReadFile(d.h, buf, &n, &ov)
	if err == syscall.ERROR_IO_PENDING {
		ms := uint32(max(timeout.Milliseconds(), 1))
		if ev, _ := syscall.WaitForSingleObject(d.event, ms); ev != syscall.WAIT_OBJECT_0 {
			syscall.CancelIoEx(d.h, &ov)
			overlappedResult(d.h, &ov, &n, true) // let the cancel finish before buf goes away
			return nil, ErrTimeout
		}
		err = overlappedResult(d.h, &ov, &n, false)
	}
	if err != nil {
		return nil, ioErr(err)
	}
	return buf[:n], nil
}

func overlappedResult(h syscall.Handle, ov *syscall.Overlapped, n *uint32, wait bool) error {
	w := uintptr(0)
	if wait {
		w = 1
	}
	if ok, _, err := procGetOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(n)), w); ok == 0 {
		return err
	}
	return nil
}

func (d *Device) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.h != 0 {
		syscall.CloseHandle(d.h)
		syscall.CloseHandle(d.event)
		d.h, d.event = 0, 0
	}
}

func ioErr(err error) error {
	if errors.Is(err, errDeviceNotConnected) || errors.Is(err, errInvalidHandle) {
		return ErrRemoved
	}
	return err
}

func clean(b []byte) string {
	return string(bytes.ReplaceAll(b, []byte{0}, nil))
}
