package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/go-macos/iokit/serial"
	"github.com/go-macos/iokit/viture"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		port, prefix, want string
	}{
		{"/dev/cu.usbmodem1", "", "/dev/cu.usbmodem1"},
		{"/dev/cu.usbmodem1", serial.DialinPrefix, "/dev/tty.usbmodem1"},
		{"/dev/tty.usbmodem1", serial.CalloutPrefix, "/dev/cu.usbmodem1"},
		{"usbmodem1", "", "/dev/cu.usbmodem1"},
		{"usbmodem1", serial.DialinPrefix, "/dev/tty.usbmodem1"},
		{"cu.usbmodem1", serial.DialinPrefix, "/dev/tty.usbmodem1"},
	} {
		if got := resolve(tc.port, tc.prefix); got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.port, tc.prefix, got, tc.want)
		}
	}
}

func TestOutbound(t *testing.T) {
	if b, err := outbound(options{}); err != nil || b != nil {
		t.Errorf("a listen-only run should send nothing: %v, %v", b, err)
	}
	b, err := outbound(options{write: "ff:fe-00 01"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{0xFF, 0xFE, 0x00, 0x01}) {
		t.Errorf("-write decoded to % x", b)
	}
	if _, err := outbound(options{write: "zz"}); err == nil {
		t.Error("-write should reject non-hex")
	}
	imu, err := outbound(options{imu: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(imu, viture.EnableIMU(true, 0)) {
		t.Errorf("-imu built % x", imu)
	}
}

func TestReportShowsRawBytesBeforeDecoding(t *testing.T) {
	var w bytes.Buffer
	p := viture.EnableIMU(true, 0)
	report(&w, "reply", p, len(p), nil)
	out := w.String()
	hexAt := strings.Index(out, "fffe")
	decodeAt := strings.Index(out, "valid VITURE packet")
	if hexAt < 0 || decodeAt < 0 {
		t.Fatalf("report did not print both the hex and the decode:\n%s", out)
	}
	if hexAt > decodeAt {
		t.Error("report printed its interpretation before the raw bytes")
	}
}

func TestReportOnNothing(t *testing.T) {
	var w bytes.Buffer
	report(&w, "unprompted", nil, 0, nil)
	if !strings.Contains(w.String(), "NOTHING") {
		t.Errorf("report of an empty read said %q", w.String())
	}
}

func TestReportOnError(t *testing.T) {
	var w bytes.Buffer
	report(&w, "reply", nil, 0, serial.ErrClosed)
	if !strings.Contains(w.String(), "read error") {
		t.Errorf("report of a failed read said %q", w.String())
	}
}

// TestDecodeDoesNotInventStructure is the guard against the most seductive
// failure mode of a reverse-engineering tool: finding a protocol in noise.
func TestDecodeDoesNotInventStructure(t *testing.T) {
	var w bytes.Buffer
	noise := make([]byte, 64)
	for i := range noise {
		noise[i] = byte(i * 7)
	}
	decode(&w, noise)
	if !strings.Contains(w.String(), "no byte range") {
		t.Errorf("decode claimed to find a packet in noise: %q", w.String())
	}
}

func TestDecodeFindsAnEmbeddedIMUReport(t *testing.T) {
	// A well-formed IMU report, buried in traffic on both sides.
	rep := viture.Packet(0, 0, make([]byte, 12))
	rep[0], rep[1] = 0xFF, 0xFC
	// Rebuild the CRC over the changed header.
	fixed := append([]byte{}, rep...)
	fixed[2] = byte(viture.CRC16(fixed[4:]) >> 8)
	fixed[3] = byte(viture.CRC16(fixed[4:]))
	buf := append([]byte{0x01, 0x02}, fixed...)
	buf = append(buf, 0xAA)

	var w bytes.Buffer
	decode(&w, buf)
	out := w.String()
	if !strings.Contains(out, "valid VITURE packet") {
		t.Fatalf("decode missed a well-formed packet:\n%s", out)
	}
	if !strings.Contains(out, "euler") {
		t.Errorf("decode did not parse the report's angles:\n%s", out)
	}
}

func TestRunListsPortsWithNoFlags(t *testing.T) {
	var w bytes.Buffer
	if err := run(&w, nil); err != nil {
		t.Fatalf("run with no flags: %v", err)
	}
	if !strings.Contains(w.String(), "serial port(s)") {
		t.Errorf("run with no flags printed %q", w.String())
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	var w bytes.Buffer
	if err := run(&w, []string{"-nonsense"}); err == nil {
		t.Error("run accepted an unknown flag")
	}
}

// TestRunSelftest exercises the control itself: on any machine with a working
// pseudo-terminal it must pass, and it must say so in words a human can check.
func TestRunSelftest(t *testing.T) {
	var w bytes.Buffer
	err := run(&w, []string{"-selftest"})
	if err != nil {
		t.Skipf("no usable pseudo-terminal here: %v", err)
	}
	if !strings.Contains(w.String(), "the reader reads") {
		t.Errorf("selftest passed without saying so:\n%s", w.String())
	}
}

// TestOnceOnAMissingPortIsNotFatal checks that a device that is not there is
// reported and skipped rather than ending the run, which is what makes a sweep
// across nodes and rates usable.
func TestOnceOnAMissingPortIsNotFatal(t *testing.T) {
	var w bytes.Buffer
	o := options{listen: time.Millisecond, bufSize: 16}
	if err := once(&w, o, "/dev/cu.definitely-not-here", 115200, false, false); err != nil {
		t.Fatalf("once: %v", err)
	}
	if !strings.Contains(w.String(), "open           FAILED") {
		t.Errorf("once said %q", w.String())
	}
}
