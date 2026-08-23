//go:build !darwin

package serial

import (
	"errors"
	"testing"
)

// TestStubsAreWiredAndUnsupported checks the non-darwin build's promise: every
// seam is assigned, so no entry point panics on a nil func, and every one
// reports a clean unsupported rather than a plausible-looking zero value.
func TestStubsAreWiredAndUnsupported(t *testing.T) {
	if _, err := openPort("/dev/ttyS0", false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("openPort = %v, want ErrUnsupported", err)
	}
	if err := closeFD(0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("closeFD = %v", err)
	}
	if _, err := getAttr(0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("getAttr = %v", err)
	}
	if err := setAttr(0, Termios{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("setAttr = %v", err)
	}
	if err := setSpeed(0, 9600); !errors.Is(err, ErrUnsupported) {
		t.Errorf("setSpeed = %v", err)
	}
	if _, err := getLines(0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("getLines = %v", err)
	}
	if err := bisLines(0, LineDTR); !errors.Is(err, ErrUnsupported) {
		t.Errorf("bisLines = %v", err)
	}
	if err := bicLines(0, LineDTR); !errors.Is(err, ErrUnsupported) {
		t.Errorf("bicLines = %v", err)
	}
	if err := flushIO(0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("flushIO = %v", err)
	}
	if f := newFile(0, "x"); f != nil {
		t.Errorf("newFile = %v, want nil", f)
	}
	if _, _, err := Loopback(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Loopback = %v", err)
	}
}

// TestPublicAPIOnUnsupportedPlatform walks the API a consumer would call, to
// prove a Linux or Windows build gets errors rather than surprises.
func TestPublicAPIOnUnsupportedPlatform(t *testing.T) {
	if _, err := Open("/dev/ttyS0", Config{Baud: 115200}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Open = %v, want ErrUnsupported", err)
	}
}

// TestListReadsTheRealDirectory covers the stub's own listNames, which is a
// real directory read even where the rest of the package is unsupported.
//
// Whether it succeeds is a property of the platform, not of this package:
// Linux has a /dev and Windows does not, and both answers are correct. What is
// under test is that the seam reads a directory and reports what it finds.
func TestListReadsTheRealDirectory(t *testing.T) {
	names, err := List()
	t.Logf("List() = %v, %v", names, err)
	if _, err := listNames("/this/directory/does/not/exist"); err == nil {
		t.Error("listing a missing directory should fail")
	}
}
