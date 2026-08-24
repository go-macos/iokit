// Command serialprobe listens to a serial port, and on request speaks the
// VITURE MCU protocol to it.
//
// It is the serial package's dogfood, and like its sibling usbprobe it is built
// so the dangerous half is opt-in:
//
//   - with no flags it lists the ports macOS publishes. It opens nothing.
//   - -selftest allocates a pseudo-terminal and reads bytes it wrote itself.
//     This is the control for the instrument: a reader that cannot be shown to
//     read cannot be used to prove a device is mute. Run it first. Every other
//     mode runs it implicitly and refuses to continue if it fails, because
//     "the port said nothing" is a finding only when the alternative
//     explanation has been eliminated.
//   - -listen opens a port and reads. It writes nothing at all, which makes it
//     the cheapest question to ask of unknown hardware: some firmware streams
//     unprompted.
//   - -write and -imu WRITE. They are never implied by another flag.
//   - -sweep is -listen and -imu run across a matrix of line rates, both
//     device nodes and both modem-line states, because "it did not answer" is
//     only worth recording once the plausible settings have been tried.
package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-macos/iokit/serial"
	"github.com/go-macos/iokit/viture"
)

// osExit is a seam so run's exit path stays testable.
var osExit = os.Exit

func main() {
	if err := run(os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "serialprobe:", err)
		osExit(1)
	}
}

// sweepBauds are the rates worth trying on a USB CDC port whose firmware never
// documented one. A USB CDC device ignores the rate at the wire level -- there
// is no UART -- but its firmware is free to treat SET_LINE_CODING as a mode
// switch, and several do.
var sweepBauds = []int{115200, 921600, 1000000, 3000000}

// options are the parsed command line.
type options struct {
	port     string
	baud     int
	listen   time.Duration
	settle   time.Duration
	imu      bool
	write    string
	dtr      bool
	rts      bool
	noCLocal bool
	selftest bool
	sweep    bool
	bufSize  int
}

func run(w io.Writer, args []string) error {
	var o options
	fs := flag.NewFlagSet("serialprobe", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&o.port, "port", "", "device path or base name (e.g. /dev/cu.usbmodem14201, or usbmodem14201)")
	fs.IntVar(&o.baud, "baud", 115200, "line rate")
	fs.DurationVar(&o.listen, "listen", 0, "read for this long, writing nothing")
	fs.DurationVar(&o.settle, "settle", 300*time.Millisecond, "pause after asserting the modem lines, before reading")
	fs.BoolVar(&o.imu, "imu", false, "WRITE the VITURE IMU-enable packet, then listen for a reply")
	fs.StringVar(&o.write, "write", "", "WRITE these hex bytes, then listen for a reply")
	fs.BoolVar(&o.dtr, "dtr", false, "assert DTR (a /dev/cu.* node leaves it low)")
	fs.BoolVar(&o.rts, "rts", false, "assert RTS")
	fs.BoolVar(&o.noCLocal, "no-clocal", false, "do NOT set CLOCAL, so the driver waits for carrier detect")
	fs.BoolVar(&o.selftest, "selftest", false, "run only the loopback control and report whether the reader works")
	fs.BoolVar(&o.sweep, "sweep", false, "try the matrix of rates, nodes and modem-line states")
	fs.IntVar(&o.bufSize, "buffer", 64<<10, "read buffer size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if o.selftest {
		return selftest(w)
	}
	if o.port == "" {
		return list(w)
	}
	// Anything that touches hardware runs the control first. A silent port is
	// evidence only against a reader that has just been shown to read.
	if err := selftest(w); err != nil {
		return fmt.Errorf("loopback control failed, so nothing this run reports about a device would mean anything: %w", err)
	}
	fmt.Fprintln(w)
	if o.sweep {
		return sweep(w, o)
	}
	return once(w, o, resolve(o.port, ""), o.baud, o.dtr, o.rts)
}

// resolve turns a port argument into a device path. A base name is expanded
// with the given prefix, defaulting to the call-out node.
func resolve(port, prefix string) string {
	if strings.HasPrefix(port, "/") {
		if prefix == "" {
			return port
		}
		return serial.DevDir + "/" + prefix + serial.BaseName(port)
	}
	if prefix == "" {
		prefix = serial.CalloutPrefix
	}
	return serial.DevDir + "/" + prefix + serial.BaseName(port)
}

// list prints the ports macOS publishes and which of the two nodes exist.
func list(w io.Writer) error {
	names, err := serial.List()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "== %d serial port(s) ==\n", len(names))
	for _, n := range names {
		fmt.Fprintf(w, "   %-28s %s   %s\n", n, serial.Callout(n), serial.Dialin(n))
	}
	if len(names) == 0 {
		fmt.Fprintln(w, "   (none)")
	}
	return nil
}

// selftest proves the reader. It opens a pseudo-terminal, writes a known
// pattern into the master, reads it back through a real serial.Port on the
// slave, and then checks that a read with nothing to read times out instead of
// hanging.
//
// Both halves matter. The first says a byte that arrives is seen; the second
// says the absence of a byte is reported rather than waited on forever. A probe
// missing either one cannot tell a mute device from a broken instrument.
func selftest(w io.Writer) error {
	fmt.Fprintln(w, "== loopback control ==")
	master, slave, err := serial.Loopback()
	if err != nil {
		return fmt.Errorf("allocating a pseudo-terminal: %w", err)
	}
	defer master.Close()

	p, err := serial.Open(slave, serial.Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	if err != nil {
		return fmt.Errorf("opening the loopback slave %s: %w", slave, err)
	}
	defer p.Close()
	fmt.Fprintf(w, "   pty            %s\n", slave)
	fmt.Fprintf(w, "   applied        %s\n", p.Config())

	want := []byte{0xFF, 0xFC, 0x00, 0x01, 'l', 'o', 'o', 'p', 0x03}
	if _, err := master.Write(want); err != nil {
		return fmt.Errorf("writing to the pty master: %w", err)
	}
	buf := make([]byte, 64)
	n, err := serial.Drain(p, buf, time.Now().Add(2*time.Second))
	if err != nil {
		return fmt.Errorf("reading the loopback: %w", err)
	}
	if n == 0 {
		return errors.New("read nothing back from a port we had just written to: the reader is broken")
	}
	fmt.Fprintf(w, "   read back      %d byte(s): %s\n", n, hex.EncodeToString(buf[:n]))

	start := time.Now()
	n2, err := serial.Drain(p, buf, time.Now().Add(200*time.Millisecond))
	if err != nil {
		return fmt.Errorf("the idle read failed instead of timing out: %w", err)
	}
	if n2 != 0 {
		return fmt.Errorf("the idle read returned %d unexpected byte(s)", n2)
	}
	waited := time.Since(start)
	if waited < 150*time.Millisecond {
		return fmt.Errorf("the idle read returned after only %v: the deadline is not being honoured", waited)
	}
	fmt.Fprintf(w, "   idle read      timed out after %v, as it should\n", waited.Round(time.Millisecond))
	fmt.Fprintln(w, "   VERDICT        the reader reads, and reports silence instead of hanging")
	return nil
}

// once runs one configuration: open, configure, optionally raise the modem
// lines, listen, optionally write, listen again.
func once(w io.Writer, o options, path string, baud int, dtr, rts bool) error {
	cfg := serial.Config{Baud: baud, CLOCAL: !o.noCLocal, ReadMin: 1}
	fmt.Fprintf(w, "== %s @ %d, DTR=%v RTS=%v CLOCAL=%v ==\n", path, baud, dtr, rts, cfg.CLOCAL)

	p, err := serial.Open(path, cfg)
	if err != nil {
		fmt.Fprintf(w, "   open           FAILED: %v\n", err)
		return nil
	}
	defer p.Close()
	fmt.Fprintf(w, "   applied        %s\n", p.Config())
	if p.Config().Baud != baud {
		fmt.Fprintf(w, "   NOTE           the driver did not take %d baud; it is running at %d\n", baud, p.Config().Baud)
	}
	if l, err := p.Lines(); err == nil {
		fmt.Fprintf(w, "   lines          %s\n", l)
	} else {
		fmt.Fprintf(w, "   lines          unreadable: %v\n", err)
	}

	var set, clear serial.Lines
	for _, x := range []struct {
		on  bool
		bit serial.Lines
	}{{dtr, serial.LineDTR}, {rts, serial.LineRTS}} {
		if x.on {
			set |= x.bit
		} else {
			clear |= x.bit
		}
	}
	if err := p.SetLines(set, clear); err != nil {
		fmt.Fprintf(w, "   set lines      FAILED: %v\n", err)
	} else if l, err := p.Lines(); err == nil {
		fmt.Fprintf(w, "   lines now      %s\n", l)
	}
	if o.settle > 0 {
		time.Sleep(o.settle)
	}
	_ = p.Flush()

	buf := make([]byte, o.bufSize)
	if o.listen > 0 {
		fmt.Fprintf(w, "   listening      %v, sending nothing\n", o.listen)
		n, err := serial.Drain(p, buf, time.Now().Add(o.listen))
		report(w, "unprompted", buf[:n], n, err)
	}

	payload, err := outbound(o)
	if err != nil {
		return err
	}
	if payload == nil {
		return nil
	}
	fmt.Fprintf(w, "   writing        %s\n", hex.EncodeToString(payload))
	m, err := p.Write(payload)
	if err != nil {
		fmt.Fprintf(w, "   write          FAILED after %d byte(s): %v\n", m, err)
		return nil
	}
	fmt.Fprintf(w, "   write          %d byte(s) queued (which proves nothing about the device)\n", m)

	wait := o.listen
	if wait <= 0 {
		wait = 2 * time.Second
	}
	n, err := serial.Drain(p, buf, time.Now().Add(wait))
	report(w, "reply", buf[:n], n, err)
	return nil
}

// outbound builds the bytes to send, or nil if this run only listens.
func outbound(o options) ([]byte, error) {
	switch {
	case o.write != "":
		b, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "", "-", "").Replace(o.write))
		if err != nil {
			return nil, fmt.Errorf("-write is not hex: %w", err)
		}
		return b, nil
	case o.imu:
		return viture.EnableIMU(true, 0), nil
	}
	return nil, nil
}

// report prints what came back: the raw bytes first, always, and only then any
// interpretation. Showing the hex before the decode is deliberate -- a decoder
// that finds structure in noise is the classic way to invent a result.
func report(w io.Writer, what string, b []byte, n int, err error) {
	if err != nil {
		fmt.Fprintf(w, "   %-14s read error after %d byte(s): %v\n", what, n, err)
		return
	}
	if n == 0 {
		fmt.Fprintf(w, "   %-14s NOTHING\n", what)
		return
	}
	fmt.Fprintf(w, "   %-14s %d byte(s)\n", what, n)
	for i := 0; i < len(b); i += 16 {
		j := min(i+16, len(b))
		fmt.Fprintf(w, "     %04x  %s\n", i, hex.EncodeToString(b[i:j]))
	}
	decode(w, b)
}

// decode looks for VITURE framing in the bytes, and says so only when the CRC
// agrees.
func decode(w io.Writer, b []byte) {
	found := 0
	for i := 0; i+viture.MinPacket <= len(b); i++ {
		if b[i] != 0xFF {
			continue
		}
		for end := i + viture.MinPacket; end <= len(b); end++ {
			if !viture.Valid(b[i:end]) {
				continue
			}
			found++
			fmt.Fprintf(w, "     +%04x  valid VITURE packet, %d byte(s)\n", i, end-i)
			if e, err := viture.ParseIMU(b[i:end]); err == nil {
				fmt.Fprintf(w, "            euler Z=%.3f X=%.3f Y=%.3f\n", e.Z, e.X, e.Y)
			}
			break
		}
	}
	if found == 0 {
		fmt.Fprintln(w, "     (no byte range in there passes the VITURE CRC: this is not the classic protocol)")
	}
}

// sweep runs the matrix. It is deliberately exhaustive rather than clever: the
// point of the exercise is to be able to say afterwards exactly what was tried.
func sweep(w io.Writer, o options) error {
	listen := o.listen
	if listen <= 0 {
		listen = 2 * time.Second
	}
	o.listen = listen
	for _, prefix := range []string{serial.CalloutPrefix, serial.DialinPrefix} {
		path := resolve(o.port, prefix)
		for _, baud := range sweepBauds {
			for _, lines := range []struct{ dtr, rts bool }{{false, false}, {true, true}} {
				if err := once(w, o, path, baud, lines.dtr, lines.rts); err != nil {
					return err
				}
				fmt.Fprintln(w)
			}
		}
	}
	return nil
}
