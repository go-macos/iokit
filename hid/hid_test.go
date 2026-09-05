package hid

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// withFakes swaps every platform seam for a controllable fake and restores the
// real ones afterwards, so these tests run identically on darwin (where init()
// wired IOKit) and on any other platform.
type fakes struct {
	infos     []Info
	refs      []uintptr
	enumErr   error
	openCode  int32
	closeCode int32
	setCode   int32
	streamErr error
	// observed
	released  []uintptr
	setCalls  [][]byte
	setKinds  []uint32
	setIDs    []int64
	streamRun func(deliver func(int, []byte))
	gotSizes  []int
}

func withFakes(t *testing.T, f *fakes) {
	t.Helper()
	oldEnum, oldOpen, oldClose, oldSet, oldStream, oldRel :=
		enumerate, openDev, closeDev, setReport, stream, releaseRef
	t.Cleanup(func() {
		enumerate, openDev, closeDev, setReport, stream, releaseRef =
			oldEnum, oldOpen, oldClose, oldSet, oldStream, oldRel
	})
	enumerate = func(Filter) ([]Info, []uintptr, error) {
		if f.enumErr != nil {
			return nil, nil, f.enumErr
		}
		return f.infos, f.refs, nil
	}
	openDev = func(uintptr) int32 { return f.openCode }
	closeDev = func(uintptr) int32 { return f.closeCode }
	setReport = func(_ uintptr, kind uint32, id int64, data []byte) int32 {
		f.setKinds = append(f.setKinds, kind)
		f.setIDs = append(f.setIDs, id)
		f.setCalls = append(f.setCalls, data)
		return f.setCode
	}
	stream = func(_ context.Context, refs []uintptr, sizes []int, deliver func(int, []byte)) error {
		f.gotSizes = sizes
		if f.streamRun != nil {
			f.streamRun(deliver)
		}
		return f.streamErr
	}
	releaseRef = func(r uintptr) { f.released = append(f.released, r) }
}

func TestReportKindString(t *testing.T) {
	for _, tc := range []struct {
		k    ReportKind
		want string
	}{
		{Input, "input"},
		{Output, "output"},
		{Feature, "feature"},
		{ReportKind(9), "ReportKind(9)"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("ReportKind(%d).String() = %q, want %q", uint32(tc.k), got, tc.want)
		}
	}
}

func TestIOErrorError(t *testing.T) {
	for _, tc := range []struct {
		code int32
		want string // substring that must appear
	}{
		{ioReturnNotPermitted, "not permitted"},
		{ioReturnNoDevice, "unplugged"},
		{ioReturnExclusiveAcc, "exclusive access"},
		{ioReturnUnsupportedOp, "no such report channel"},
		{ioReturnInternalError, "frameworks could not be loaded"},
		{-1, "IOReturn 0xffffffff"},
	} {
		e := &IOError{Op: "Op", Code: tc.code}
		got := e.Error()
		if !strings.Contains(got, tc.want) {
			t.Errorf("IOError{%d}.Error() = %q, want it to contain %q", tc.code, got, tc.want)
		}
		if !strings.Contains(got, "Op") {
			t.Errorf("IOError.Error() = %q, want it to name the op", got)
		}
	}
}

func TestIOErr(t *testing.T) {
	if err := ioErr("x", ioReturnSuccess); err != nil {
		t.Errorf("ioErr(success) = %v, want nil", err)
	}
	err := ioErr("x", ioReturnNoDevice)
	var ioe *IOError
	if !errors.As(err, &ioe) {
		t.Fatalf("ioErr(failure) = %v, want *IOError", err)
	}
	if ioe.Op != "x" || ioe.Code != ioReturnNoDevice {
		t.Errorf("ioErr gave %+v, want Op=x Code=%d", ioe, ioReturnNoDevice)
	}
}

func TestInfoString(t *testing.T) {
	full := Info{
		VendorID: 0x35CA, ProductID: 0x1201, UsagePage: 0xFF00, Usage: 1,
		Product: "Beast", Manufacturer: "VITURE",
		MaxInputReportSize: 64, MaxOutputReportSize: 64,
	}
	got := full.String()
	for _, want := range []string{"35ca:1201", "0xff00/0x1", "in=64", "out=64", `"Beast"`, `by "VITURE"`} {
		if !strings.Contains(got, want) {
			t.Errorf("Info.String() = %q, want it to contain %q", got, want)
		}
	}
	// The name fields are optional and must simply be omitted when absent.
	bare := Info{VendorID: 1, ProductID: 2}.String()
	if strings.Contains(bare, `"`) {
		t.Errorf("Info.String() with no names = %q, want no quoted name", bare)
	}
}

func TestFilterMatch(t *testing.T) {
	dev := Info{VendorID: 0x35CA, ProductID: 0x1201, UsagePage: 0xFF00, Usage: 0x01}
	for _, tc := range []struct {
		name string
		f    Filter
		want bool
	}{
		{"zero filter matches all", Filter{}, true},
		{"vendor match", Filter{VendorID: 0x35CA}, true},
		{"vendor mismatch", Filter{VendorID: 0x1234}, false},
		{"usage page match", Filter{UsagePage: 0xFF00}, true},
		{"usage page mismatch", Filter{UsagePage: 0x000C}, false},
		{"usage match", Filter{Usage: 0x01}, true},
		{"usage mismatch", Filter{Usage: 0x02}, false},
		{"product in list", Filter{ProductIDs: []uint16{0x1104, 0x1201}}, true},
		{"product not in list", Filter{ProductIDs: []uint16{0x1104}}, false},
		{"all constraints together", Filter{VendorID: 0x35CA, UsagePage: 0xFF00, Usage: 1, ProductIDs: []uint16{0x1201}}, true},
	} {
		if got := tc.f.Match(dev); got != tc.want {
			t.Errorf("%s: Match() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeviceOpen(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := &Device{ref: 7}
	if err := d.Open(); err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}
	if !d.open {
		t.Fatal("Open() succeeded but device is not marked open")
	}
	if err := d.Open(); err != nil { // idempotent
		t.Fatalf("second Open() = %v, want nil", err)
	}

	f.openCode = ioReturnNotPermitted
	bad := &Device{ref: 8}
	if err := bad.Open(); err == nil {
		t.Fatal("Open() with a failing IOReturn = nil, want an error")
	}
	if bad.open {
		t.Fatal("Open() failed but device is marked open")
	}
}

func TestDeviceClose(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	// An open device closes the handle and releases the reference.
	d := &Device{ref: 3}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if want := []uintptr{3}; len(f.released) != 1 || f.released[0] != want[0] {
		t.Errorf("released = %v, want %v", f.released, want)
	}
	if err := d.Close(); err != nil { // idempotent
		t.Fatalf("second Close() = %v, want nil", err)
	}
	if len(f.released) != 1 {
		t.Errorf("second Close() released again: %v", f.released)
	}

	// A never-opened device is released without a close call.
	f.released = nil
	f.closeCode = ioReturnNoDevice
	never := &Device{ref: 4}
	if err := never.Close(); err != nil {
		t.Fatalf("Close() of an unopened device = %v, want nil", err)
	}
	if len(f.released) != 1 {
		t.Errorf("released = %v, want the reference dropped once", f.released)
	}

	// A failing close still releases, and reports the failure.
	f.released = nil
	f.closeCode = ioReturnNoDevice
	f.openCode = ioReturnSuccess
	bad := &Device{ref: 5}
	if err := bad.Open(); err != nil {
		t.Fatal(err)
	}
	if err := bad.Close(); err == nil {
		t.Error("Close() with a failing IOReturn = nil, want an error")
	}
	if len(f.released) != 1 {
		t.Errorf("a failing Close() must still release: %v", f.released)
	}
}

func TestDeviceSetReport(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := &Device{ref: 1}
	if err := d.SetReport(Output, 0, []byte{1}); !errors.Is(err, ErrNotOpen) {
		t.Errorf("SetReport on a closed device = %v, want ErrNotOpen", err)
	}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetReport(Output, 0, nil); !errors.Is(err, ErrEmptyReport) {
		t.Errorf("SetReport with no data = %v, want ErrEmptyReport", err)
	}
	if err := d.SetReport(Feature, 3, []byte{0xAA, 0xBB}); err != nil {
		t.Fatalf("SetReport() = %v, want nil", err)
	}
	if len(f.setCalls) != 1 || f.setKinds[0] != uint32(Feature) || f.setIDs[0] != 3 {
		t.Errorf("SetReport passed kind=%v id=%v, want feature/3", f.setKinds, f.setIDs)
	}

	f.setCode = ioReturnUnsupportedOp
	if err := d.SetReport(Output, 0, []byte{1}); err == nil {
		t.Error("SetReport with a failing IOReturn = nil, want an error")
	}
}

func TestDevicesFiltersAndReleases(t *testing.T) {
	f := &fakes{
		infos: []Info{
			{VendorID: 0x35CA, ProductID: 0x1201, Product: "Beast"},
			{VendorID: 0x05AC, ProductID: 0x0001},
			{VendorID: 0x35CA, ProductID: 0x1102},
		},
		refs: []uintptr{10, 11, 12},
	}
	withFakes(t, f)

	devs, err := Devices(Filter{VendorID: 0x35CA})
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("Devices() returned %d devices, want 2", len(devs))
	}
	if devs[0].Info().Product != "Beast" {
		t.Errorf("first device = %q, want Beast", devs[0].Info().Product)
	}
	if got := devs[0].String(); !strings.Contains(got, "Beast") {
		t.Errorf("Device.String() = %q, want it to name the product", got)
	}
	// The rejected device must not be leaked.
	if len(f.released) != 1 || f.released[0] != 11 {
		t.Errorf("released = %v, want exactly the rejected ref 11", f.released)
	}
}

func TestDevicesEnumerateError(t *testing.T) {
	sentinel := errors.New("boom")
	withFakes(t, &fakes{enumErr: sentinel})
	if _, err := Devices(Filter{}); !errors.Is(err, sentinel) {
		t.Errorf("Devices() = %v, want the enumeration error", err)
	}
}

func TestStream(t *testing.T) {
	if err := Stream(context.Background(), func(*Device, []byte) {}); !errors.Is(err, ErrNoDevices) {
		t.Errorf("Stream() with no devices = %v, want ErrNoDevices", err)
	}

	f := &fakes{}
	withFakes(t, f)
	closed := &Device{ref: 1}
	if err := Stream(context.Background(), func(*Device, []byte) {}, closed); !errors.Is(err, ErrNotOpen) {
		t.Errorf("Stream() with an unopened device = %v, want ErrNotOpen", err)
	}

	// Two devices: the deliver index must select the right one, and the buffer
	// size must come from each device's own descriptor (with the fallback).
	a := &Device{ref: 1, info: Info{Product: "a", MaxInputReportSize: 8}}
	b := &Device{ref: 2, info: Info{Product: "b"}} // no advertised size
	for _, d := range []*Device{a, b} {
		if err := d.Open(); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	f.streamRun = func(deliver func(int, []byte)) {
		deliver(1, []byte{0xFF})
		deliver(0, []byte{0x01, 0x02})
	}
	if err := Stream(context.Background(), func(d *Device, data []byte) {
		seen = append(seen, d.Info().Product+":"+string(rune('0'+len(data))))
	}, a, b); err != nil {
		t.Fatalf("Stream() = %v, want nil", err)
	}
	if len(seen) != 2 || seen[0] != "b:1" || seen[1] != "a:2" {
		t.Errorf("deliver routed to %v, want [b:1 a:2]", seen)
	}
	if want := []int{8, defaultBufSize}; len(f.gotSizes) != 2 || f.gotSizes[0] != want[0] || f.gotSizes[1] != want[1] {
		t.Errorf("buffer sizes = %v, want %v", f.gotSizes, want)
	}
}

func TestStreamPropagatesError(t *testing.T) {
	sentinel := errors.New("pump failed")
	f := &fakes{streamErr: sentinel}
	withFakes(t, f)
	d := &Device{ref: 1}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := Stream(context.Background(), func(*Device, []byte) {}, d); !errors.Is(err, sentinel) {
		t.Errorf("Stream() = %v, want the underlying error", err)
	}
}

func TestStreamRespectsContext(t *testing.T) {
	// The portable layer does not itself watch the context — it hands it to the
	// platform seam — so this asserts the contract holds end to end with a seam
	// that behaves like the real pump.
	f := &fakes{}
	withFakes(t, f)
	stream = func(ctx context.Context, _ []uintptr, _ []int, _ func(int, []byte)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	d := &Device{ref: 1}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := Stream(ctx, func(*Device, []byte) {}, d); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stream() = %v, want context.DeadlineExceeded", err)
	}
}

func TestBufSize(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, defaultBufSize},
		{-1, defaultBufSize},
		{64, 64},
		{1, 1},
	} {
		if got := bufSize(tc.in); got != tc.want {
			t.Errorf("bufSize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestUnsupportedMeansSomethingElseOnTheManager(t *testing.T) {
	// The same IOReturn means two very different things depending on what was
	// being attempted, and saying the device one for a MANAGER failure sends
	// the reader to the wrong end of the problem. It did, for an hour.
	dev := (&IOError{Op: "IOHIDDeviceSetReport", Code: ioReturnUnsupportedOp}).Error()
	mgr := (&IOError{Op: "IOHIDManagerOpen", Code: ioReturnUnsupportedOp}).Error()

	if !strings.Contains(dev, "report channel") {
		t.Errorf("on a device: %q", dev)
	}
	if strings.Contains(mgr, "report channel") {
		t.Errorf("on the manager, still talking about a report channel: %q", mgr)
	}
	if !strings.Contains(mgr, "Input Monitoring") {
		t.Errorf("on the manager, no mention of the consent that is nearly always the cause: %q", mgr)
	}
	// Both must still carry the operation and the raw code, because the
	// explanation is a guess and the number is not.
	for _, s := range []string{dev, mgr} {
		if !strings.Contains(s, "0xe00002c7") {
			t.Errorf("the raw IOReturn is missing from %q", s)
		}
	}
}

// TestClosingUnderAStreamIsRefused.
//
// ⛔⛔ CLOSING UNDER A STREAM DOES NOT FAIL, IT ENDS THE PROCESS. IOKit keeps
// the device scheduled on a run loop with a registered callback; releasing it
// leaves that schedule pointing at freed memory, and macOS kills the program
// with "BUG IN CLIENT OF LIBPLATFORM: os_unfair_lock is corrupt" -- SIGKILL,
// no Go panic, no stack, nothing on the program's own output. Measured
// 2026-09-05 on a VITURE headset: a caller that cancelled the context and
// closed immediately died on the FIRST attempt, twenty times out of twenty.
//
// ⚠ CANCELLING IS NOT STOPPING, which is why the mistake is easy: the pump is
// inside CFRunLoopRunInMode and only looks at the context when it comes out,
// so a caller that cancels and closes has not waited for anything. An error is
// a thing a caller can handle; a dead process is not.
func TestClosingUnderAStreamIsRefused(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := &Device{ref: 1, open: true, info: Info{Product: "a headset"}}
	var during error
	f.streamRun = func(deliver func(int, []byte)) {
		during = d.Close()
	}
	if err := Stream(context.Background(), func(*Device, []byte) {}, d); err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	if !errors.Is(during, ErrStreaming) {
		t.Errorf("Close() during a stream = %v, want ErrStreaming", during)
	}
	// And it says what to DO, because "refused" alone sends somebody looking
	// for a lock that is not there.
	if during != nil && !strings.Contains(during.Error(), "cancel, wait") {
		t.Errorf("the refusal reads %q and does not name the remedy", during)
	}
	// The refusal must not have half-closed it: a device released and then
	// reported as still open is the same crash with an error in front of it.
	if len(f.released) != 0 {
		t.Errorf("the refused Close released %v anyway", f.released)
	}
	if !d.open {
		t.Error("the refused Close marked the device shut")
	}

	// ⭐ AND THE DEVICE IS CLOSEABLE ONCE THE STREAM HAS RETURNED, which is the
	// half that makes this a guard rather than a leak: a device left marked
	// would be one nobody could ever close.
	if err := d.Close(); err != nil {
		t.Errorf("Close() after the stream = %v", err)
	}
	if len(f.released) != 1 {
		t.Errorf("the device was released %d times", len(f.released))
	}
}
