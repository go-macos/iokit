package ioreturn

import (
	"strings"
	"testing"
)

// TestCodeValues pins every constant to the packed word a C program printed
// from <IOKit/IOReturn.h> and <IOKit/usb/USB.h>. A decimal constant is
// unreadable, so a typo in one would otherwise be invisible until a probe
// misreported a failure. The hex here is the transcription check.
func TestCodeValues(t *testing.T) {
	for _, tc := range []struct {
		c    Code
		want uint32
	}{
		{Success, 0x00000000},
		{Err, 0xE00002BC},
		{NoMemory, 0xE00002BD},
		{NoResources, 0xE00002BE},
		{IPCError, 0xE00002BF},
		{NoDevice, 0xE00002C0},
		{NotPrivileged, 0xE00002C1},
		{BadArgument, 0xE00002C2},
		{LockedRead, 0xE00002C3},
		{LockedWrite, 0xE00002C4},
		{ExclusiveAccess, 0xE00002C5},
		{BadMessageID, 0xE00002C6},
		{Unsupported, 0xE00002C7},
		{VMError, 0xE00002C8},
		{InternalError, 0xE00002C9},
		{IOError, 0xE00002CA},
		{NotOpen, 0xE00002CD},
		{NotReadable, 0xE00002CE},
		{NotWritable, 0xE00002CF},
		{Busy, 0xE00002D5},
		{Timeout, 0xE00002D6},
		{Offline, 0xE00002D7},
		{NotAttached, 0xE00002D9},
		{NoInterrupt, 0xE00002DF},
		{NoFrames, 0xE00002E0},
		{NotPermitted, 0xE00002E2},
		{NoPower, 0xE00002E3},
		{Aborted, 0xE00002EB},
		{NotResponding, 0xE00002ED},
		{Invalid, 0xE0000001},
		{USBDevicePortWasNotSuspended, 0xE0004047},
		{USBClearPipeStallNotRecursive, 0xE0004048},
		{USBDeviceNotHighSpeed, 0xE0004049},
		{USBInterfaceNotFound, 0xE000404E},
		{USBPipeStalled, 0xE000404F},
		{USBTransactionReturned, 0xE0004050},
		{USBTransactionTimeout, 0xE0004051},
		{USBConfigNotFound, 0xE0004056},
		{USBEndpointNotFound, 0xE0004057},
		{USBNoAsyncPortErr, 0xE000405F},
		{USBTooManyPipesErr, 0xE0004060},
		{USBUnknownPipeErr, 0xE0004061},
	} {
		if got := uint32(tc.c); got != tc.want {
			t.Errorf("%s = 0x%08x, want 0x%08x", Name(tc.c), got, tc.want)
		}
	}
}

// TestNamesCoverEveryConstant guards the table against a constant that was
// added without its name, which would make Describe silently unhelpful.
func TestNamesCoverEveryConstant(t *testing.T) {
	if len(names) != 42 {
		t.Errorf("names has %d entries; add the new constant's name (and update this count)", len(names))
	}
	for c, n := range names {
		if !strings.HasPrefix(n, "kIOReturn") && !strings.HasPrefix(n, "kIOUSB") {
			t.Errorf("name for 0x%08x is %q, want a kIOReturn.../kIOUSB... identifier", uint32(c), n)
		}
	}
	// Every explained code must also be named, or Describe would emit a hex
	// rendering followed by prose, which reads as a bug.
	for c := range meanings {
		if _, ok := names[c]; !ok {
			t.Errorf("0x%08x has a meaning but no name", uint32(c))
		}
	}
}

func TestName(t *testing.T) {
	if got := Name(NotPermitted); got != "kIOReturnNotPermitted" {
		t.Errorf("Name(NotPermitted) = %q", got)
	}
	// An unknown word is not an error: another IOKit family may define it.
	if got := Name(Code(-1)); got != "IOReturn(0xffffffff)" {
		t.Errorf("Name(-1) = %q, want the hex rendering", got)
	}
}

func TestDescribe(t *testing.T) {
	// A code with a meaning gets the sentence appended.
	got := Describe(USBPipeStalled)
	if !strings.HasPrefix(got, "kIOUSBPipeStalled: ") || !strings.Contains(got, "STALL") {
		t.Errorf("Describe(USBPipeStalled) = %q", got)
	}
	// A named code without a meaning is just its name.
	if got := Describe(NoMemory); got != "kIOReturnNoMemory" {
		t.Errorf("Describe(NoMemory) = %q, want the bare name", got)
	}
	// An unnamed code falls back to hex.
	if got := Describe(Code(-2)); got != "IOReturn(0xfffffffe)" {
		t.Errorf("Describe(-2) = %q", got)
	}
}

func TestCodeString(t *testing.T) {
	got := ExclusiveAccess.String()
	for _, want := range []string{"0xe00002c5", "kIOReturnExclusiveAccess", "kernel driver"} {
		if !strings.Contains(got, want) {
			t.Errorf("Code.String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestIsSuccess(t *testing.T) {
	if !IsSuccess(Success) {
		t.Error("IsSuccess(Success) = false")
	}
	if IsSuccess(NoDevice) {
		t.Error("IsSuccess(NoDevice) = true")
	}
}
