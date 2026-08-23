package main

import (
	"errors"
	"testing"

	"github.com/go-macos/iokit/ioreturn"
	"github.com/go-macos/iokit/usb"
)

func TestFindPipe(t *testing.T) {
	pipes := []usb.Pipe{
		{Ref: 1, Number: 3, Dir: usb.PipeOut, Type: usb.TransferBulk},
		{Ref: 2, Number: 3, Dir: usb.PipeIn, Type: usb.TransferBulk},
	}
	if p, ok := findPipe(pipes, 0x83); !ok || p.Ref != 2 {
		t.Errorf("findPipe(0x83) = %+v, %v", p, ok)
	}
	if p, ok := findPipe(pipes, 0x03); !ok || p.Ref != 1 {
		t.Errorf("findPipe(0x03) = %+v, %v", p, ok)
	}
	if _, ok := findPipe(pipes, 0x81); ok {
		t.Error("findPipe found an endpoint the interface does not have")
	}
}

// TestIsTimeout is what keeps a polling loop running. A NAKed bulk IN is the
// ordinary answer of an idle pipe, and treating it as a failure would end the
// listen on the first empty poll.
func TestIsTimeout(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":            {nil, false},
		"plain":          {errors.New("boom"), false},
		"usb timeout":    {&usb.IOError{Op: "ReadPipeTO", Code: ioreturn.USBTransactionTimeout}, true},
		"kernel timeout": {&usb.IOError{Op: "ReadPipeTO", Code: ioreturn.Timeout}, true},
		"stall":          {&usb.IOError{Op: "ReadPipeTO", Code: ioreturn.USBPipeStalled}, false},
		"exclusive":      {&usb.IOError{Op: "USBInterfaceOpen", Code: ioreturn.ExclusiveAccess}, false},
	} {
		if got := isTimeout(tc.err); got != tc.want {
			t.Errorf("%s: isTimeout = %v, want %v", name, got, tc.want)
		}
	}
}

func TestResult(t *testing.T) {
	if got := result(nil); got != "ACCEPTED" {
		t.Errorf("result(nil) = %q", got)
	}
	if got := result(errors.New("stalled")); got != "stalled" {
		t.Errorf("result(err) = %q", got)
	}
}
