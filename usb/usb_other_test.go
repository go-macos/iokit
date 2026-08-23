//go:build !darwin

package usb

import (
	"errors"
	"testing"
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

// TestStubsAreWiredAndUnsupported checks the non-darwin build's promise: every
// seam is assigned, so no entry point panics on a nil func, and every one
// reports a clean unsupported rather than a plausible-looking zero value.
func TestStubsAreWiredAndUnsupported(t *testing.T) {
	if _, _, err := enumerate(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("enumerate() = %v, want ErrUnsupported", err)
	}
	for name, got := range map[string]ioreturn.Code{
		"openDev":    openDev(0, false),
		"closeDev":   closeDev(0),
		"configDesc": func() ioreturn.Code { _, c := configDesc(0, 0); return c }(),
		"control":    func() ioreturn.Code { _, c := control(0, Setup{}, nil, time.Second); return c }(),
	} {
		if got != ioreturn.Unsupported {
			t.Errorf("%s = %v, want kIOReturnUnsupported", name, got)
		}
	}
	releaseRef(0) // must not panic
}

// TestPublicAPIOnUnsupportedPlatform walks the API a consumer would call, to
// prove a Linux or Windows build gets errors rather than surprises.
func TestPublicAPIOnUnsupportedPlatform(t *testing.T) {
	if _, err := Devices(Filter{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Devices() = %v, want ErrUnsupported", err)
	}
	d := &Device{ref: 1}
	if err := d.Open(); err == nil {
		t.Error("Open() = nil on an unsupported platform")
	}
	if _, err := d.ConfigDescriptor(0); err == nil {
		t.Error("ConfigDescriptor() = nil on an unsupported platform")
	}
}
