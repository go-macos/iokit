//go:build darwin

package usb

import (
	"time"
	"unsafe"

	"github.com/go-macos/iokit/ioreturn"
)

// IOUSBInterfaceInterface vtable indices, in 8-byte words. Measured, not
// counted, by the same method the device indices were: a C program calling
// offsetof() on the real IOUSBLib.h structures. The program printed identical
// offsets for every method used here across IOUSBInterfaceStruct182, 190, 500
// and 942, so whichever version QueryInterface hands back, these are right.
// Version 100 does not appear: the current SDK no longer declares it.
const (
	idxUSBInterfaceOpen      = 8
	idxUSBInterfaceClose     = 9
	idxGetInterfaceNumber    = 17
	idxGetNumEndpoints       = 19
	idxGetPipeProperties     = 26
	idxReadPipeTO            = 39
	idxWritePipeTO           = 40
	idxUSBInterfaceOpenSeize = 44
)

var (
	uuidInterfaceUserClientType = uuid{0xd411f39ec686972d, 0x612805270a0051ad} // kIOUSBInterfaceUserClientTypeID

	// interfaceInterfaces are tried newest first. All four share the vtable
	// prefix this file uses.
	interfaceInterfaces = []struct {
		name string
		id   uuid
	}{
		{"kIOUSBInterfaceInterfaceID942", uuid{0xae4b7bc03b665287, 0x5a9cab2f03228495}},
		{"kIOUSBInterfaceInterfaceID500", uuid{0xa74e93b0c3380d6c, 0x16acdd5dfb099b80}},
		{"kIOUSBInterfaceInterfaceID190", uuid{0xd611a6745584db8f, 0x8e60d3653000b197}},
		{"kIOUSBInterfaceInterfaceID182", uuid{0xd51196484cac2349, 0x861e80270a000892}},
	}
)

// interfaceClasses are the IOKit class names a USB interface may be registered
// under, newest first.
var interfaceClasses = []string{"IOUSBHostInterface", "IOUSBInterface"}

func init() {
	enumerateIfaces = darwinEnumerateIfaces
	openIface = darwinOpenIface
	closeIface = darwinCloseIface
	ifacePipes = darwinPipes
	pipeRead = darwinReadPipe
	pipeWrite = darwinWritePipe
	releaseIface = darwinRelease
}

// darwinEnumerateIfaces lists every USB interface on the machine. It matches on
// the interface service class rather than walking down from each device,
// because the registry already indexes it that way and the alternative needs
// the device interface's iterator vtable for no gain.
func darwinEnumerateIfaces() ([]InterfaceInfo, []uintptr, error) {
	if err := load(); err != nil {
		return nil, nil, err
	}
	for _, class := range interfaceClasses {
		matching := ioServiceMatching(class)
		if matching == 0 {
			continue
		}
		var iter uint32
		// IOServiceGetMatchingServices consumes the dictionary reference.
		if rc := ioServiceGetMatchingServices(0, matching, &iter); rc != 0 {
			continue
		}
		infos, refs := drainIfaceIterator(iter)
		ioObjectRelease(iter)
		if len(infos) > 0 {
			return infos, refs, nil
		}
	}
	return nil, nil, nil // no USB interfaces is not an error
}

// drainIfaceIterator turns an io_iterator_t into described, held interfaces.
func drainIfaceIterator(iter uint32) ([]InterfaceInfo, []uintptr) {
	var infos []InterfaceInfo
	var refs []uintptr
	for {
		svc := ioIteratorNext(iter)
		if svc == 0 {
			return infos, refs
		}
		infos = append(infos, describeIface(svc))
		refs = append(refs, registryAdd(&native{service: svc}))
	}
}

// describeIface reads an interface's registry properties. Every one is
// optional: a property the device omitted gives a zero field, not an error.
func describeIface(svc uint32) InterfaceInfo {
	vid, _ := propInt(svc, "idVendor")
	pid, _ := propInt(svc, "idProduct")
	num, _ := propInt(svc, "bInterfaceNumber")
	alt, _ := propInt(svc, "bAlternateSetting")
	cls, _ := propInt(svc, "bInterfaceClass")
	sub, _ := propInt(svc, "bInterfaceSubClass")
	proto, _ := propInt(svc, "bInterfaceProtocol")
	eps, _ := propInt(svc, "bNumEndpoints")
	loc, _ := propInt(svc, "locationID")
	return InterfaceInfo{
		VendorID:   uint16(vid),
		ProductID:  uint16(pid),
		Number:     uint8(num),
		Alt:        uint8(alt),
		Class:      uint8(cls),
		SubClass:   uint8(sub),
		Protocol:   uint8(proto),
		Endpoints:  uint8(eps),
		LocationID: uint32(loc),
	}
}

// attachIface creates the CFPlugIn for an interface service and queries it for
// an IOUSBInterfaceInterface. Like the device's attach, it does not open.
func attachIface(n *native) ioreturn.Code {
	if n.dev != nil {
		return ioreturn.Success
	}
	pluginType := cfUUIDCreateFromUUIDBytes(0, uuidInterfaceUserClientType.lo, uuidInterfaceUserClientType.hi)
	interfaceType := cfUUIDCreateFromUUIDBytes(0, uuidCFPlugInInterface.lo, uuidCFPlugInInterface.hi)
	defer cfRelease(pluginType)
	defer cfRelease(interfaceType)

	var plugin unsafe.Pointer
	var score int32
	if rc := ioCreatePlugInInterface(n.service, pluginType, interfaceType, &plugin, &score); rc != 0 {
		return ioreturn.Code(rc)
	}
	if plugin == nil {
		return ioreturn.NoResources
	}
	defer vcall(plugin, idxRelease)

	slot := cMalloc(uint64(unsafe.Sizeof(uintptr(0))))
	if slot == nil {
		return ioreturn.NoMemory
	}
	defer cFree(slot)

	last := ioreturn.Unsupported
	for _, cand := range interfaceInterfaces {
		*(*uintptr)(slot) = 0
		hr := vcall(plugin, idxQueryInterface, uintptr(cand.id.lo), uintptr(cand.id.hi), uintptr(slot))
		iface := *(*unsafe.Pointer)(slot)
		if hr == 0 && iface != nil {
			n.dev = iface
			// Every version this file accepts has the timeout variants; 182 is
			// the oldest, and that is where they arrived.
			n.hasTO = true
			return ioreturn.Success
		}
		last = ioreturn.Code(hr)
	}
	return last
}

// darwinOpenIface attaches if needed, then claims the interface.
func darwinOpenIface(tok uintptr, seize bool) ioreturn.Code {
	if err := load(); err != nil {
		return ioreturn.Err
	}
	n := registryGet(tok)
	if n == nil {
		return ioreturn.NoDevice
	}
	if rc := attachIface(n); !ioreturn.IsSuccess(rc) {
		return rc
	}
	idx := idxUSBInterfaceOpen
	if seize {
		idx = idxUSBInterfaceOpenSeize
	}
	return ioreturn.Code(vcall(n.dev, idx))
}

// darwinCloseIface gives the claim back but keeps the interface object, so a
// caller may reopen without re-enumerating.
func darwinCloseIface(tok uintptr) ioreturn.Code {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return ioreturn.NotOpen
	}
	return ioreturn.Code(vcall(n.dev, idxUSBInterfaceClose))
}

// darwinPipes reads the properties of every data pipe.
//
// Pipe refs run 1..GetNumEndpoints; ref 0 is the interface's control pipe and
// is not a data endpoint, which is the off-by-one this loop exists to get
// right. Every out-parameter is C memory, because purego passes uintptr
// arguments the collector cannot see.
func darwinPipes(tok uintptr) ([]Pipe, ioreturn.Code) {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return nil, ioreturn.NotOpen
	}
	// One block holds all six out-parameters: five bytes and one uint16,
	// laid out with the uint16 first so it is naturally aligned.
	const (
		offMaxPacket = 0 // uint16
		offDir       = 2
		offNumber    = 3
		offType      = 4
		offInterval  = 5
		offCount     = 6
		blockSize    = 8
	)
	block := cMalloc(blockSize)
	if block == nil {
		return nil, ioreturn.NoMemory
	}
	defer cFree(block)
	b := unsafe.Slice((*byte)(block), blockSize)
	clear(b)

	if rc := vcall(n.dev, idxGetNumEndpoints, uintptr(unsafe.Add(block, offCount))); rc != 0 {
		return nil, ioreturn.Code(rc)
	}
	count := b[offCount]

	out := make([]Pipe, 0, count)
	for ref := uint8(1); ref <= count; ref++ {
		clear(b[:offCount])
		rc := vcall(n.dev, idxGetPipeProperties,
			uintptr(ref),
			uintptr(unsafe.Add(block, offDir)),
			uintptr(unsafe.Add(block, offNumber)),
			uintptr(unsafe.Add(block, offType)),
			uintptr(block), // maxPacketSize, UInt16*
			uintptr(unsafe.Add(block, offInterval)),
		)
		if rc != 0 {
			return out, ioreturn.Code(rc)
		}
		out = append(out, Pipe{
			Ref:       ref,
			Number:    b[offNumber],
			Dir:       PipeDir(b[offDir]),
			Type:      TransferType(b[offType]),
			MaxPacket: u16(b[offMaxPacket:]),
			Interval:  b[offInterval],
		})
	}
	return out, ioreturn.Success
}

// darwinReadPipe reads with both timeouts set, so a device that NAKs forever
// produces kIOUSBTransactionTimeout instead of a wedged call.
//
// noDataTimeout bounds the wait for the first byte and completionTimeout the
// whole transfer; giving them the same value makes "nothing arrived in N
// milliseconds" the single question being asked.
func darwinReadPipe(tok uintptr, ref uint8, buf []byte, timeout time.Duration) (int, ioreturn.Code) {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return 0, ioreturn.NotOpen
	}
	if len(buf) == 0 {
		return 0, ioreturn.BadArgument
	}
	data := cMalloc(uint64(len(buf)))
	if data == nil {
		return 0, ioreturn.NoMemory
	}
	defer cFree(data)
	clear(unsafe.Slice((*byte)(data), len(buf)))

	size := cMalloc(4)
	if size == nil {
		return 0, ioreturn.NoMemory
	}
	defer cFree(size)
	*(*uint32)(size) = uint32(len(buf))

	ms := timeoutMS(timeout)
	rc := vcall(n.dev, idxReadPipeTO, uintptr(ref), uintptr(data), uintptr(size), uintptr(ms), uintptr(ms))
	if rc != 0 {
		// The size word is an in-out parameter, and IOUSBLib leaves it alone
		// when the transfer never completed. Measured on a device that NAKed
		// every IN token: the call came back kIOUSBTransactionTimeout with the
		// word still holding the buffer length it was given, and the buffer
		// itself overwritten with kernel scratch -- a poison fill put in
		// beforehand was gone. Reporting that word as a byte count invents
		// half a kilobyte of data out of a device that said nothing, which is
		// the single most expensive mistake this package could make. On any
		// non-success code the count is therefore zero.
		return 0, ioreturn.Code(rc)
	}
	got := int(*(*uint32)(size))
	if got > len(buf) {
		got = len(buf)
	}
	if got > 0 {
		copy(buf, unsafe.Slice((*byte)(data), got))
	}
	return got, ioreturn.Success
}

// darwinWritePipe writes with both timeouts set.
func darwinWritePipe(tok uintptr, ref uint8, buf []byte, timeout time.Duration) (int, ioreturn.Code) {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return 0, ioreturn.NotOpen
	}
	var data unsafe.Pointer
	if len(buf) > 0 {
		data = cMalloc(uint64(len(buf)))
		if data == nil {
			return 0, ioreturn.NoMemory
		}
		defer cFree(data)
		copy(unsafe.Slice((*byte)(data), len(buf)), buf)
	}
	ms := timeoutMS(timeout)
	rc := vcall(n.dev, idxWritePipeTO, uintptr(ref), uintptr(data), uintptr(len(buf)), uintptr(ms), uintptr(ms))
	if rc != 0 {
		return 0, ioreturn.Code(rc)
	}
	return len(buf), ioreturn.Success
}

// timeoutMS clamps a duration to the milliseconds IOUSBLib takes. Zero would
// mean "no timeout" to IOKit -- an unbounded wait on a device that may never
// answer -- so it becomes one millisecond instead.
func timeoutMS(d time.Duration) uint32 {
	ms := d.Milliseconds()
	if ms <= 0 {
		return 1
	}
	if ms > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(ms)
}
