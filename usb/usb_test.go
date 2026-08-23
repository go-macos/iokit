package usb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

// withFakes swaps every platform seam for a controllable fake and restores the
// real ones afterwards, so these tests run identically on darwin (where init()
// wired IOUSBLib) and on any other platform.
type fakes struct {
	infos     []Info
	refs      []uintptr
	enumErr   error
	openCode  ioreturn.Code
	closeCode ioreturn.Code
	ctlN      int
	ctlCode   ioreturn.Code
	cfgBytes  []byte
	cfgCode   ioreturn.Code
	// observed
	released   []uintptr
	openSeizes []bool
	setups     []Setup
	timeouts   []time.Duration
	ctlData    [][]byte
	// fill, when set, is written into the caller's buffer before returning, so
	// a test can prove an In transfer copies bytes back.
	fill []byte
}

func withFakes(t *testing.T, f *fakes) {
	t.Helper()
	oldEnum, oldOpen, oldClose, oldCtl, oldCfg, oldRel :=
		enumerate, openDev, closeDev, control, configDesc, releaseRef
	t.Cleanup(func() {
		enumerate, openDev, closeDev, control, configDesc, releaseRef =
			oldEnum, oldOpen, oldClose, oldCtl, oldCfg, oldRel
	})
	enumerate = func() ([]Info, []uintptr, error) {
		if f.enumErr != nil {
			return nil, nil, f.enumErr
		}
		return f.infos, f.refs, nil
	}
	openDev = func(_ uintptr, seize bool) ioreturn.Code {
		f.openSeizes = append(f.openSeizes, seize)
		return f.openCode
	}
	closeDev = func(uintptr) ioreturn.Code { return f.closeCode }
	control = func(_ uintptr, s Setup, data []byte, to time.Duration) (int, ioreturn.Code) {
		f.setups = append(f.setups, s)
		f.timeouts = append(f.timeouts, to)
		f.ctlData = append(f.ctlData, append([]byte(nil), data...))
		copy(data, f.fill)
		return f.ctlN, f.ctlCode
	}
	configDesc = func(uintptr, byte) ([]byte, ioreturn.Code) { return f.cfgBytes, f.cfgCode }
	releaseRef = func(r uintptr) { f.released = append(f.released, r) }
}

// openDevice returns a Device already through Open, for tests about what comes
// after opening.
func openDevice(t *testing.T, f *fakes) *Device {
	t.Helper()
	d := &Device{ref: 1, info: Info{VendorID: 0x35CA, ProductID: 0x1201}}
	if err := d.Open(); err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return d
}

func TestDirectionString(t *testing.T) {
	if got := In.String(); got != "in" {
		t.Errorf("In.String() = %q", got)
	}
	if got := Out.String(); got != "out" {
		t.Errorf("Out.String() = %q", got)
	}
}

func TestTypeString(t *testing.T) {
	for _, tc := range []struct {
		t    Type
		want string
	}{
		{Standard, "standard"},
		{Class, "class"},
		{Vendor, "vendor"},
		{Type(0x60), "Type(0x60)"},
	} {
		if got := tc.t.String(); got != tc.want {
			t.Errorf("Type(%#02x).String() = %q, want %q", uint8(tc.t), got, tc.want)
		}
	}
}

func TestRecipientString(t *testing.T) {
	for _, tc := range []struct {
		r    Recipient
		want string
	}{
		{ToDevice, "device"},
		{ToInterface, "interface"},
		{ToEndpoint, "endpoint"},
		{ToOther, "other"},
		{Recipient(9), "Recipient(9)"},
	} {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("Recipient(%d).String() = %q, want %q", uint8(tc.r), got, tc.want)
		}
	}
}

// TestRequestType pins the bit assembly. bmRequestType is the one field a
// hand-built control transfer gets wrong silently: a wrong direction bit turns
// a read into a write.
func TestRequestType(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Setup
		want byte
	}{
		{"vendor in to device", Setup{Direction: In, Type: Vendor, Recipient: ToDevice}, 0xC0},
		{"vendor out to device", Setup{Direction: Out, Type: Vendor, Recipient: ToDevice}, 0x40},
		{"vendor in to interface", Setup{Direction: In, Type: Vendor, Recipient: ToInterface}, 0xC1},
		{"standard in to device", Setup{Direction: In, Type: Standard, Recipient: ToDevice}, 0x80},
		{"class out to endpoint", Setup{Direction: Out, Type: Class, Recipient: ToEndpoint}, 0x22},
	} {
		if got := tc.s.RequestType(); got != tc.want {
			t.Errorf("%s: RequestType() = %#02x, want %#02x", tc.name, got, tc.want)
		}
	}
}

func TestSetupString(t *testing.T) {
	s := Setup{Direction: In, Type: Vendor, Recipient: ToInterface, Request: 0x15, Value: 0x0102, Index: 3}
	got := s.String()
	for _, want := range []string{"bmRequestType=0xc1", "in", "vendor", "interface", "bRequest=0x15", "wValue=0x0102", "wIndex=0x0003"} {
		if !strings.Contains(got, want) {
			t.Errorf("Setup.String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestGetDescriptor(t *testing.T) {
	s := GetDescriptor(DescString, 2, 0x0409)
	if s.RequestType() != 0x80 {
		t.Errorf("bmRequestType = %#02x, want 0x80", s.RequestType())
	}
	if s.Request != ReqGetDescriptor {
		t.Errorf("bRequest = %#02x, want %#02x", s.Request, ReqGetDescriptor)
	}
	// wValue packs the descriptor type in the high byte and the index in the low.
	if s.Value != 0x0302 {
		t.Errorf("wValue = %#04x, want 0x0302", s.Value)
	}
	if s.Index != 0x0409 {
		t.Errorf("wIndex = %#04x, want 0x0409", s.Index)
	}
}

func TestIOError(t *testing.T) {
	e := &IOError{Op: "DeviceRequestTO", Code: ioreturn.USBPipeStalled}
	got := e.Error()
	for _, want := range []string{"usb:", "DeviceRequestTO", "kIOUSBPipeStalled"} {
		if !strings.Contains(got, want) {
			t.Errorf("IOError.Error() = %q, want it to contain %q", got, want)
		}
	}
	if !e.Stalled() {
		t.Error("Stalled() = false for kIOUSBPipeStalled")
	}
	if (&IOError{Code: ioreturn.Timeout}).Stalled() {
		t.Error("Stalled() = true for a timeout")
	}
}

func TestIOErr(t *testing.T) {
	if err := ioErr("x", ioreturn.Success); err != nil {
		t.Errorf("ioErr(success) = %v, want nil", err)
	}
	var ioe *IOError
	if err := ioErr("x", ioreturn.NoDevice); !errors.As(err, &ioe) {
		t.Fatalf("ioErr(failure) = %v, want *IOError", err)
	}
	if ioe.Op != "x" || ioe.Code != ioreturn.NoDevice {
		t.Errorf("ioErr gave %+v", ioe)
	}
}

func TestInfoString(t *testing.T) {
	full := Info{VendorID: 0x35CA, ProductID: 0x1201, LocationID: 0x02130000,
		Class: 239, SubClass: 2, Protocol: 1, Product: "Beast", Manufacturer: "VITURE"}
	got := full.String()
	for _, want := range []string{"35ca:1201", "loc=0x02130000", "class=239/2/1", `"Beast"`, `by "VITURE"`} {
		if !strings.Contains(got, want) {
			t.Errorf("Info.String() = %q, want it to contain %q", got, want)
		}
	}
	if bare := (Info{VendorID: 1, ProductID: 2}).String(); strings.Contains(bare, `"`) {
		t.Errorf("Info.String() with no names = %q, want no quoted name", bare)
	}
}

func TestFilterMatch(t *testing.T) {
	dev := Info{VendorID: 0x35CA, ProductID: 0x1201, LocationID: 0x02130000}
	for _, tc := range []struct {
		name string
		f    Filter
		want bool
	}{
		{"zero filter matches all", Filter{}, true},
		{"vendor match", Filter{VendorID: 0x35CA}, true},
		{"vendor mismatch", Filter{VendorID: 0x1234}, false},
		{"location match", Filter{LocationID: 0x02130000}, true},
		{"location mismatch", Filter{LocationID: 0x02110000}, false},
		{"product in list", Filter{ProductIDs: []uint16{0x1104, 0x1201}}, true},
		{"product not in list", Filter{ProductIDs: []uint16{0x1104}}, false},
		{"everything together", Filter{VendorID: 0x35CA, LocationID: 0x02130000, ProductIDs: []uint16{0x1201}}, true},
	} {
		if got := tc.f.Match(dev); got != tc.want {
			t.Errorf("%s: Match() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeviceAccessors(t *testing.T) {
	d := &Device{info: Info{VendorID: 1, ProductID: 2}}
	if d.Info().VendorID != 1 {
		t.Error("Info() lost the vendor")
	}
	if d.String() != d.info.String() {
		t.Error("Device.String() differs from Info.String()")
	}
	if d.Seized() {
		t.Error("a fresh device reports itself seized")
	}
}

func TestOpen(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := openDevice(t, f)
	if !d.open || d.Seized() {
		t.Fatalf("after Open: open=%v seized=%v, want true/false", d.open, d.Seized())
	}
	if err := d.Open(); err != nil { // idempotent
		t.Fatalf("second Open() = %v", err)
	}
	if len(f.openSeizes) != 1 {
		t.Errorf("Open called the seam %d times, want 1 (the second must be a no-op)", len(f.openSeizes))
	}
}

func TestOpenSeize(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := &Device{ref: 1}
	if err := d.OpenSeize(); err != nil {
		t.Fatalf("OpenSeize() = %v", err)
	}
	if !d.Seized() {
		t.Error("Seized() = false after OpenSeize")
	}
	if len(f.openSeizes) != 1 || !f.openSeizes[0] {
		t.Errorf("seam got seize=%v, want [true]", f.openSeizes)
	}
}

// TestOpenFailure checks that a refused open leaves the device closed and names
// the operation that was actually attempted, which is how a reader tells a
// plain open apart from a seize in a log.
func TestOpenFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seize bool
		op    string
	}{
		{"plain", false, "USBDeviceOpen"},
		{"seize", true, "USBDeviceOpenSeize"},
	} {
		f := &fakes{openCode: ioreturn.ExclusiveAccess}
		withFakes(t, f)
		d := &Device{ref: 1}
		var err error
		if tc.seize {
			err = d.OpenSeize()
		} else {
			err = d.Open()
		}
		var ioe *IOError
		if !errors.As(err, &ioe) {
			t.Fatalf("%s: Open = %v, want *IOError", tc.name, err)
		}
		if ioe.Op != tc.op {
			t.Errorf("%s: Op = %q, want %q", tc.name, ioe.Op, tc.op)
		}
		if d.open || d.Seized() {
			t.Errorf("%s: a failed open left the device marked open", tc.name)
		}
	}
}

func TestClose(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := openDevice(t, f)
	if err := d.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if len(f.released) != 1 || f.released[0] != 1 {
		t.Errorf("released = %v, want [1]", f.released)
	}
	if err := d.Close(); err != nil { // idempotent
		t.Fatalf("second Close() = %v", err)
	}
	if len(f.released) != 1 {
		t.Errorf("the second Close released again: %v", f.released)
	}
}

// TestCloseNeverOpened proves a device that was enumerated but never opened is
// still released, which is the common path for every device a probe lists and
// does not touch.
func TestCloseNeverOpened(t *testing.T) {
	f := &fakes{closeCode: ioreturn.NotOpen}
	withFakes(t, f)

	d := &Device{ref: 4}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() on an unopened device = %v, want nil", err)
	}
	if len(f.released) != 1 {
		t.Errorf("released = %v, want the reference back", f.released)
	}
}

func TestCloseReportsFailure(t *testing.T) {
	f := &fakes{closeCode: ioreturn.NoDevice}
	withFakes(t, f)

	d := openDevice(t, f)
	if err := d.Close(); err == nil {
		t.Fatal("Close() = nil, want the IOReturn reported")
	}
	// Even a failing close must give the reference back, or the handle leaks.
	if len(f.released) != 1 {
		t.Errorf("released = %v, want the reference back anyway", f.released)
	}
}

func TestControlNotOpen(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := &Device{ref: 1}
	if _, err := d.Control(Setup{}, nil, 0); !errors.Is(err, ErrNotOpen) {
		t.Errorf("Control on a closed device = %v, want ErrNotOpen", err)
	}
}

func TestControlTooLong(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := openDevice(t, f)
	if _, err := d.Control(Setup{}, make([]byte, maxTransfer+1), 0); !errors.Is(err, ErrTooLong) {
		t.Errorf("Control with an oversized buffer = %v, want ErrTooLong", err)
	}
	// The seam must not have been reached: an unsendable request is rejected
	// before it touches the device.
	if len(f.setups) != 0 {
		t.Errorf("the oversized request reached the seam: %v", f.setups)
	}
}

func TestControlDefaultsTheTimeout(t *testing.T) {
	f := &fakes{}
	withFakes(t, f)

	d := openDevice(t, f)
	for _, given := range []time.Duration{0, -time.Second} {
		if _, err := d.Control(Setup{}, nil, given); err != nil {
			t.Fatalf("Control(timeout=%v) = %v", given, err)
		}
	}
	for i, got := range f.timeouts {
		if got != defaultTimeout {
			t.Errorf("call %d got timeout %v, want the default %v", i, got, defaultTimeout)
		}
	}
	// A caller-supplied timeout must survive untouched.
	if _, err := d.Control(Setup{}, nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := f.timeouts[len(f.timeouts)-1]; got != 5*time.Second {
		t.Errorf("explicit timeout became %v", got)
	}
}

// TestControlClampsCount pins the two ways a native layer could report a byte
// count the caller's buffer cannot back. Neither should ever happen; both would
// slice out of range one level up if they did.
func TestControlClampsCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"negative", -3, 0},
		{"beyond the buffer", 99, 8},
	} {
		f := &fakes{ctlN: tc.got}
		withFakes(t, f)
		d := openDevice(t, f)
		n, err := d.Control(Setup{}, make([]byte, 8), 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if n != tc.want {
			t.Errorf("%s: Control returned %d, want it clamped to %d", tc.name, n, tc.want)
		}
	}
}

// TestControlReportsBytesWithError is the property a probe depends on: when a
// transfer fails part-way, how much arrived is the finding, so the count must
// come back alongside the error rather than be thrown away.
func TestControlReportsBytesWithError(t *testing.T) {
	f := &fakes{ctlN: 3, ctlCode: ioreturn.USBTransactionTimeout}
	withFakes(t, f)

	d := openDevice(t, f)
	n, err := d.Control(Setup{}, make([]byte, 8), 0)
	if n != 3 {
		t.Errorf("Control returned %d bytes, want 3 reported despite the error", n)
	}
	var ioe *IOError
	if !errors.As(err, &ioe) || ioe.Code != ioreturn.USBTransactionTimeout {
		t.Errorf("Control error = %v, want the timeout code", err)
	}
}

func TestDescriptor(t *testing.T) {
	f := &fakes{ctlN: 4, fill: []byte{0x12, 0x01, 0x10, 0x02, 0xFF}}
	withFakes(t, f)

	d := openDevice(t, f)
	b, err := d.Descriptor(DescDevice, 0, 0, 18)
	if err != nil {
		t.Fatalf("Descriptor() = %v", err)
	}
	// Exactly what the device sent, not the whole buffer.
	if want := []byte{0x12, 0x01, 0x10, 0x02}; string(b) != string(want) {
		t.Errorf("Descriptor() = % x, want % x", b, want)
	}
	if got := f.setups[0]; got.Request != ReqGetDescriptor || got.Value != 0x0100 {
		t.Errorf("Descriptor sent %v, want a GET_DESCRIPTOR for the device descriptor", got)
	}
	// The request must have asked for the caller's max, via wLength.
	if len(f.ctlData[0]) != 18 {
		t.Errorf("Descriptor asked for %d bytes, want 18", len(f.ctlData[0]))
	}
}

func TestDescriptorFailure(t *testing.T) {
	f := &fakes{ctlCode: ioreturn.USBPipeStalled}
	withFakes(t, f)

	d := openDevice(t, f)
	if _, err := d.Descriptor(DescDevice, 0, 0, 18); err == nil {
		t.Fatal("Descriptor() = nil error on a stall")
	}
}

func TestConfigDescriptor(t *testing.T) {
	f := &fakes{cfgBytes: []byte{9, 2, 9, 0, 1, 1, 0, 0xC0, 50}}
	withFakes(t, f)

	// Deliberately NOT opened: reading a cached descriptor must not need one.
	d := &Device{ref: 1}
	b, err := d.ConfigDescriptor(0)
	if err != nil {
		t.Fatalf("ConfigDescriptor() = %v", err)
	}
	if len(b) != 9 {
		t.Errorf("ConfigDescriptor() = %d bytes, want 9", len(b))
	}
}

func TestConfigDescriptorFailures(t *testing.T) {
	// An IOReturn failure surfaces as an IOError.
	f := &fakes{cfgCode: ioreturn.Unsupported}
	withFakes(t, f)
	if _, err := (&Device{ref: 1}).ConfigDescriptor(0); err == nil {
		t.Error("ConfigDescriptor() = nil error on kIOReturnUnsupported")
	}

	// A descriptor too short to carry its own header is rejected rather than
	// handed on to a parser that would read past its end.
	f2 := &fakes{cfgBytes: []byte{9, 2, 9}}
	withFakes(t, f2)
	if _, err := (&Device{ref: 1}).ConfigDescriptor(0); !errors.Is(err, ErrShortDescriptor) {
		t.Errorf("ConfigDescriptor(3 bytes) = %v, want ErrShortDescriptor", err)
	}
}

func TestDevices(t *testing.T) {
	f := &fakes{
		infos: []Info{
			{VendorID: 0x35CA, ProductID: 0x1201},
			{VendorID: 0x0C45, ProductID: 0x6368},
			{VendorID: 0x35CA, ProductID: 0x1102},
		},
		refs: []uintptr{10, 11, 12},
	}
	withFakes(t, f)

	devs, err := Devices(Filter{VendorID: 0x35CA})
	if err != nil {
		t.Fatalf("Devices() = %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("Devices() returned %d, want 2", len(devs))
	}
	// The device that did not match must have been released immediately, not
	// leaked until the process exits.
	if len(f.released) != 1 || f.released[0] != 11 {
		t.Errorf("released = %v, want [11]", f.released)
	}
}

func TestDevicesEnumerateError(t *testing.T) {
	f := &fakes{enumErr: ErrUnsupported}
	withFakes(t, f)

	if _, err := Devices(Filter{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Devices() = %v, want ErrUnsupported", err)
	}
}
