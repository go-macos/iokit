//go:build darwin

package usb

import (
	"errors"
	"testing"
)

// TestDoLoadReportsFrameworkFailure reaches doLoad's error path through the
// dlopen seam, without needing a machine that lacks IOKit. It calls doLoad
// rather than load so the package's sync.Once is not burnt for the other tests.
func TestDoLoadReportsFrameworkFailure(t *testing.T) {
	sentinel := errors.New("no such framework")
	old := dlopen
	t.Cleanup(func() { dlopen = old })

	// Each of the three dlopen calls must be checked, so fail on each in turn.
	for i := 0; i < 3; i++ {
		calls := 0
		dlopen = func(path string) (uintptr, error) {
			calls++
			if calls > i {
				return 0, sentinel
			}
			return old(path)
		}
		if err := doLoad(); !errors.Is(err, sentinel) {
			t.Errorf("doLoad() with dlopen failing on call %d = %v, want the error", i+1, err)
		}
	}
	// Restore the real symbols for the tests that follow, since doLoad above
	// left the function pointers half-assigned.
	dlopen = old
	if err := doLoad(); err != nil {
		t.Fatalf("doLoad() with the real dlopen = %v", err)
	}
}

func TestRegistry(t *testing.T) {
	n := &native{service: 42}
	tok := registryAdd(n)
	if tok == 0 {
		t.Fatal("registryAdd returned the zero token, which means 'absent'")
	}
	if got := registryGet(tok); got != n {
		t.Errorf("registryGet(%d) = %v, want the entry back", tok, got)
	}
	if got := registryDrop(tok); got != n {
		t.Errorf("registryDrop(%d) = %v, want the entry", tok, got)
	}
	if got := registryGet(tok); got != nil {
		t.Errorf("registryGet after drop = %v, want nil", got)
	}
	if got := registryDrop(tok); got != nil {
		t.Errorf("registryDrop twice = %v, want nil", got)
	}
	// A token that was never issued must not resolve, which is the reason the
	// portable layer carries a token rather than a raw pointer.
	if got := registryGet(^uintptr(0)); got != nil {
		t.Errorf("registryGet(bogus) = %v, want nil", got)
	}
}

// TestSeamsRejectUnknownTokens pins the darwin seams' behaviour for a handle
// that is not in the registry -- the shape a use-after-close would take.
func TestSeamsRejectUnknownTokens(t *testing.T) {
	const bogus = ^uintptr(0)
	if got := darwinOpen(bogus, false); got.String() == "" || got == 0 {
		t.Errorf("darwinOpen(bogus) = %v, want a failure code", got)
	}
	if got := darwinClose(bogus); got == 0 {
		t.Errorf("darwinClose(bogus) = %v, want a failure code", got)
	}
	if _, got := darwinControl(bogus, Setup{}, nil, 0); got == 0 {
		t.Errorf("darwinControl(bogus) = %v, want a failure code", got)
	}
	if _, got := darwinConfigDesc(bogus, 0); got == 0 {
		t.Errorf("darwinConfigDesc(bogus) = %v, want a failure code", got)
	}
	darwinRelease(bogus) // must not panic
}

func TestByteHelpers(t *testing.T) {
	b := make([]byte, 8)
	putU16(b, 0x1234)
	if got := u16(b); got != 0x1234 {
		t.Errorf("u16(putU16(0x1234)) = %#04x", got)
	}
	if b[0] != 0x34 || b[1] != 0x12 {
		t.Errorf("putU16 wrote % x, want little-endian 34 12", b[:2])
	}
	putU32(b[2:], 0xDEADBEEF)
	if got := u32(b[2:]); got != 0xDEADBEEF {
		t.Errorf("u32(putU32(0xdeadbeef)) = %#08x", got)
	}
	if b[2] != 0xEF || b[5] != 0xDE {
		t.Errorf("putU32 wrote % x, want little-endian ef be ad de", b[2:6])
	}
}

// TestOnDeviceEnumeration talks to the machine's real USB bus. It is the only
// test that proves the IOUSBLib binding works at all -- the vtable indices, the
// CFPlugIn handshake, the registry property reads -- and it is written to be
// meaningful on a developer's machine and harmless on a CI runner that may have
// no USB device at all.
func TestOnDeviceEnumeration(t *testing.T) {
	devs, err := Devices(Filter{})
	if err != nil {
		t.Fatalf("Devices() = %v", err)
	}
	t.Logf("%d USB device(s) on this machine", len(devs))
	if len(devs) == 0 {
		t.Skip("no USB devices: nothing to assert against")
	}
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()

	described := 0
	for _, d := range devs {
		i := d.Info()
		t.Logf("  %s", d)
		if i.VendorID != 0 {
			described++
		}
		// The cached configuration descriptor needs no open, so it must work on
		// every device, including ones a kernel driver holds exclusively.
		raw, err := d.ConfigDescriptor(0)
		if err != nil {
			t.Errorf("  %s: ConfigDescriptor = %v", d, err)
			continue
		}
		cfg, err := ParseConfig(raw)
		if err != nil {
			t.Errorf("  %s: ParseConfig(%d bytes) = %v", d, len(raw), err)
			continue
		}
		if len(cfg.Interfaces) == 0 {
			t.Errorf("  %s: configuration declares no interface", d)
		}
		t.Logf("     %d interface(s), %d uninterpreted descriptor(s)", len(cfg.Interfaces), cfg.Unknown)
	}
	if described == 0 {
		t.Error("not one device reported a vendor ID: the registry property reads are broken")
	}
}
