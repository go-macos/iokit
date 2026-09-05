//go:build darwin

package usb

import (
	"testing"
	"time"
)

// msDuration spells a millisecond count as a Duration, so the table below reads
// as milliseconds rather than as nanosecond arithmetic.
func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// TestIfaceSeamsRejectUnknownTokens pins the interface seams' behaviour for a
// handle that is not in the registry -- the shape a use-after-close takes.
func TestIfaceSeamsRejectUnknownTokens(t *testing.T) {
	const bogus = ^uintptr(0)
	if got := darwinOpenIface(bogus, false); got == 0 {
		t.Errorf("darwinOpenIface(bogus) = %v, want a failure code", got)
	}
	if got := darwinCloseIface(bogus); got == 0 {
		t.Errorf("darwinCloseIface(bogus) = %v, want a failure code", got)
	}
	if _, got := darwinPipes(bogus); got == 0 {
		t.Errorf("darwinPipes(bogus) = %v, want a failure code", got)
	}
	if _, _, got := darwinReadPipe(bogus, 1, make([]byte, 4), 0); got == 0 {
		t.Errorf("darwinReadPipe(bogus) = %v, want a failure code", got)
	}
	if _, got := darwinWritePipe(bogus, 1, []byte{1}, 0); got == 0 {
		t.Errorf("darwinWritePipe(bogus) = %v, want a failure code", got)
	}
}

// TestReadPipeRejectsAnEmptyBuffer covers the guard that keeps a zero-length
// read from being handed to IOUSBLib, which would have no buffer to fill and no
// way to say so.
func TestReadPipeRejectsAnEmptyBuffer(t *testing.T) {
	n := &native{}
	tok := registryAdd(n)
	t.Cleanup(func() { registryDrop(tok) })
	n.dev = nil
	if _, _, code := darwinReadPipe(tok, 1, nil, 0); code == 0 {
		t.Error("a nil buffer should be refused")
	}
}

func TestTimeoutMS(t *testing.T) {
	for _, tc := range []struct {
		ms   int64
		want uint32
	}{{0, 1}, {-5, 1}, {250, 250}, {1 << 40, ^uint32(0)}} {
		if got := timeoutMS(msDuration(tc.ms)); got != tc.want {
			t.Errorf("timeoutMS(%dms) = %d, want %d", tc.ms, got, tc.want)
		}
	}
}

// TestOnDeviceInterfaceEnumeration talks to the machine's real USB bus through
// the interface half of the binding. Like its device-level sibling it is
// written to be meaningful on a developer's machine and harmless on a CI runner
// with no USB device at all. It opens nothing: claiming an interface is exactly
// what disturbs a driver, and enumeration alone already proves the CFPlugIn
// handshake and the registry property reads.
func TestOnDeviceInterfaceEnumeration(t *testing.T) {
	ifs, err := Interfaces(InterfaceFilter{})
	if err != nil {
		t.Fatalf("Interfaces() = %v", err)
	}
	defer func() {
		for _, i := range ifs {
			i.Close()
		}
	}()
	t.Logf("%d USB interface(s) on this machine", len(ifs))
	if len(ifs) == 0 {
		t.Skip("no USB interfaces: nothing to assert against")
	}
	described := 0
	for _, i := range ifs {
		t.Logf("  %s", i)
		if i.Info().VendorID != 0 {
			described++
		}
	}
	if described == 0 {
		t.Error("not one interface reported a vendor ID: the registry property reads are broken")
	}
}
