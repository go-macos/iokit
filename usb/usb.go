// Package usb performs USB control transfers on macOS through IOKit, with no
// cgo.
//
// It exists because a HID interface is not always where a device's interesting
// protocol lives. Recent XR glasses, in particular, publish HID interfaces that
// accept every report and answer none: the real channel is a vendor control
// request on endpoint 0. Reaching endpoint 0 means the IOUSBLib device
// interface, which this package binds through
// github.com/ebitengine/purego, so a consumer builds with CGO_ENABLED=0.
//
// # What macOS lets you do
//
// A USB device and its interfaces are claimed separately. A composite device
// whose interfaces are all taken by kernel drivers -- an audio device, a HID
// device -- may still allow a device-level open, because the drivers claimed
// interfaces and not the device. It may equally refuse: whether
// [Device.Open] succeeds is a property of the device's kernel driver stack,
// not of this package, and the refusal arrives as an [IOError] carrying
// kIOReturnExclusiveAccess or kIOReturnNotPermitted. [Device.OpenSeize] asks
// IOKit to take the device from whoever holds it, which is the documented
// escape hatch and still fails when the holder is privileged.
//
// [Device.ConfigDescriptor] is the exception worth knowing: it reads a
// descriptor the kernel already cached at enumeration, so it works on a device
// that was never opened, and it is the safest first question to ask about
// hardware whose protocol is unknown.
//
// # What a successful call proves
//
// The lesson the sibling hid package records applies here with a sharper edge.
// A HID SetReport that returns success proves only that macOS accepted the
// write. A control transfer is better: the USB protocol acknowledges at the bus
// level, so kIOReturnSuccess on an OUT transfer means the device's controller
// ACKed the packet, and kIOUSBPipeStalled means it actively refused. Those two
// are real evidence. What still is not evidence is a device that answers a
// vendor IN request with all zeroes; check the byte count, not the error.
package usb

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

// Direction is the transfer direction bit of bmRequestType, bit 7.
type Direction uint8

// The two transfer directions. USB names them from the host's point of view.
const (
	// Out sends data from the host to the device.
	Out Direction = 0x00
	// In reads data from the device to the host.
	In Direction = 0x80
)

// String names the direction.
func (d Direction) String() string {
	if d == In {
		return "in"
	}
	return "out"
}

// Type is the request-type field of bmRequestType, bits 6..5.
type Type uint8

// The request types. Standard requests are defined by the USB specification
// itself; Vendor requests are whatever the manufacturer decided, which is
// exactly what a reverse-engineering probe is looking for.
const (
	Standard Type = 0x00
	Class    Type = 0x20
	Vendor   Type = 0x40
)

// String names the request type.
func (t Type) String() string {
	switch t {
	case Standard:
		return "standard"
	case Class:
		return "class"
	case Vendor:
		return "vendor"
	}
	return fmt.Sprintf("Type(%#02x)", uint8(t))
}

// Recipient is the recipient field of bmRequestType, bits 4..0.
type Recipient uint8

// The request recipients.
const (
	ToDevice    Recipient = 0
	ToInterface Recipient = 1
	ToEndpoint  Recipient = 2
	ToOther     Recipient = 3
)

// String names the recipient.
func (r Recipient) String() string {
	switch r {
	case ToDevice:
		return "device"
	case ToInterface:
		return "interface"
	case ToEndpoint:
		return "endpoint"
	case ToOther:
		return "other"
	}
	return fmt.Sprintf("Recipient(%d)", uint8(r))
}

// Standard request codes and descriptor types, from the USB specification.
// Only the ones a probe needs are named.
const (
	// ReqGetDescriptor is standard request 6, the one every USB device must
	// implement. It is therefore the control transfer to use when the question
	// is "does my binding work at all".
	ReqGetDescriptor byte = 6

	DescDevice        byte = 1
	DescConfiguration byte = 2
	DescString        byte = 3
)

// Setup is a USB control request's 8-byte setup packet, minus the length,
// which [Device.Control] takes from the data slice.
type Setup struct {
	Direction Direction
	Type      Type
	Recipient Recipient
	// Request is bRequest. Its meaning depends on Type: for [Standard] it is a
	// specification-defined code such as [ReqGetDescriptor]; for [Vendor] it is
	// whatever the manufacturer chose.
	Request byte
	Value   uint16
	Index   uint16
}

// RequestType assembles bmRequestType from the three fields that make it up.
func (s Setup) RequestType() byte {
	return byte(s.Direction) | byte(s.Type) | byte(s.Recipient)
}

// String renders the setup packet the way a probe log wants to read it.
func (s Setup) String() string {
	return fmt.Sprintf("bmRequestType=%#02x (%s %s %s) bRequest=%#02x wValue=%#04x wIndex=%#04x",
		s.RequestType(), s.Direction, s.Type, s.Recipient, s.Request, s.Value, s.Index)
}

// GetDescriptor builds the standard GET_DESCRIPTOR setup packet. kind is a
// Desc... constant, index selects among descriptors of that kind, and lang is
// the language ID, which is zero for everything except string descriptors.
func GetDescriptor(kind, index byte, lang uint16) Setup {
	return Setup{
		Direction: In,
		Type:      Standard,
		Recipient: ToDevice,
		Request:   ReqGetDescriptor,
		Value:     uint16(kind)<<8 | uint16(index),
		Index:     lang,
	}
}

// Errors reported by the package. They are stable and may be tested with
// errors.Is.
var (
	// ErrUnsupported is returned by every entry point on non-darwin platforms.
	ErrUnsupported = errors.New("usb: unsupported on this platform (darwin only)")
	// ErrNotOpen is returned by [Device.Control] on a device that has not been
	// opened, or has already been closed.
	ErrNotOpen = errors.New("usb: device not open")
	// ErrTooLong is returned when a transfer's data exceeds the 65535 bytes a
	// setup packet's wLength can express.
	ErrTooLong = errors.New("usb: transfer longer than wLength can express")
	// ErrShortDescriptor is returned by [Device.ConfigDescriptor] when the
	// device's cached descriptor is too short to carry its own length, which
	// means the kernel handed back something that is not a descriptor.
	ErrShortDescriptor = errors.New("usb: descriptor shorter than its own header")
	// ErrReleased is returned by an [Interface] method called after Close.
	ErrReleased = errors.New("usb: interface already released")
)

// maxTransfer is the largest wLength a setup packet can express.
const maxTransfer = 0xFFFF

// IOError wraps a non-zero IOReturn code from a named operation, decoded
// through the shared ioreturn table.
type IOError struct {
	Op   string
	Code ioreturn.Code
}

// Error renders the operation and the decoded code.
func (e *IOError) Error() string {
	return fmt.Sprintf("usb: %s: %s", e.Op, e.Code)
}

// Stalled reports whether the device refused the request outright. A STALL is
// the most informative failure a probe can get: the device received the setup
// packet, understood the framing, and declined. A vendor request that stalls is
// a request this device does not implement -- and one that does NOT stall is a
// lead.
func (e *IOError) Stalled() bool { return e.Code == ioreturn.USBPipeStalled }

// ioErr returns nil for kIOReturnSuccess and an [IOError] otherwise.
func ioErr(op string, code ioreturn.Code) error {
	if ioreturn.IsSuccess(code) {
		return nil
	}
	return &IOError{Op: op, Code: code}
}

// ---------------------------------------------------------------------------
// Platform seams, in the shape the sibling hid package established: the darwin
// build wires IOUSBLib into these in an init(), every other platform wires
// unsupported stubs, and the portable logic above is exercised without a Mac.
// ---------------------------------------------------------------------------

var (
	// enumerate lists every USB device: parallel slices of description and
	// native handle.
	enumerate func() ([]Info, []uintptr, error)
	// openDev opens a device, seizing it from its current owner when seize is
	// set, and returns an IOReturn code.
	openDev func(ref uintptr, seize bool) ioreturn.Code
	// closeDev closes an open device.
	closeDev func(ref uintptr) ioreturn.Code
	// control runs one control transfer on endpoint 0, returning the number of
	// bytes actually transferred and an IOReturn code.
	control func(ref uintptr, s Setup, data []byte, timeout time.Duration) (int, ioreturn.Code)
	// configDesc reads a cached configuration descriptor, which needs no open.
	configDesc func(ref uintptr, index byte) ([]byte, ioreturn.Code)
	// releaseRef drops our hold on a native handle.
	releaseRef func(uintptr)
)

// Info describes an enumerated USB device, read from the IOKit registry at
// enumeration time. A field the device does not publish is left zero rather
// than reported as an error.
type Info struct {
	VendorID  uint16
	ProductID uint16
	// Class, SubClass and Protocol are the device descriptor's triple. A
	// composite device -- one whose interfaces have unrelated classes, which
	// is what XR glasses are -- reports class 0.
	Class    uint8
	SubClass uint8
	Protocol uint8
	// Release is bcdDevice, the device's own version number.
	Release      uint16
	Product      string
	Manufacturer string
	SerialNumber string
	// LocationID is the device's position on the USB topology. It is the only
	// field that distinguishes two identical devices, and it is stable for as
	// long as the device stays in the same port.
	LocationID uint32
	// Speed is the IOKit device speed index: 0 low, 1 full, 2 high, 3 super,
	// 4 super+. It is reported as read, without interpretation.
	Speed uint8
}

// String renders the device the way a probe listing reads.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%04x:%04x loc=%#08x class=%d/%d/%d", i.VendorID, i.ProductID,
		i.LocationID, i.Class, i.SubClass, i.Protocol)
	if i.Product != "" {
		fmt.Fprintf(&b, " %q", i.Product)
	}
	if i.Manufacturer != "" {
		fmt.Fprintf(&b, " by %q", i.Manufacturer)
	}
	return b.String()
}

// Filter selects devices during enumeration. A zero Filter matches everything,
// and every field left zero is a wildcard.
type Filter struct {
	// VendorID matches the USB vendor. Zero matches any vendor.
	VendorID uint16
	// ProductIDs matches any one of the listed products. Empty matches any
	// product.
	ProductIDs []uint16
	// LocationID matches one position on the USB topology, which is how a
	// caller pins one of several identical devices. Zero matches any position.
	LocationID uint32
}

// Match reports whether i satisfies every constraint the filter sets.
func (f Filter) Match(i Info) bool {
	if f.VendorID != 0 && i.VendorID != f.VendorID {
		return false
	}
	if f.LocationID != 0 && i.LocationID != f.LocationID {
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

// Device is an enumerated USB device. Enumeration takes a hold on the
// underlying IOKit object, so a Device stays valid after the [Devices] call
// that produced it has returned; [Device.Close] gives it back.
type Device struct {
	info   Info
	ref    uintptr
	open   bool
	seized bool
}

// Info returns the device's enumeration-time description.
func (d *Device) Info() Info { return d.info }

// String renders the device as its Info does.
func (d *Device) String() string { return d.info.String() }

// Seized reports whether the open handle was taken from another owner by
// [Device.OpenSeize] rather than granted by [Device.Open]. Worth logging: a
// seized device is one whose kernel driver was pushed aside, and the driver may
// take it back.
func (d *Device) Seized() bool { return d.seized }

// Open takes a device-level handle. It must succeed before [Device.Control]
// will do anything.
//
// Opening an already-open device is a no-op. Opening fails with an [IOError]
// carrying kIOReturnExclusiveAccess when a kernel driver holds the device
// itself -- as opposed to holding its interfaces, which does not conflict.
func (d *Device) Open() error { return d.openWith(false) }

// OpenSeize takes the device away from its current owner. This is IOKit's
// documented escape hatch for a device a kernel driver already holds, and it is
// not gentle: the previous owner loses the device. It still fails, with
// kIOReturnNotPermitted or kIOReturnExclusiveAccess, when the owner is one
// macOS will not displace.
func (d *Device) OpenSeize() error { return d.openWith(true) }

// openWith is the body of both open entry points.
func (d *Device) openWith(seize bool) error {
	if d.open {
		return nil
	}
	op := "USBDeviceOpen"
	if seize {
		op = "USBDeviceOpenSeize"
	}
	if err := ioErr(op, openDev(d.ref, seize)); err != nil {
		return err
	}
	d.open = true
	d.seized = seize
	return nil
}

// Close closes the handle and gives back the IOKit object. The Device must not
// be used afterwards. Closing twice is a no-op.
func (d *Device) Close() error {
	if d.ref == 0 {
		return nil
	}
	var err error
	if d.open {
		err = ioErr("USBDeviceClose", closeDev(d.ref))
		d.open = false
		d.seized = false
	}
	releaseRef(d.ref)
	d.ref = 0
	return err
}

// defaultTimeout bounds a control transfer that the device never answers. USB
// gives no lower bound on how long a device may take, and a probe that hangs
// forever on the first unanswered vendor request is a probe that reports
// nothing, so every transfer gets a deadline whether the caller set one or not.
const defaultTimeout = 2 * time.Second

// Control runs one control transfer on endpoint 0.
//
// data is the payload for an [Out] transfer and the buffer to fill for an [In]
// one; its length becomes wLength. A zero-length data is a legitimate control
// transfer -- many vendor commands carry everything in wValue and wIndex.
//
// The return is the number of bytes actually transferred, which for an [In]
// transfer is what the device chose to send and may be less than len(data). A
// nil error means the device's controller acknowledged the transfer at the bus
// level.
//
// timeout bounds the transfer; zero or negative means [defaultTimeout]. A
// device that never answers fails with kIOUSBTransactionTimeout, which is a
// different and much more interesting fact than a device that STALLs -- see
// [IOError.Stalled].
func (d *Device) Control(s Setup, data []byte, timeout time.Duration) (int, error) {
	if !d.open {
		return 0, ErrNotOpen
	}
	if len(data) > maxTransfer {
		return 0, fmt.Errorf("%w: %d bytes", ErrTooLong, len(data))
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	n, code := control(d.ref, s, data, timeout)
	// A partial transfer is still a transfer: report what arrived alongside the
	// error, because for a probe the byte count is the finding.
	if n < 0 {
		n = 0
	}
	if n > len(data) {
		n = len(data)
	}
	return n, ioErr("DeviceRequestTO", code)
}

// Descriptor reads a standard descriptor with a GET_DESCRIPTOR control
// transfer, returning exactly the bytes the device sent. max bounds the read.
//
// It requires an open device. For a configuration descriptor on a device that
// will not open, use [Device.ConfigDescriptor] instead.
func (d *Device) Descriptor(kind, index byte, lang uint16, max int) ([]byte, error) {
	buf := make([]byte, max)
	n, err := d.Control(GetDescriptor(kind, index, lang), buf, 0)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// ConfigDescriptor returns the index'th configuration descriptor, together with
// every interface and endpoint descriptor that follows it, as the device
// published them.
//
// It reads what the kernel cached when it enumerated the device, so it needs no
// open and cannot be refused by a driver holding the device. That makes it the
// first thing to ask of hardware whose protocol is unknown: it names every
// interface class and every endpoint, including any endpoint no driver claimed.
func (d *Device) ConfigDescriptor(index byte) ([]byte, error) {
	b, code := configDesc(d.ref, index)
	if err := ioErr("GetConfigurationDescriptorPtr", code); err != nil {
		return nil, err
	}
	if len(b) < 4 {
		return nil, fmt.Errorf("%w: %d bytes", ErrShortDescriptor, len(b))
	}
	return b, nil
}

// Devices enumerates every USB device matching f. It opens nothing: the
// returned devices are handles the caller opens as needed and must
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
