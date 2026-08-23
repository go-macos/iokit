// Package hid reads and writes macOS HID devices through IOKit, with no cgo.
//
// It exists because the fleet had no way at all to talk to a USB HID device:
// XR glasses, game controllers, sensor pucks and the like publish their data as
// vendor-specific HID reports, and reaching them meant either cgo and hidapi or
// shelling out. Everything here goes through github.com/ebitengine/purego, so a
// consumer builds with CGO_ENABLED=0.
//
// The shape of the API follows what IOKit actually is, rather than hiding it.
// Enumeration ([Devices]) is cheap and does not open anything. Writing a report
// ([Device.SetReport]) is a plain synchronous call. Reading input reports is
// not: IOKit delivers them to a CoreFoundation run loop, and a run loop belongs
// to one OS thread. [Stream] therefore pins the calling goroutine for its whole
// duration and calls the handler on that thread.
//
// One warning learned the hard way: [Device.SetReport] returning nil means the
// macOS HID stack accepted the write, NOT that the device acted on it. A device
// that does not implement the vendor protocol you are speaking will accept
// every report and answer nothing at all.
package hid

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-macos/iokit/ioreturn"
)

// ReportKind selects which of the three HID report channels a call addresses.
type ReportKind uint32

// The HID report kinds, matching IOKit's IOHIDReportType.
const (
	Input   ReportKind = 0
	Output  ReportKind = 1
	Feature ReportKind = 2
)

// String names the report kind.
func (k ReportKind) String() string {
	switch k {
	case Input:
		return "input"
	case Output:
		return "output"
	case Feature:
		return "feature"
	}
	return fmt.Sprintf("ReportKind(%d)", uint32(k))
}

// Errors reported by the package. They are stable and may be tested with
// errors.Is.
var (
	// ErrUnsupported is returned by every entry point on non-darwin platforms.
	ErrUnsupported = errors.New("hid: unsupported on this platform (darwin only)")
	// ErrNotOpen is returned when a report call is made on a device that has
	// not been opened, or has already been closed.
	ErrNotOpen = errors.New("hid: device not open")
	// ErrEmptyReport is returned by [Device.SetReport] for a zero-length
	// report, which IOKit would reject with an opaque code.
	ErrEmptyReport = errors.New("hid: empty report")
	// ErrNoDevices is returned by [Stream] when asked to stream from nothing,
	// which would otherwise block until the context expired for no reason.
	ErrNoDevices = errors.New("hid: no devices to stream")
)

// Common IOReturn codes, so a caller can react to the ones that mean something
// actionable rather than matching on hex. The values live in the shared
// ioreturn package, which is where every IOKit binding in this module gets
// them from; the names here stay local because what a code MEANS is
// domain-specific -- kIOReturnUnsupported on a HID device is a missing report
// channel, and on a USB device it is something else entirely.
const (
	ioReturnSuccess       = int32(ioreturn.Success)
	ioReturnNotPermitted  = int32(ioreturn.NotPermitted)
	ioReturnNoDevice      = int32(ioreturn.NoDevice)
	ioReturnExclusiveAcc  = int32(ioreturn.ExclusiveAccess)
	ioReturnUnsupportedOp = int32(ioreturn.Unsupported)
	// ioReturnInternalError stands in when the failure is on our side of the
	// boundary -- the frameworks would not load, so no IOReturn was ever
	// produced. It is kIOReturnError, IOKit's general-purpose failure.
	ioReturnInternalError = int32(ioreturn.Err)
)

// IOError wraps a non-zero IOKit IOReturn code from a named operation. IOReturn
// values are packed system/subsystem/code words, so hex is the only generally
// useful rendering; the few codes worth naming are named here.
type IOError struct {
	Op   string
	Code int32
}

// Error renders the operation and code, naming the code when it is one a caller
// can act on.
func (e *IOError) Error() string {
	name := ""
	switch e.Code {
	case ioReturnNotPermitted:
		name = " (not permitted: the device is claimed exclusively, or macOS input monitoring is required)"
	case ioReturnNoDevice:
		name = " (no device: it was unplugged)"
	case ioReturnExclusiveAcc:
		name = " (exclusive access: another process holds the device)"
	case ioReturnUnsupportedOp:
		name = " (unsupported: the device has no such report channel)"
	case ioReturnInternalError:
		name = " (internal: the IOKit frameworks could not be loaded)"
	}
	return fmt.Sprintf("hid: %s: IOReturn 0x%08x%s", e.Op, uint32(e.Code), name)
}

// ioErr returns nil for kIOReturnSuccess and an [IOError] otherwise, so call
// sites read as a single line.
func ioErr(op string, code int32) error {
	if code == ioReturnSuccess {
		return nil
	}
	return &IOError{Op: op, Code: code}
}

// ---------------------------------------------------------------------------
// Platform seams. The darwin build assigns the real IOKit implementations in an
// init(); every other platform assigns unsupported stubs. Keeping the portable
// logic above them lets this file be exercised without a Mac, and lets a test
// swap in fakes to reach the error branches.
// ---------------------------------------------------------------------------

var (
	// enumerate lists every HID device: parallel slices of description and
	// retained native reference.
	enumerate func() ([]Info, []uintptr, error)
	// openDev and closeDev open and close a native device, each returning an
	// IOReturn code.
	openDev  func(uintptr) int32
	closeDev func(uintptr) int32
	// setReport writes one report, returning an IOReturn code.
	setReport func(ref uintptr, kind uint32, id int64, data []byte) int32
	// stream schedules refs for input reports and blocks until ctx is done,
	// calling deliver(index, report) for each report that arrives.
	stream func(ctx context.Context, refs []uintptr, sizes []int, deliver func(int, []byte)) error
	// releaseRef drops our retain on a native device.
	releaseRef func(uintptr)
)

// Info describes an enumerated HID device. Every field is read from the IOKit
// registry at enumeration time; a field the device does not publish is left
// zero rather than reported as an error, because a HID device is free to omit
// any of them.
type Info struct {
	VendorID     uint16
	ProductID    uint16
	UsagePage    uint16
	Usage        uint16
	Product      string
	Manufacturer string
	SerialNumber string
	Transport    string
	// MaxInputReportSize and MaxOutputReportSize are the report sizes the
	// descriptor advertises. [Stream] uses the input size as its buffer size.
	MaxInputReportSize  int
	MaxOutputReportSize int
	// LocationID distinguishes two otherwise identical devices, and the several
	// interfaces of one physical device, on the USB topology.
	LocationID uint32
}

// String renders the device the way a probe listing reads: identifiers, usage
// pair, report sizes and whatever name the device gave.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%04x:%04x usage %#x/%#x in=%d out=%d",
		i.VendorID, i.ProductID, i.UsagePage, i.Usage,
		i.MaxInputReportSize, i.MaxOutputReportSize)
	if i.Product != "" {
		fmt.Fprintf(&b, " %q", i.Product)
	}
	if i.Manufacturer != "" {
		fmt.Fprintf(&b, " by %q", i.Manufacturer)
	}
	return b.String()
}

// Filter selects devices during enumeration. A zero Filter matches everything,
// and every field left zero (or empty) is a wildcard, so a caller narrows only
// on what it knows.
type Filter struct {
	// VendorID matches the USB vendor. Zero matches any vendor.
	VendorID uint16
	// ProductIDs matches any one of the listed products. Empty matches any
	// product -- the useful case for a vendor whose model range shares a
	// protocol, where enumerating every PID is a maintenance burden.
	ProductIDs []uint16
	// UsagePage matches the primary usage page, e.g. 0xFF00 for a
	// vendor-specific interface. Zero matches any page.
	UsagePage uint16
	// Usage matches the primary usage within the page. Zero matches any usage.
	Usage uint16
}

// Match reports whether i satisfies every constraint the filter sets.
func (f Filter) Match(i Info) bool {
	if f.VendorID != 0 && i.VendorID != f.VendorID {
		return false
	}
	if f.UsagePage != 0 && i.UsagePage != f.UsagePage {
		return false
	}
	if f.Usage != 0 && i.Usage != f.Usage {
		return false
	}
	if len(f.ProductIDs) > 0 {
		for _, p := range f.ProductIDs {
			if i.ProductID == p {
				return true
			}
		}
		return false
	}
	return true
}

// Device is an enumerated HID device. Enumeration retains the underlying IOKit
// object, so a Device stays valid -- and [Device.Open] may be called on it --
// after the [Devices] call that produced it has returned. [Device.Close]
// releases it.
type Device struct {
	info Info
	ref  uintptr
	open bool
}

// Info returns the device's enumeration-time description.
func (d *Device) Info() Info { return d.info }

// String renders the device as its Info does.
func (d *Device) String() string { return d.info.String() }

// Open takes a non-exclusive handle on the device. Opening is required before
// [Device.SetReport] or [Stream]. Opening an already-open device is a no-op.
//
// A vendor-specific device (usage page 0xFF00 and up) normally opens without
// any user consent. A device that publishes a keyboard or pointer usage is
// gated by macOS input monitoring, and Open then fails with an [IOError]
// carrying kIOReturnNotPermitted.
func (d *Device) Open() error {
	if d.open {
		return nil
	}
	if err := ioErr("IOHIDDeviceOpen", openDev(d.ref)); err != nil {
		return err
	}
	d.open = true
	return nil
}

// Close closes the handle and releases the IOKit object. The Device must not be
// used afterwards. Closing twice is a no-op.
func (d *Device) Close() error {
	if d.ref == 0 {
		return nil
	}
	var err error
	if d.open {
		err = ioErr("IOHIDDeviceClose", closeDev(d.ref))
		d.open = false
	}
	releaseRef(d.ref)
	d.ref = 0
	return err
}

// SetReport sends a report to the device. Most vendor protocols use [Output]
// with report ID 0.
//
// data is sent as given: it is NOT padded to the descriptor's report size.
// Devices differ on whether they require the full size, so padding is the
// caller's decision -- a protocol that needs it should build a full-size
// buffer.
//
// A nil return means macOS accepted the write. It is not evidence that the
// device understood it.
func (d *Device) SetReport(kind ReportKind, id byte, data []byte) error {
	if !d.open {
		return ErrNotOpen
	}
	if len(data) == 0 {
		return ErrEmptyReport
	}
	return ioErr("IOHIDDeviceSetReport", setReport(d.ref, uint32(kind), int64(id), data))
}

// Devices enumerates every HID device matching f. It opens nothing: the
// returned devices are retained handles the caller opens as needed and must
// [Device.Close] when done.
func Devices(f Filter) ([]*Device, error) {
	infos, refs, err := enumerate()
	if err != nil {
		return nil, err
	}
	var out []*Device
	for i, info := range infos {
		if f.Match(info) {
			out = append(out, &Device{info: info, ref: refs[i]})
			continue
		}
		releaseRef(refs[i]) // not ours to keep
	}
	return out, nil
}

// Stream delivers input reports from devs to fn until ctx is done, then returns
// ctx.Err().
//
// Three properties of this call follow from IOKit and are not incidental:
//
//   - It BLOCKS, and it pins the calling goroutine to its OS thread for the
//     whole call. IOKit delivers reports to the run loop of the thread that
//     scheduled the device; without pinning, the Go scheduler could move the
//     goroutine and the reports would arrive on a loop nobody is pumping. Run
//     it on its own goroutine.
//   - fn is called ON that thread, synchronously, between pumps. It must not
//     block for long -- a slow handler is back-pressure on the device. Hand the
//     data to a channel and return.
//   - The slice passed to fn is only valid for the duration of the call; it
//     aliases the buffer IOKit writes into. Copy whatever you keep.
//
// Every device in devs must already be open. Devices that are not open are
// reported as an error before anything is scheduled.
func Stream(ctx context.Context, fn func(*Device, []byte), devs ...*Device) error {
	if len(devs) == 0 {
		return ErrNoDevices
	}
	refs := make([]uintptr, len(devs))
	sizes := make([]int, len(devs))
	for i, d := range devs {
		if !d.open {
			return fmt.Errorf("%w: %s", ErrNotOpen, d.info)
		}
		refs[i] = d.ref
		sizes[i] = bufSize(d.info.MaxInputReportSize)
	}
	return stream(ctx, refs, sizes, func(i int, data []byte) {
		fn(devs[i], data)
	})
}

// defaultBufSize is the buffer used for a device whose descriptor does not
// advertise an input report size. 64 bytes is the USB full-speed interrupt
// endpoint maximum and the size every vendor HID protocol met so far uses.
const defaultBufSize = 64

// bufSize picks the read buffer for a device, falling back when the descriptor
// advertises nothing usable.
func bufSize(max int) int {
	if max <= 0 {
		return defaultBufSize
	}
	return max
}
