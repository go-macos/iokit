package usb

import (
	"fmt"
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

// This file adds the second half of USB access: an interface and its pipes.
//
// [Device] reaches endpoint 0, which every device has and no driver can refuse.
// An interface is the opposite: it is exactly what kernel drivers claim, so
// opening one is a negotiation with whatever already owns it, and the refusal
// code is the finding. A CDC-ACM function whose data interface AppleUSBACMData
// holds is reachable only by taking it, and knowing whether macOS will let you
// is worth more than guessing.

// PipeDir is a pipe's direction as IOUSBLib reports it. These are the kUSB*
// endpoint-direction values, which are small ordinals and not the 0x80 bit that
// [Direction] uses in bmRequestType.
type PipeDir uint8

// The pipe directions.
const (
	PipeOut PipeDir = 0
	PipeIn  PipeDir = 1
	PipeAny PipeDir = 3
)

// String names the direction.
func (d PipeDir) String() string {
	switch d {
	case PipeOut:
		return "out"
	case PipeIn:
		return "in"
	case PipeAny:
		return "any"
	default:
		return fmt.Sprintf("dir(%d)", uint8(d))
	}
}

// The kUSB* transfer-type values IOUSBLib reports for a pipe are the same
// small ordinals the endpoint descriptor carries in bmAttributes, so a pipe
// reuses [TransferType] rather than declaring a parallel enumeration.

// Pipe describes one endpoint of an open interface.
//
// Ref is the number IOUSBLib addresses the pipe by, and it is not the endpoint
// address: pipes are numbered 1..N in descriptor order, with 0 reserved for the
// interface's control pipe. [InterfaceHandle.Pipe] exists so a caller can keep
// thinking in endpoint addresses, which is what a descriptor dump shows.
type Pipe struct {
	Ref       uint8
	Number    uint8
	Dir       PipeDir
	Type      TransferType
	MaxPacket uint16
	Interval  uint8
}

// Address is the endpoint address as a configuration descriptor spells it:
// the endpoint number with bit 7 set for an IN endpoint.
func (p Pipe) Address() byte {
	a := p.Number & 0x0F
	if p.Dir == PipeIn {
		a |= 0x80
	}
	return a
}

// String renders the pipe the way a descriptor dump does, plus the pipe ref,
// which is what the read and write calls actually take.
func (p Pipe) String() string {
	return fmt.Sprintf("pipe %d: ep %#02x %s %s max=%d interval=%d",
		p.Ref, p.Address(), p.Dir, p.Type, p.MaxPacket, p.Interval)
}

// InterfaceInfo describes an interface as the IOKit registry has it, without
// opening anything.
type InterfaceInfo struct {
	VendorID  uint16
	ProductID uint16
	// Number is bInterfaceNumber and Alt is bAlternateSetting.
	Number uint8
	Alt    uint8
	// Class, SubClass and Protocol are the interface descriptor's triple.
	Class    uint8
	SubClass uint8
	Protocol uint8
	// Endpoints is bNumEndpoints, which counts the data pipes only.
	Endpoints uint8
	// LocationID identifies the parent device's position on the bus, so
	// interfaces of the same device can be told from those of its twin.
	LocationID uint32
}

// String renders the interface for a probe log.
func (i InterfaceInfo) String() string {
	return fmt.Sprintf("%04x:%04x loc=%#08x if%d alt%d class=%d/%d/%d %d endpoint(s)",
		i.VendorID, i.ProductID, i.LocationID, i.Number, i.Alt, i.Class, i.SubClass, i.Protocol, i.Endpoints)
}

// InterfaceFilter narrows [Interfaces]. A zero field matches everything.
type InterfaceFilter struct {
	VendorID   uint16
	ProductIDs []uint16
	LocationID uint32
	// Numbers restricts to these bInterfaceNumber values.
	Numbers []uint8
}

// Match reports whether i passes the filter.
func (f InterfaceFilter) Match(i InterfaceInfo) bool {
	if f.VendorID != 0 && i.VendorID != f.VendorID {
		return false
	}
	if f.LocationID != 0 && i.LocationID != f.LocationID {
		return false
	}
	if len(f.ProductIDs) > 0 && !containsU16(f.ProductIDs, i.ProductID) {
		return false
	}
	if len(f.Numbers) > 0 && !containsU8(f.Numbers, i.Number) {
		return false
	}
	return true
}

func containsU16(s []uint16, v uint16) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func containsU8(s []uint8, v uint8) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Seams for the interface half, filled in by the platform files.
var (
	enumerateIfaces func() ([]InterfaceInfo, []uintptr, error)
	openIface       func(tok uintptr, seize bool) ioreturn.Code
	closeIface      func(tok uintptr) ioreturn.Code
	ifacePipes      func(tok uintptr) ([]Pipe, ioreturn.Code)
	pipeRead        func(tok uintptr, ref uint8, buf []byte, timeout time.Duration) (int, ioreturn.Code)
	pipeWrite       func(tok uintptr, ref uint8, buf []byte, timeout time.Duration) (int, ioreturn.Code)
	releaseIface    func(tok uintptr)
)

// InterfaceHandle is one USB interface of one device.
type InterfaceHandle struct {
	ref    uintptr
	info   InterfaceInfo
	opened bool
	seized bool
}

// Interfaces lists the interfaces matching f. Every returned interface holds a
// kernel reference and must be closed.
func Interfaces(f InterfaceFilter) ([]*InterfaceHandle, error) {
	infos, refs, err := enumerateIfaces()
	if err != nil {
		return nil, err
	}
	out := make([]*InterfaceHandle, 0, len(infos))
	for k, info := range infos {
		if !f.Match(info) {
			releaseIface(refs[k])
			continue
		}
		out = append(out, &InterfaceHandle{ref: refs[k], info: info})
	}
	return out, nil
}

// Info describes the interface.
func (i *InterfaceHandle) Info() InterfaceInfo { return i.info }

// String renders the interface.
func (i *InterfaceHandle) String() string { return i.info.String() }

// Seized reports whether the handle was taken from a previous owner.
func (i *InterfaceHandle) Seized() bool { return i.seized }

// Open claims the interface.
//
// This is where a kernel driver says no. An interface bound to
// AppleUSBACMData, AppleUSBAudio or the HID family is already owned, and the
// refusal arrives as an [IOError] carrying kIOReturnExclusiveAccess. That code
// is a result, not a bug: it says the protocol is reachable but spoken for.
func (i *InterfaceHandle) Open() error { return i.openWith(false) }

// OpenSeize takes the interface from whoever holds it. Everything that driver
// was doing stops -- for a CDC data interface, that means the /dev/cu.* node
// stops carrying bytes until the handle is closed.
func (i *InterfaceHandle) OpenSeize() error { return i.openWith(true) }

func (i *InterfaceHandle) openWith(seize bool) error {
	if i.ref == 0 {
		return ErrReleased
	}
	op := "USBInterfaceOpen"
	if seize {
		op = "USBInterfaceOpenSeize"
	}
	if err := ioErr(op, openIface(i.ref, seize)); err != nil {
		return err
	}
	i.opened, i.seized = true, seize
	return nil
}

// Close releases the interface handle and the kernel reference.
func (i *InterfaceHandle) Close() error {
	if i.ref == 0 {
		return nil
	}
	var err error
	if i.opened {
		err = ioErr("USBInterfaceClose", closeIface(i.ref))
		i.opened = false
	}
	releaseIface(i.ref)
	i.ref = 0
	return err
}

// Pipes lists the interface's endpoints. It needs an open interface: pipe
// properties come from the handle, not from the registry.
func (i *InterfaceHandle) Pipes() ([]Pipe, error) {
	if i.ref == 0 {
		return nil, ErrReleased
	}
	ps, code := ifacePipes(i.ref)
	if err := ioErr("GetPipeProperties", code); err != nil {
		return nil, err
	}
	return ps, nil
}

// Pipe finds the pipe with the given endpoint address, as a descriptor dump
// spells it -- 0x83 for IN endpoint 3.
func (i *InterfaceHandle) Pipe(address byte) (Pipe, error) {
	ps, err := i.Pipes()
	if err != nil {
		return Pipe{}, err
	}
	for _, p := range ps {
		if p.Address() == address {
			return p, nil
		}
	}
	return Pipe{}, fmt.Errorf("usb: no endpoint %#02x on interface %d", address, i.info.Number)
}

// Read reads from a pipe, identified by its ref from [InterfaceHandle.Pipes].
//
// A read that returns kIOUSBTransactionTimeout means the device NAKed for the
// whole timeout: it was asked and did not answer. That is a different finding
// from a stall, and both are different from a short read, so the byte count is
// returned even when the error is not nil.
func (i *InterfaceHandle) Read(ref uint8, buf []byte, timeout time.Duration) (int, error) {
	if i.ref == 0 {
		return 0, ErrReleased
	}
	n, code := pipeRead(i.ref, ref, buf, timeout)
	return n, ioErr("ReadPipeTO", code)
}

// Write writes to a pipe. Success means the device's controller ACKed the
// transfer at the bus level, which is real evidence the bytes arrived -- and
// still no evidence at all that the firmware understood them.
func (i *InterfaceHandle) Write(ref uint8, buf []byte, timeout time.Duration) (int, error) {
	if i.ref == 0 {
		return 0, ErrReleased
	}
	n, code := pipeWrite(i.ref, ref, buf, timeout)
	return n, ioErr("WritePipeTO", code)
}
