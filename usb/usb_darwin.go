//go:build darwin

package usb

// IOUSBLib is not a flat C library. It is a CFPlugIn: IOKit hands back a
// pointer to a pointer to a vtable of function pointers, COM-style, and every
// call goes through an index into that vtable. purego cannot describe that
// shape, so this file does it by hand:
//
//   - the flat IOKit and CoreFoundation entry points are bound normally with
//     purego.RegisterLibFunc, which keeps their pointer arguments alive for
//     the duration of the call;
//   - the vtable methods are dispatched through purego.SyscallN against a
//     function pointer read out of the vtable at a fixed index.
//
// Those indices are the load-bearing constants of this file. They were not
// counted by eye off a header: a C program that includes <IOKit/usb/IOUSBLib.h>
// printed offsetof() for every method used here, and they are reproduced in
// TestVTableIndices' comment. Counting by eye gets it wrong -- DeviceRequest
// sits at 26, not at the 24 an eyeball count of the documented method list
// suggests, because two methods in the middle are undocumented.
//
// Every out-parameter handed to a vtable method is C memory from malloc, never
// a Go pointer. purego.SyscallN takes uintptr arguments, so the Go compiler and
// garbage collector cannot see a Go pointer passed that way, and a moving stack
// would corrupt it silently.

import (
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/go-macos/iokit/ioreturn"
)

const (
	frameworkIOKit          = "/System/Library/Frameworks/IOKit.framework/IOKit"
	frameworkCoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	libSystem               = "/usr/lib/libSystem.B.dylib"
)

const (
	kCFStringEncodingUTF8 = 0x08000100
	kCFNumberSInt64Type   = 4
)

// IUnknown and IOUSBDeviceInterface vtable indices, in 8-byte words. Measured,
// not counted: see the file comment.
const (
	idxQueryInterface = 1
	idxRelease        = 3

	idxUSBDeviceOpen      = 8
	idxUSBDeviceClose     = 9
	idxGetConfigDescPtr   = 21
	idxDeviceRequest      = 26
	idxUSBDeviceOpenSeize = 29
	idxDeviceRequestTO    = 30
)

// CFUUIDBytes is sixteen bytes passed by value. On both arm64 and amd64 a
// 16-byte all-integer struct travels in two general-purpose registers holding
// the raw bytes as little-endian words, so a CFUUIDBytes argument is exactly
// two uintptr arguments. These words were printed by a C program calling
// CFUUIDGetUUIDBytes on the real constants.
type uuid struct{ lo, hi uint64 }

var (
	uuidDeviceUserClientType = uuid{0xd411c09e80b7c79d, 0x612805270a004fa5} // kIOUSBDeviceUserClientTypeID
	uuidCFPlugInInterface    = uuid{0xd4119c1058e844c2, 0x6f42c6e45000d491} // kIOCFPlugInInterfaceID

	// deviceInterfaces are tried newest first. The three listed share one
	// vtable prefix -- the C program printed identical offsets for every method
	// this file calls across 100, 182 and 500 -- so whichever we get, the
	// indices above are right. Only 100 lacks the two "TO" methods, which
	// hasTimeouts records.
	deviceInterfaces = []struct {
		name        string
		id          uuid
		hasTimeouts bool
	}{
		{"kIOUSBDeviceInterfaceID500", uuid{0xe2485b4b47f03ca3, 0x3be1eafc07027db5}, true},
		{"kIOUSBDeviceInterfaceID182", uuid{0xd511914896c42f15, 0x861e80270a00529d}, true},
		{"kIOUSBDeviceInterfaceID100", uuid{0xd411f39ed087815c, 0x612805270a00458b}, false},
	}
)

var (
	cfRelease                 func(uintptr)
	cfStringCreateWithCString func(alloc uintptr, s string, enc uint32) uintptr
	cfStringGetCString        func(str uintptr, buf unsafe.Pointer, size int64, enc uint32) bool
	cfNumberGetValue          func(num uintptr, theType int32, valuePtr unsafe.Pointer) bool
	cfGetTypeID               func(cf uintptr) uint64
	cfNumberGetTypeID         func() uint64
	cfStringGetTypeID         func() uint64
	cfUUIDCreateFromUUIDBytes func(alloc uintptr, lo, hi uint64) uintptr

	ioServiceMatching             func(name string) uintptr
	ioServiceGetMatchingServices  func(mainPort uint32, matching uintptr, existing *uint32) int32
	ioIteratorNext                func(iter uint32) uint32
	ioObjectRelease               func(obj uint32) int32
	ioRegistryEntryCreateProperty func(entry uint32, key uintptr, alloc uintptr, opts uint32) uintptr
	ioCreatePlugInInterface       func(service uint32, pluginType, interfaceType uintptr, theInterface *unsafe.Pointer, score *int32) int32

	cMalloc func(size uint64) unsafe.Pointer
	cFree   func(unsafe.Pointer)
)

var (
	loadOnce sync.Once
	loadErr  error
)

// load resolves every symbol once. A failure means the process is not on a
// macOS with IOKit, which no retry will fix.
func load() error {
	loadOnce.Do(func() { loadErr = doLoad() })
	return loadErr
}

// doLoad is the body of load, split out so its error paths are reachable from a
// test without burning the sync.Once.
func doLoad() error {
	cf, err := dlopen(frameworkCoreFoundation)
	if err != nil {
		return err
	}
	iokit, err := dlopen(frameworkIOKit)
	if err != nil {
		return err
	}
	libc, err := dlopen(libSystem)
	if err != nil {
		return err
	}

	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfGetTypeID, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&cfNumberGetTypeID, cf, "CFNumberGetTypeID")
	purego.RegisterLibFunc(&cfStringGetTypeID, cf, "CFStringGetTypeID")
	purego.RegisterLibFunc(&cfUUIDCreateFromUUIDBytes, cf, "CFUUIDCreateFromUUIDBytes")

	purego.RegisterLibFunc(&ioServiceMatching, iokit, "IOServiceMatching")
	purego.RegisterLibFunc(&ioServiceGetMatchingServices, iokit, "IOServiceGetMatchingServices")
	purego.RegisterLibFunc(&ioIteratorNext, iokit, "IOIteratorNext")
	purego.RegisterLibFunc(&ioObjectRelease, iokit, "IOObjectRelease")
	purego.RegisterLibFunc(&ioRegistryEntryCreateProperty, iokit, "IORegistryEntryCreateCFProperty")
	purego.RegisterLibFunc(&ioCreatePlugInInterface, iokit, "IOCreatePlugInInterfaceForService")

	purego.RegisterLibFunc(&cMalloc, libc, "malloc")
	purego.RegisterLibFunc(&cFree, libc, "free")
	return nil
}

// dlopen is a seam so a test can force doLoad's failure path without a machine
// that lacks IOKit.
var dlopen = func(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

// ---------------------------------------------------------------------------
// COM-style dispatch.
// ---------------------------------------------------------------------------

// vcall invokes vtable entry idx on a CFPlugIn interface pointer. iface is the
// double pointer IOKit handed back: the first dereference is the vtable, the
// second is the function.
func vcall(iface unsafe.Pointer, idx int, args ...uintptr) int32 {
	vtbl := *(*unsafe.Pointer)(iface)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*int(unsafe.Sizeof(uintptr(0)))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(iface))
	all = append(all, args...)
	r, _, _ := purego.SyscallN(fn, all...)
	return int32(uint32(r))
}

// ---------------------------------------------------------------------------
// Native device registry.
//
// The portable half of the package carries an opaque uintptr per device. On
// darwin that uintptr is a token into this registry rather than a raw pointer,
// for two reasons: a raw C pointer round-tripped through uintptr would need an
// unsafe.Pointer conversion that go vet rightly refuses, and a token lets a
// stale handle be rejected instead of dereferenced.
// ---------------------------------------------------------------------------

type native struct {
	service uint32         // io_service_t, held until release
	dev     unsafe.Pointer // IOUSBDeviceInterface**, nil until attached
	hasTO   bool           // the interface version we got has DeviceRequestTO
}

var (
	registryMu   sync.Mutex
	registry             = map[uintptr]*native{}
	registryNext uintptr = 1
)

func registryAdd(n *native) uintptr {
	registryMu.Lock()
	defer registryMu.Unlock()
	tok := registryNext
	registryNext++
	registry[tok] = n
	return tok
}

func registryGet(tok uintptr) *native {
	registryMu.Lock()
	defer registryMu.Unlock()
	return registry[tok]
}

func registryDrop(tok uintptr) *native {
	registryMu.Lock()
	defer registryMu.Unlock()
	n := registry[tok]
	delete(registry, tok)
	return n
}

func init() {
	enumerate = darwinEnumerate
	openDev = darwinOpen
	closeDev = darwinClose
	control = darwinControl
	configDesc = darwinConfigDesc
	releaseRef = darwinRelease
}

// ---------------------------------------------------------------------------
// Enumeration.
// ---------------------------------------------------------------------------

// serviceClasses are the IOKit class names a USB device may be registered
// under. Modern macOS uses IOUSBHostDevice; the older name still matches on
// systems that kept the legacy family, and trying both costs one empty
// iterator.
var serviceClasses = []string{"IOUSBHostDevice", "IOUSBDevice"}

// darwinEnumerate lists every USB device. The portable [Filter] narrows in Go
// rather than through a matching dictionary, which avoids building a
// CFDictionary for what is a handful of integer comparisons.
func darwinEnumerate() ([]Info, []uintptr, error) {
	if err := load(); err != nil {
		return nil, nil, err
	}
	for _, class := range serviceClasses {
		matching := ioServiceMatching(class)
		if matching == 0 {
			continue
		}
		var iter uint32
		// IOServiceGetMatchingServices consumes the dictionary reference,
		// success or failure, so it must not be released here.
		if rc := ioServiceGetMatchingServices(0, matching, &iter); rc != 0 {
			continue
		}
		infos, refs := drainIterator(iter)
		ioObjectRelease(iter)
		if len(infos) > 0 {
			return infos, refs, nil
		}
	}
	return nil, nil, nil // no USB devices is not an error
}

// drainIterator turns an io_iterator_t into described, held devices.
func drainIterator(iter uint32) ([]Info, []uintptr) {
	var infos []Info
	var refs []uintptr
	for {
		svc := ioIteratorNext(iter)
		if svc == 0 {
			return infos, refs
		}
		// The iterator hands over a reference; the registry entry keeps it and
		// darwinRelease gives it back.
		infos = append(infos, describe(svc))
		refs = append(refs, registryAdd(&native{service: svc}))
	}
}

// describe reads a device's registry properties. Every one is optional: a
// device that omits a property gets a zero field, not an error.
func describe(svc uint32) Info {
	vid, _ := propInt(svc, "idVendor")
	pid, _ := propInt(svc, "idProduct")
	cls, _ := propInt(svc, "bDeviceClass")
	sub, _ := propInt(svc, "bDeviceSubClass")
	proto, _ := propInt(svc, "bDeviceProtocol")
	rel, _ := propInt(svc, "bcdDevice")
	loc, _ := propInt(svc, "locationID")
	speed, _ := propInt(svc, "Device Speed")
	return Info{
		VendorID:     uint16(vid),
		ProductID:    uint16(pid),
		Class:        uint8(cls),
		SubClass:     uint8(sub),
		Protocol:     uint8(proto),
		Release:      uint16(rel),
		LocationID:   uint32(loc),
		Speed:        uint8(speed),
		Product:      propStr(svc, "USB Product Name"),
		Manufacturer: propStr(svc, "USB Vendor Name"),
		SerialNumber: propStr(svc, "USB Serial Number"),
	}
}

// prop reads one registry property, which the caller owns and must release.
func prop(svc uint32, key string) uintptr {
	k := cfStringCreateWithCString(0, key, kCFStringEncodingUTF8)
	if k == 0 {
		return 0
	}
	defer cfRelease(k)
	return ioRegistryEntryCreateProperty(svc, k, 0, 0)
}

// propInt reads an integer property, reporting whether it was present AND was
// actually a number. The type check is not defensive noise: CFNumberGetValue on
// a CFString is undefined behaviour, and the registry is free to publish a
// property under a type this code did not expect.
func propInt(svc uint32, key string) (int64, bool) {
	v := prop(svc, key)
	if v == 0 {
		return 0, false
	}
	defer cfRelease(v)
	if cfGetTypeID(v) != cfNumberGetTypeID() {
		return 0, false
	}
	var out int64
	if !cfNumberGetValue(v, kCFNumberSInt64Type, unsafe.Pointer(&out)) {
		return 0, false
	}
	return out, true
}

// propStr reads a string property, yielding "" when absent or not a string.
func propStr(svc uint32, key string) string {
	v := prop(svc, key)
	if v == 0 {
		return ""
	}
	defer cfRelease(v)
	if cfGetTypeID(v) != cfStringGetTypeID() {
		return ""
	}
	buf := make([]byte, 512)
	if !cfStringGetCString(v, unsafe.Pointer(&buf[0]), int64(len(buf)), kCFStringEncodingUTF8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// ---------------------------------------------------------------------------
// Attaching, opening, closing.
// ---------------------------------------------------------------------------

// attach creates the CFPlugIn for a service and queries it for a USB device
// interface. It does NOT open the device: the resulting interface already
// answers the questions that read cached kernel state, such as
// GetConfigurationDescriptorPtr.
func attach(n *native) ioreturn.Code {
	if n.dev != nil {
		return ioreturn.Success
	}
	pluginType := cfUUIDCreateFromUUIDBytes(0, uuidDeviceUserClientType.lo, uuidDeviceUserClientType.hi)
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
	// The plugin is released either way: on success the device interface it
	// produced holds its own reference.
	defer vcall(plugin, idxRelease)

	// The out-parameter is C memory, because purego.SyscallN passes uintptr
	// arguments the garbage collector cannot see.
	slot := cMalloc(uint64(unsafe.Sizeof(uintptr(0))))
	if slot == nil {
		return ioreturn.NoMemory
	}
	defer cFree(slot)

	last := ioreturn.Unsupported
	for _, cand := range deviceInterfaces {
		*(*uintptr)(slot) = 0
		hr := vcall(plugin, idxQueryInterface, uintptr(cand.id.lo), uintptr(cand.id.hi), uintptr(slot))
		dev := *(*unsafe.Pointer)(slot)
		if hr == 0 && dev != nil {
			n.dev = dev
			n.hasTO = cand.hasTimeouts
			return ioreturn.Success
		}
		last = ioreturn.Code(hr)
	}
	return last
}

// darwinOpen attaches if needed, then takes a device-level handle.
func darwinOpen(tok uintptr, seize bool) ioreturn.Code {
	if err := load(); err != nil {
		return ioreturn.Err
	}
	n := registryGet(tok)
	if n == nil {
		return ioreturn.NoDevice
	}
	if rc := attach(n); !ioreturn.IsSuccess(rc) {
		return rc
	}
	idx := idxUSBDeviceOpen
	if seize {
		if !n.hasTO {
			// USBDeviceOpenSeize arrived with interface version 182, alongside
			// the timeout variants; an interface old enough to lack one lacks
			// the other, and calling index 29 on it would jump past the vtable.
			return ioreturn.Unsupported
		}
		idx = idxUSBDeviceOpenSeize
	}
	return ioreturn.Code(vcall(n.dev, idx))
}

// darwinClose closes the handle but keeps the interface, so a caller may reopen
// or keep reading cached descriptors.
func darwinClose(tok uintptr) ioreturn.Code {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return ioreturn.NotOpen
	}
	return ioreturn.Code(vcall(n.dev, idxUSBDeviceClose))
}

// darwinRelease drops the interface and the io_service_t reference.
func darwinRelease(tok uintptr) {
	n := registryDrop(tok)
	if n == nil || load() != nil {
		return
	}
	if n.dev != nil {
		vcall(n.dev, idxRelease)
		n.dev = nil
	}
	if n.service != 0 {
		ioObjectRelease(n.service)
		n.service = 0
	}
}

// ---------------------------------------------------------------------------
// Control transfers.
// ---------------------------------------------------------------------------

// IOUSBDevRequestTO field offsets, in bytes, as printed by offsetof(). The
// struct is 32 bytes; the no-timeout IOUSBDevRequest is its first 24 minus the
// two timeout words, and shares every offset below wLenDone.
const (
	reqBmRequestType     = 0
	reqBRequest          = 1
	reqWValue            = 2
	reqWIndex            = 4
	reqWLength           = 6
	reqPData             = 8
	reqWLenDone          = 16
	reqNoDataTimeout     = 20
	reqCompletionTimeout = 24
	reqSizeTO            = 32
	reqSize              = 24
)

// darwinControl runs one control transfer on endpoint 0.
//
// IOUSBLib takes wValue, wIndex and wLength in HOST byte order and does the
// USB byte swapping itself, so the fields go in as native little-endian words.
func darwinControl(tok uintptr, s Setup, data []byte, timeout time.Duration) (int, ioreturn.Code) {
	n := registryGet(tok)
	if n == nil || n.dev == nil {
		return 0, ioreturn.NotOpen
	}

	size := uint64(reqSizeTO)
	if !n.hasTO {
		size = reqSize
	}
	req := cMalloc(size)
	if req == nil {
		return 0, ioreturn.NoMemory
	}
	defer cFree(req)
	r := unsafe.Slice((*byte)(req), size)
	clear(r)

	var buf unsafe.Pointer
	if len(data) > 0 {
		buf = cMalloc(uint64(len(data)))
		if buf == nil {
			return 0, ioreturn.NoMemory
		}
		defer cFree(buf)
		cb := unsafe.Slice((*byte)(buf), len(data))
		clear(cb)
		if s.Direction == Out {
			copy(cb, data)
		}
	}

	r[reqBmRequestType] = s.RequestType()
	r[reqBRequest] = s.Request
	putU16(r[reqWValue:], s.Value)
	putU16(r[reqWIndex:], s.Index)
	putU16(r[reqWLength:], uint16(len(data)))
	// Stored as uintptr rather than unsafe.Pointer: writing a pointer-typed
	// word into memory Go does not own would arm a write barrier for no reason.
	*(*uintptr)(unsafe.Add(req, reqPData)) = uintptr(buf)

	idx := idxDeviceRequest
	if n.hasTO {
		ms := uint32(timeout.Milliseconds())
		if ms == 0 {
			ms = 1
		}
		putU32(r[reqNoDataTimeout:], ms)
		putU32(r[reqCompletionTimeout:], ms)
		idx = idxDeviceRequestTO
	}

	rc := vcall(n.dev, idx, uintptr(req))
	done := int(u32(r[reqWLenDone:]))
	if s.Direction == In && done > 0 && buf != nil {
		if done > len(data) {
			done = len(data)
		}
		copy(data, unsafe.Slice((*byte)(buf), done))
	}
	return done, ioreturn.Code(rc)
}

// darwinConfigDesc reads a cached configuration descriptor. The kernel already
// fetched it during enumeration, so this needs the interface but not an open
// device -- which is the whole reason it exists as its own seam.
func darwinConfigDesc(tok uintptr, index byte) ([]byte, ioreturn.Code) {
	if err := load(); err != nil {
		return nil, ioreturn.Err
	}
	n := registryGet(tok)
	if n == nil {
		return nil, ioreturn.NoDevice
	}
	if rc := attach(n); !ioreturn.IsSuccess(rc) {
		return nil, rc
	}
	slot := cMalloc(uint64(unsafe.Sizeof(uintptr(0))))
	if slot == nil {
		return nil, ioreturn.NoMemory
	}
	defer cFree(slot)
	*(*uintptr)(slot) = 0

	rc := vcall(n.dev, idxGetConfigDescPtr, uintptr(index), uintptr(slot))
	if rc != 0 {
		return nil, ioreturn.Code(rc)
	}
	desc := *(*unsafe.Pointer)(slot)
	if desc == nil {
		return nil, ioreturn.NoResources
	}
	// The descriptor's own header carries the total length of the whole
	// configuration block: bLength, bDescriptorType, then wTotalLength. IOKit
	// caches it in USB byte order, little-endian.
	head := unsafe.Slice((*byte)(desc), 4)
	total := int(u16(head[2:]))
	if total < 4 {
		return nil, ioreturn.BadArgument
	}
	// Copy out of kernel-cached memory before returning: the caller must not
	// hold a pointer whose lifetime IOKit controls.
	out := make([]byte, total)
	copy(out, unsafe.Slice((*byte)(desc), total))
	return out, ioreturn.Success
}

// Little-endian helpers over C memory. encoding/binary would do, but these are
// four lines and keep this file free of a dependency it needs nowhere else.
func putU16(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }
func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
func u16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
