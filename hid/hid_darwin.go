//go:build darwin

package hid

import (
	"context"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Framework and dylib paths. IOKit carries the HID manager; CoreFoundation
// carries the run loop, the container types the registry answers in, and the
// string/number accessors used to read a property.
const (
	frameworkIOKit          = "/System/Library/Frameworks/IOKit.framework/IOKit"
	frameworkCoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	libSystem               = "/usr/lib/libSystem.B.dylib"
)

// CoreFoundation constants.
const (
	kCFStringEncodingUTF8 = 0x08000100
	kCFNumberSInt32Type   = 3
)

// pumpInterval is how long each CFRunLoopRunInMode call blocks. It bounds how
// quickly Stream notices a cancelled context; reports themselves are not
// delayed by it, since the run loop returns as soon as a source fires.
const pumpInterval = 50 * time.Millisecond

var (
	cfRelease                 func(uintptr)
	cfRetain                  func(uintptr) uintptr
	cfStringCreateWithCString func(alloc uintptr, s string, enc uint32) uintptr
	cfStringGetCString        func(str uintptr, buf unsafe.Pointer, size int64, enc uint32) bool
	cfNumberGetValue          func(num uintptr, theType int32, valuePtr unsafe.Pointer) bool
	cfSetGetCount             func(set uintptr) int64
	cfSetGetValues            func(set uintptr, values unsafe.Pointer)
	cfRunLoopGetCurrent       func() uintptr
	cfRunLoopRunInMode        func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32

	ioHIDManagerCreate            func(alloc uintptr, opts uint32) uintptr
	ioHIDManagerSetDeviceMatching func(mgr uintptr, matching uintptr)
	ioHIDManagerOpen              func(mgr uintptr, opts uint32) int32
	ioHIDManagerCopyDevices       func(mgr uintptr) uintptr
	ioHIDDeviceGetProperty        func(dev uintptr, key uintptr) uintptr
	ioHIDDeviceOpen               func(dev uintptr, opts uint32) int32
	ioHIDDeviceClose              func(dev uintptr, opts uint32) int32
	ioHIDDeviceSetReport          func(dev uintptr, typ uint32, reportID int64, report unsafe.Pointer, length int64) int32
	ioHIDDeviceRegisterInputCB    func(dev uintptr, report unsafe.Pointer, length int64, cb uintptr, ctx unsafe.Pointer)
	ioHIDDeviceScheduleWithRL     func(dev uintptr, rl uintptr, mode uintptr)
	ioHIDDeviceUnscheduleFromRL   func(dev uintptr, rl uintptr, mode uintptr)

	cMalloc func(size uint64) unsafe.Pointer
	cFree   func(unsafe.Pointer)

	kCFRunLoopDefaultMode uintptr
)

// loadOnce/loadErr record the one-shot framework and symbol resolution, so
// every entry point reports a clean error rather than calling a nil func value.
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
	purego.RegisterLibFunc(&cfRetain, cf, "CFRetain")
	purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&cfSetGetCount, cf, "CFSetGetCount")
	purego.RegisterLibFunc(&cfSetGetValues, cf, "CFSetGetValues")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")

	purego.RegisterLibFunc(&ioHIDManagerCreate, iokit, "IOHIDManagerCreate")
	purego.RegisterLibFunc(&ioHIDManagerSetDeviceMatching, iokit, "IOHIDManagerSetDeviceMatching")
	purego.RegisterLibFunc(&ioHIDManagerOpen, iokit, "IOHIDManagerOpen")
	purego.RegisterLibFunc(&ioHIDManagerCopyDevices, iokit, "IOHIDManagerCopyDevices")
	purego.RegisterLibFunc(&ioHIDDeviceGetProperty, iokit, "IOHIDDeviceGetProperty")
	purego.RegisterLibFunc(&ioHIDDeviceOpen, iokit, "IOHIDDeviceOpen")
	purego.RegisterLibFunc(&ioHIDDeviceClose, iokit, "IOHIDDeviceClose")
	purego.RegisterLibFunc(&ioHIDDeviceSetReport, iokit, "IOHIDDeviceSetReport")
	purego.RegisterLibFunc(&ioHIDDeviceRegisterInputCB, iokit, "IOHIDDeviceRegisterInputReportCallback")
	purego.RegisterLibFunc(&ioHIDDeviceScheduleWithRL, iokit, "IOHIDDeviceScheduleWithRunLoop")
	purego.RegisterLibFunc(&ioHIDDeviceUnscheduleFromRL, iokit, "IOHIDDeviceUnscheduleFromRunLoop")

	purego.RegisterLibFunc(&cMalloc, libc, "malloc")
	purego.RegisterLibFunc(&cFree, libc, "free")

	// CFRunLoop compares mode names by VALUE, not by pointer identity, and the
	// contents of the exported kCFRunLoopDefaultMode global are literally that
	// name. Building the CFString ourselves is therefore equivalent to
	// dereferencing the global -- and it avoids the uintptr->unsafe.Pointer
	// conversion that go vet's unsafeptr check rightly flags.
	kCFRunLoopDefaultMode = cfstr("kCFRunLoopDefaultMode")
	return nil
}

// dlopen is a seam so a test can force doLoad's failure path without a
// machine that lacks IOKit.
var dlopen = func(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

// cfstr makes a CFString the caller must release.
func cfstr(s string) uintptr {
	return cfStringCreateWithCString(0, s, kCFStringEncodingUTF8)
}

// propInt reads an integer registry property, reporting whether it was present.
func propInt(dev uintptr, key string) (int32, bool) {
	k := cfstr(key)
	defer cfRelease(k)
	v := ioHIDDeviceGetProperty(dev, k)
	if v == 0 {
		return 0, false
	}
	var out int32
	if !cfNumberGetValue(v, kCFNumberSInt32Type, unsafe.Pointer(&out)) {
		return 0, false
	}
	return out, true
}

// propStr reads a string registry property, yielding "" when absent.
func propStr(dev uintptr, key string) string {
	k := cfstr(key)
	defer cfRelease(k)
	v := ioHIDDeviceGetProperty(dev, k)
	if v == 0 {
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

func init() {
	enumerate = darwinEnumerate
	openDev = func(ref uintptr) int32 {
		if err := load(); err != nil {
			return ioReturnInternalError
		}
		return ioHIDDeviceOpen(ref, 0)
	}
	closeDev = func(ref uintptr) int32 {
		if err := load(); err != nil {
			return ioReturnInternalError
		}
		return ioHIDDeviceClose(ref, 0)
	}
	setReport = darwinSetReport
	stream = darwinStream
	releaseRef = func(ref uintptr) {
		if ref != 0 && load() == nil {
			cfRelease(ref)
		}
	}
}

// darwinEnumerate lists every HID device. It matches everything (a NULL
// matching dictionary) and lets the portable [Filter] narrow in Go, which
// avoids building a CFDictionary — and needing its two exported
// callback-struct globals — for what is a handful of integer comparisons.
func darwinEnumerate() ([]Info, []uintptr, error) {
	if err := load(); err != nil {
		return nil, nil, err
	}
	mgr := ioHIDManagerCreate(0, 0)
	if mgr == 0 {
		return nil, nil, &IOError{Op: "IOHIDManagerCreate", Code: ioReturnInternalError}
	}
	defer cfRelease(mgr)

	ioHIDManagerSetDeviceMatching(mgr, 0) // NULL: match every device
	if err := ioErr("IOHIDManagerOpen", ioHIDManagerOpen(mgr, 0)); err != nil {
		return nil, nil, err
	}
	set := ioHIDManagerCopyDevices(mgr)
	if set == 0 {
		return nil, nil, nil // no devices is not an error
	}
	defer cfRelease(set)

	n := cfSetGetCount(set)
	if n <= 0 {
		return nil, nil, nil
	}
	refs := make([]uintptr, n)
	cfSetGetValues(set, unsafe.Pointer(&refs[0]))

	infos := make([]Info, n)
	for i, ref := range refs {
		vid, _ := propInt(ref, "VendorID")
		pid, _ := propInt(ref, "ProductID")
		up, _ := propInt(ref, "PrimaryUsagePage")
		us, _ := propInt(ref, "PrimaryUsage")
		in, _ := propInt(ref, "MaxInputReportSize")
		out, _ := propInt(ref, "MaxOutputReportSize")
		loc, _ := propInt(ref, "LocationID")
		infos[i] = Info{
			VendorID:            uint16(vid),
			ProductID:           uint16(pid),
			UsagePage:           uint16(up),
			Usage:               uint16(us),
			Product:             propStr(ref, "Product"),
			Manufacturer:        propStr(ref, "Manufacturer"),
			SerialNumber:        propStr(ref, "SerialNumber"),
			Transport:           propStr(ref, "Transport"),
			MaxInputReportSize:  int(in),
			MaxOutputReportSize: int(out),
			LocationID:          uint32(loc),
		}
		// The set owns its members and we release it on return, so retain every
		// device the caller may keep. Devices the filter rejects are released by
		// the portable Devices().
		cfRetain(ref)
	}
	return infos, refs, nil
}

// darwinSetReport writes a report. The Go slice is only read for the duration
// of the call, so handing IOKit a Go pointer is safe: nothing retains it.
func darwinSetReport(ref uintptr, kind uint32, id int64, data []byte) int32 {
	if err := load(); err != nil {
		return ioReturnInternalError
	}
	return ioHIDDeviceSetReport(ref, kind, id, unsafe.Pointer(&data[0]), int64(len(data)))
}

// ---------------------------------------------------------------------------
// Input report streaming.
//
// IOKit hands each report to a C callback. purego can only produce a bounded
// number of callbacks over a process's life, so exactly one is created here and
// shared: it looks the sending device up in a registry to find which stream —
// and which index within that stream — the report belongs to.
// ---------------------------------------------------------------------------

type streamTarget struct {
	index   int
	deliver func(int, []byte)
}

var (
	registryMu sync.Mutex
	registry   = map[uintptr]streamTarget{}

	callbackOnce sync.Once
	callbackPtr  uintptr
)

// inputCallback is the single IOHIDReportCallback. Its parameters are declared
// as uintptr because each one is an integer or pointer word in both the arm64
// and amd64 C ABIs, and purego passes them straight through.
func inputCallback(ctx, result, sender uintptr, typ, reportID uint32, reportPtr unsafe.Pointer, length uintptr) uintptr {
	n := int(int64(length))
	if n <= 0 || reportPtr == nil {
		return 0
	}
	registryMu.Lock()
	t, ok := registry[sender]
	registryMu.Unlock()
	if !ok {
		return 0
	}
	t.deliver(t.index, unsafe.Slice((*byte)(reportPtr), n))
	return 0
}

// darwinStream schedules every device on this thread's run loop and pumps it
// until ctx is done.
//
// The goroutine is pinned for the whole call because the run loop, and the
// devices attached to it, belong to ONE OS thread. Getting this wrong is
// silent: the devices attach to the thread that happened to be running, the Go
// scheduler moves the goroutine, and the pump then services a run loop nothing
// is attached to — zero reports, no error. That was this package's first bug.
func darwinStream(ctx context.Context, refs []uintptr, sizes []int, deliver func(int, []byte)) error {
	if err := load(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	callbackOnce.Do(func() { callbackPtr = purego.NewCallback(inputCallback) })

	rl := cfRunLoopGetCurrent()
	bufs := make([]unsafe.Pointer, len(refs))

	registryMu.Lock()
	for i, ref := range refs {
		registry[ref] = streamTarget{index: i, deliver: deliver}
	}
	registryMu.Unlock()

	for i, ref := range refs {
		// The buffer is C-owned on purpose: IOKit keeps the pointer for as long
		// as the callback is registered, which is exactly what a Go pointer may
		// not be used for.
		bufs[i] = cMalloc(uint64(sizes[i]))
		ioHIDDeviceRegisterInputCB(ref, bufs[i], int64(sizes[i]), callbackPtr, nil)
		ioHIDDeviceScheduleWithRL(ref, rl, kCFRunLoopDefaultMode)
	}

	defer func() {
		for i, ref := range refs {
			// Detach before freeing, so an in-flight report cannot be written
			// into memory already handed back to the allocator.
			ioHIDDeviceRegisterInputCB(ref, bufs[i], int64(sizes[i]), 0, nil)
			ioHIDDeviceUnscheduleFromRL(ref, rl, kCFRunLoopDefaultMode)
			cFree(bufs[i])
		}
		registryMu.Lock()
		for _, ref := range refs {
			delete(registry, ref)
		}
		registryMu.Unlock()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfRunLoopRunInMode(kCFRunLoopDefaultMode, pumpInterval.Seconds(), false)
	}
}
