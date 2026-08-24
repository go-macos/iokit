// Command usbprobe inspects the machine's USB devices and, on request, talks to
// one over endpoint 0.
//
// It is the usb package's dogfood: everything it does goes through the public
// API. It is also a reverse-engineering instrument, and it is built so that the
// dangerous half is opt-in:
//
//   - with no flags it only reads. It enumerates, and it decodes the
//     configuration descriptor the kernel already cached, which needs no open
//     and cannot disturb a device someone is using.
//   - -control opens a device and issues the standard GET_DESCRIPTOR request.
//     This is the known-good control for the binding itself: a device
//     descriptor read back with the right vendor and product IDs proves the
//     control-transfer path works end to end, so that a later failure can be
//     blamed on the device rather than on this code.
//   - -scan sweeps vendor-defined requests looking for one the device answers.
//     It reads only: every request it sends is device-to-host.
//   - -imu WRITES to the device. It is the only flag that does, and it is never
//     implied by another.
package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-macos/iokit/ioreturn"
	"github.com/go-macos/iokit/usb"
	"github.com/go-macos/iokit/viture"
)

// osExit is a seam so run's exit path stays testable.
var osExit = os.Exit

func main() {
	if err := run(os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "usbprobe:", err)
		osExit(1)
	}
}

// options are the parsed command line.
type options struct {
	device    string
	control   bool
	scan      bool
	imu       bool
	filter    usb.Filter
	iface     int
	bulkRead  int
	bulkOut   int
	bulkWrite string
	bulkIMU   bool
	bulkFor   time.Duration
	cdc       bool
	cdcSet    int
	seize     bool
	timeout   time.Duration
	maxReq    int
}

func run(w io.Writer, args []string) error {
	var o options
	fs := flag.NewFlagSet("usbprobe", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&o.device, "device", "", "restrict to one device, as vendor:product in hex (e.g. 35ca:1201)")
	fs.BoolVar(&o.control, "control", false, "open each device and read its device descriptor (the binding's known-good control)")
	fs.BoolVar(&o.scan, "scan", false, "sweep vendor-defined device-to-host requests, looking for one the device answers")
	fs.BoolVar(&o.imu, "imu", false, "WRITE the VITURE IMU-enable packet to the device (the only flag that writes)")
	fs.IntVar(&o.iface, "iface", -1, "work on this interface number instead of the device (-1 for none)")
	fs.IntVar(&o.bulkRead, "bulk-read", 0, "poll this IN endpoint address for -bulk-for (e.g. 0x83)")
	fs.IntVar(&o.bulkOut, "bulk-out", 0, "OUT endpoint address that -bulk-write and -bulk-imu use (e.g. 0x03)")
	fs.StringVar(&o.bulkWrite, "bulk-write", "", "WRITE these hex bytes to the -bulk-out endpoint")
	fs.BoolVar(&o.bulkIMU, "bulk-imu", false, "WRITE the VITURE IMU-enable packet to the -bulk-out endpoint")
	fs.DurationVar(&o.bulkFor, "bulk-for", 5*time.Second, "how long -bulk-read keeps polling")
	fs.BoolVar(&o.cdc, "cdc", false, "read the CDC line coding straight from the device, bypassing the kernel serial driver")
	fs.IntVar(&o.cdcSet, "cdc-set", 0, "WRITE this line rate with CDC SET_LINE_CODING and read it back")
	fs.BoolVar(&o.seize, "seize", false, "take the device from its current owner if a plain open is refused")
	fs.DurationVar(&o.timeout, "timeout", 250*time.Millisecond, "per-transfer timeout")
	fs.IntVar(&o.maxReq, "max-request", 0xFF, "highest bRequest the sweep tries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	filter, err := parseFilter(o.device)
	if err != nil {
		return err
	}
	o.filter = filter
	if o.iface >= 0 {
		return ifaceMode(w, o)
	}
	devs, err := usb.Devices(filter)
	if err != nil {
		return err
	}
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()

	fmt.Fprintf(w, "== %d USB device(s) ==\n", len(devs))
	for _, d := range devs {
		fmt.Fprintf(w, "\n%s\n", d)
		report(w, d, o)
	}
	if len(devs) == 0 && o.device != "" {
		// A CI runner legitimately has no USB device at all, so an empty bus is
		// not a failure. A -device that matched nothing is.
		return fmt.Errorf("no USB device matched -device %s", o.device)
	}
	return nil
}

// parseFilter turns a "vendor:product" string into a usb.Filter. An empty
// string matches everything.
func parseFilter(s string) (usb.Filter, error) {
	if s == "" {
		return usb.Filter{}, nil
	}
	vid, pid, ok := strings.Cut(s, ":")
	if !ok {
		return usb.Filter{}, fmt.Errorf("bad -device %q: want vendor:product in hex", s)
	}
	v, err := strconv.ParseUint(vid, 16, 16)
	if err != nil {
		return usb.Filter{}, fmt.Errorf("bad -device vendor %q: %w", vid, err)
	}
	p, err := strconv.ParseUint(pid, 16, 16)
	if err != nil {
		return usb.Filter{}, fmt.Errorf("bad -device product %q: %w", pid, err)
	}
	return usb.Filter{VendorID: uint16(v), ProductIDs: []uint16{uint16(p)}}, nil
}

// report runs every phase the options asked for against one device.
func report(w io.Writer, d *usb.Device, o options) {
	// Phase 1, always: the cached configuration descriptor. No open, no risk,
	// and it names every interface and endpoint the device has -- including any
	// no driver claimed, which is where an unclaimed protocol would live.
	raw, err := d.ConfigDescriptor(0)
	if err != nil {
		fmt.Fprintf(w, "  config descriptor: %v\n", err)
	} else if cfg, err := usb.ParseConfig(raw); err != nil {
		fmt.Fprintf(w, "  config descriptor: %v (%d raw bytes)\n", err, len(raw))
	} else {
		fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(cfg.String(), "\n", "\n  "))
	}

	if !o.control && !o.scan && !o.imu && !o.cdc && o.cdcSet == 0 {
		return
	}

	// Phase 2: open. Everything past here needs a device-level handle, and
	// whether we get one is a property of the device's kernel driver stack.
	if err := d.Open(); err != nil {
		fmt.Fprintf(w, "  open: %v\n", err)
		if !o.seize {
			fmt.Fprintln(w, "  open: not retrying with a seize (-seize to try)")
			return
		}
		if err := d.OpenSeize(); err != nil {
			fmt.Fprintf(w, "  seize: %v\n", err)
			return
		}
		fmt.Fprintln(w, "  seize: SUCCEEDED -- the device was taken from its previous owner")
	} else {
		fmt.Fprintln(w, "  open: ok")
	}

	if o.control {
		controlCheck(w, d, o.timeout)
	}
	if o.scan {
		sweep(w, d, o)
	}
	if o.cdc || o.cdcSet != 0 {
		cdcCheck(w, d, o)
	}
	if o.imu {
		imuAttempt(w, d, o.timeout)
	}
}

// cdcCheck asks the device itself what its serial function is configured for.
//
// This is the question the kernel driver stands between: /dev/cu.* reports what
// AppleUSBACM believes, while GET_LINE_CODING reports what the firmware
// believes, and the two disagreeing -- or the second answering seven zero bytes
// to everything -- is the difference between a real CDC function and a
// descriptor-only one whose /dev node leads nowhere.
func cdcCheck(w io.Writer, d *usb.Device, o options) {
	for _, iface := range []uint16{0, 1} {
		if o.cdcSet != 0 {
			want := usb.LineCoding{Rate: uint32(o.cdcSet), StopBits: usb.Stop1, Parity: usb.ParityNone, DataBits: 8}
			_, err := d.Control(usb.SetLineCoding(iface), want.Bytes(), o.timeout)
			fmt.Fprintf(w, "  CDC if%d SET_LINE_CODING %s: %s\n", iface, want, result(err))
			// DTR and RTS on a USB CDC device are this request, not a wire.
			_, err = d.Control(usb.SetControlLineState(iface, usb.ControlLineDTR|usb.ControlLineRTS), nil, o.timeout)
			fmt.Fprintf(w, "  CDC if%d SET_CONTROL_LINE_STATE DTR|RTS: %s\n", iface, result(err))
		}
		buf := make([]byte, usb.LineCodingSize)
		n, err := d.Control(usb.GetLineCoding(iface), buf, o.timeout)
		if err != nil {
			fmt.Fprintf(w, "  CDC if%d GET_LINE_CODING: %v\n", iface, err)
			continue
		}
		lc, perr := usb.ParseLineCoding(buf[:n])
		switch {
		case perr != nil:
			fmt.Fprintf(w, "  CDC if%d GET_LINE_CODING: %d byte(s) %s: %v\n", iface, n, hex.EncodeToString(buf[:n]), perr)
		case lc.Zero():
			fmt.Fprintf(w, "  CDC if%d GET_LINE_CODING: %d byte(s) %s -- ALL ZERO: the request is ACKed and unimplemented\n", iface, n, hex.EncodeToString(buf[:n]))
		default:
			fmt.Fprintf(w, "  CDC if%d GET_LINE_CODING: %d byte(s) %s = %s\n", iface, n, hex.EncodeToString(buf[:n]), lc)
		}
	}
}

// result renders a control transfer outcome in one word, so a column of them
// can be read at a glance.
func result(err error) string {
	if err == nil {
		return "ACCEPTED"
	}
	return err.Error()
}

// controlCheck is the known-good control for the whole binding. GET_DESCRIPTOR
// with a device descriptor is the one control transfer every USB device on
// earth must answer, and the answer contains the vendor and product IDs, which
// the IOKit registry reported independently. If those two agree, the control
// path carried real bytes off the wire; nothing else this program prints means
// anything until they do.
func controlCheck(w io.Writer, d *usb.Device, timeout time.Duration) {
	b, err := d.Descriptor(usb.DescDevice, 0, 0, 18)
	if err != nil {
		fmt.Fprintf(w, "  CONTROL: FAIL: %v\n", err)
		return
	}
	fmt.Fprintf(w, "  CONTROL: %d byte(s): %s\n", len(b), hex.EncodeToString(b))
	if len(b) < 18 {
		fmt.Fprintf(w, "  CONTROL: FAIL: a device descriptor is 18 bytes, got %d\n", len(b))
		return
	}
	vid := uint16(b[8]) | uint16(b[9])<<8
	pid := uint16(b[10]) | uint16(b[11])<<8
	info := d.Info()
	if vid == info.VendorID && pid == info.ProductID {
		fmt.Fprintf(w, "  CONTROL: PASS: descriptor reports %04x:%04x, matching the registry\n", vid, pid)
		return
	}
	fmt.Fprintf(w, "  CONTROL: FAIL: descriptor reports %04x:%04x, registry says %04x:%04x\n",
		vid, pid, info.VendorID, info.ProductID)
}

// sweep tries every vendor-defined device-to-host request in turn and reports
// the ones that do not stall.
//
// Direction is fixed to In on purpose. A host-to-device sweep would write
// arbitrary vendor commands into hardware the user is wearing, which could
// change a display mode or worse; a device-to-host sweep asks the device to
// speak and is safe to run against anything.
//
// The sweep runs a canary first. "Everything stalled" is only a finding if this
// program can recognise an answer when it gets one, so before concluding
// anything about the device, the sweep issues the one request every USB device
// must answer and checks that the answer arrives. A silent instrument and a
// silent device look identical in the log otherwise.
func sweep(w io.Writer, d *usb.Device, o options) {
	if !canary(w, d, o.timeout) {
		fmt.Fprintln(w, "  SWEEP: ABORTED: the canary failed, so a stall from this device would prove nothing")
		return
	}
	buf := make([]byte, 64)
	// For the device recipient wIndex is unused; for the interface recipient it
	// selects the interface, so every interface the configuration declares gets
	// its own pass. Sweeping only interface 0 would miss a protocol that lives
	// on the HID or CDC interface.
	for _, pass := range []struct {
		rcpt    usb.Recipient
		indices []uint16
	}{
		{usb.ToDevice, []uint16{0}},
		{usb.ToInterface, []uint16{0, 1, 2, 3, 4, 5}},
	} {
		for _, idx := range pass.indices {
			var hits, tried int
			for req := 0; req <= o.maxReq; req++ {
				s := usb.Setup{
					Direction: usb.In,
					Type:      usb.Vendor,
					Recipient: pass.rcpt,
					Request:   byte(req),
					Index:     idx,
				}
				tried++
				n, err := d.Control(s, buf, o.timeout)
				if err == nil {
					hits++
					fmt.Fprintf(w, "  SWEEP: %s wIndex=%d bRequest=%#02x ANSWERED %d byte(s): %s\n",
						pass.rcpt, idx, req, n, hex.EncodeToString(buf[:n]))
					continue
				}
				var ioe *usb.IOError
				if errors.As(err, &ioe) && ioe.Stalled() {
					continue // the expected answer for a request the device does not implement
				}
				hits++
				fmt.Fprintf(w, "  SWEEP: %s wIndex=%d bRequest=%#02x %v\n", pass.rcpt, idx, req, err)
			}
			fmt.Fprintf(w, "  SWEEP: %s wIndex=%d: %d of %d request(s) did anything but stall\n",
				pass.rcpt, idx, hits, tried)
		}
	}
}

// canary issues the standard GET_STATUS request, which every USB device must
// answer with two bytes, and reports whether it did. It is the sweep's proof
// that it can tell an answer from a stall.
func canary(w io.Writer, d *usb.Device, timeout time.Duration) bool {
	buf := make([]byte, 2)
	n, err := d.Control(usb.Setup{Direction: usb.In, Type: usb.Standard, Recipient: usb.ToDevice}, buf, timeout)
	if err != nil || n != 2 {
		fmt.Fprintf(w, "  SWEEP: canary GET_STATUS: FAIL: %d byte(s), %v\n", n, err)
		return false
	}
	fmt.Fprintf(w, "  SWEEP: canary GET_STATUS: PASS: %s\n", hex.EncodeToString(buf[:n]))
	return true
}

// imuAttempt sends the classic VITURE IMU-enable packet as a vendor
// host-to-device control transfer, then asks for an answer.
//
// The packet's framing is known; the control-transfer envelope it should
// travel in is not, so this tries the encodings a vendor would plausibly have
// chosen. THIS WRITES TO THE DEVICE.
func imuAttempt(w io.Writer, d *usb.Device, timeout time.Duration) {
	pkt := viture.EnableIMU(true, 0)
	fmt.Fprintf(w, "  IMU: packet %s\n", hex.EncodeToString(pkt))

	for _, rcpt := range []usb.Recipient{usb.ToDevice, usb.ToInterface} {
		for _, req := range []byte{0x01, 0x02, 0x09, 0x15} {
			out := usb.Setup{Direction: usb.Out, Type: usb.Vendor, Recipient: rcpt, Request: req}
			n, err := d.Control(out, pkt, timeout)
			if err != nil {
				fmt.Fprintf(w, "  IMU: send %s bRequest=%#02x: %v\n", rcpt, req, err)
				continue
			}
			fmt.Fprintf(w, "  IMU: send %s bRequest=%#02x: ACCEPTED %d byte(s)\n", rcpt, req, n)

			// An accepted write is not an answer. Ask for one.
			in := usb.Setup{Direction: usb.In, Type: usb.Vendor, Recipient: rcpt, Request: req}
			buf := make([]byte, 64)
			m, err := d.Control(in, buf, timeout)
			if err != nil {
				fmt.Fprintf(w, "  IMU: read back %s bRequest=%#02x: %v\n", rcpt, req, err)
				continue
			}
			fmt.Fprintf(w, "  IMU: read back %d byte(s): %s\n", m, hex.EncodeToString(buf[:m]))
			if e, err := viture.ParseIMU(buf[:m]); err == nil {
				fmt.Fprintf(w, "  IMU: DECODED ORIENTATION %+v\n", e)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Interface and pipe mode.
// ---------------------------------------------------------------------------

// ifaceMode lists a device's interfaces and, on request, claims one and talks
// to its pipes.
//
// It is the last door in the device: endpoint 0 is shared and refuses vendor
// requests, the HID interface accepts reports and answers none, so what remains
// is the bulk pipes a kernel driver already owns. Whether macOS hands them over
// is the question, and the IOReturn code is the answer worth recording either
// way.
func ifaceMode(w io.Writer, o options) error {
	filter := usb.InterfaceFilter{VendorID: o.filter.VendorID, ProductIDs: o.filter.ProductIDs}
	if o.iface >= 0 {
		filter.Numbers = []uint8{uint8(o.iface)}
	}
	ifs, err := usb.Interfaces(filter)
	if err != nil {
		return err
	}
	defer func() {
		for _, i := range ifs {
			i.Close()
		}
	}()
	fmt.Fprintf(w, "== %d USB interface(s) ==\n", len(ifs))
	for _, i := range ifs {
		fmt.Fprintf(w, "\n%s\n", i)
		ifaceReport(w, i, o)
	}
	return nil
}

// ifaceReport claims one interface and reports what it found.
func ifaceReport(w io.Writer, i *usb.InterfaceHandle, o options) {
	if err := i.Open(); err != nil {
		fmt.Fprintf(w, "  USBInterfaceOpen: %v\n", err)
		if !o.seize {
			fmt.Fprintln(w, "  not retrying with a seize (-seize to take it from its driver)")
			return
		}
		if err := i.OpenSeize(); err != nil {
			fmt.Fprintf(w, "  USBInterfaceOpenSeize: %v\n", err)
			return
		}
		fmt.Fprintln(w, "  USBInterfaceOpenSeize: SUCCEEDED -- the kernel driver has been displaced")
	} else {
		fmt.Fprintln(w, "  USBInterfaceOpen: ok")
	}

	pipes, err := i.Pipes()
	if err != nil {
		fmt.Fprintf(w, "  pipes: %v\n", err)
		return
	}
	for _, p := range pipes {
		fmt.Fprintf(w, "  %s\n", p)
	}

	if o.bulkWrite != "" {
		writePipe(w, i, pipes, o)
	}
	if o.bulkIMU {
		writeBytes(w, i, pipes, o, viture.EnableIMU(true, 0), "the VITURE IMU-enable packet")
	}
	if o.bulkRead != 0 {
		readPipe(w, i, pipes, o)
	}
}

// findPipe resolves an endpoint address to the pipe ref the read and write
// calls take.
func findPipe(pipes []usb.Pipe, address byte) (usb.Pipe, bool) {
	for _, p := range pipes {
		if p.Address() == address {
			return p, true
		}
	}
	return usb.Pipe{}, false
}

// writePipe sends the -bulk-write bytes.
func writePipe(w io.Writer, i *usb.InterfaceHandle, pipes []usb.Pipe, o options) {
	b, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "", "-", "").Replace(o.bulkWrite))
	if err != nil {
		fmt.Fprintf(w, "  -bulk-write is not hex: %v\n", err)
		return
	}
	writeBytes(w, i, pipes, o, b, "the -bulk-write payload")
}

// writeBytes sends payload on the OUT pipe named by -bulk-out.
func writeBytes(w io.Writer, i *usb.InterfaceHandle, pipes []usb.Pipe, o options, payload []byte, what string) {
	p, ok := findPipe(pipes, byte(o.bulkOut))
	if !ok {
		fmt.Fprintf(w, "  no endpoint %#02x on this interface\n", byte(o.bulkOut))
		return
	}
	n, err := i.Write(p.Ref, payload, o.timeout)
	if err != nil {
		fmt.Fprintf(w, "  WRITE ep %#02x %s: %v\n", p.Address(), what, err)
		return
	}
	fmt.Fprintf(w, "  WRITE ep %#02x %s: %d byte(s) ACKed by the device's controller: %s\n",
		p.Address(), what, n, hex.EncodeToString(payload))
}

// readPipe reads from the IN pipe named by -bulk-read, printing the raw bytes
// before any interpretation of them.
func readPipe(w io.Writer, i *usb.InterfaceHandle, pipes []usb.Pipe, o options) {
	p, ok := findPipe(pipes, byte(o.bulkRead))
	if !ok {
		fmt.Fprintf(w, "  no endpoint %#02x on this interface\n", byte(o.bulkRead))
		return
	}
	size := int(p.MaxPacket)
	if size <= 0 {
		size = 512
	}
	deadline := time.Now().Add(o.bulkFor)
	total, reads, timeouts := 0, 0, 0
	for time.Now().Before(deadline) {
		buf := make([]byte, size)
		n, err := i.Read(p.Ref, buf, o.timeout)
		reads++
		switch {
		case n > 0:
			// Raw bytes first, always, and before any interpretation of them.
			total += n
			fmt.Fprintf(w, "  READ  ep %#02x attempt %d: %d byte(s)\n", p.Address(), reads, n)
			fmt.Fprintf(w, "        %s\n", hex.EncodeToString(buf[:n]))
		case isTimeout(err):
			timeouts++
		case err != nil:
			fmt.Fprintf(w, "  READ  ep %#02x attempt %d: %v\n", p.Address(), reads, err)
			return
		}
	}
	fmt.Fprintf(w, "  READ  ep %#02x: %d byte(s) in %d attempt(s) over %v, %d of them timed out\n",
		p.Address(), total, reads, o.bulkFor, timeouts)
	if total == 0 {
		fmt.Fprintln(w, "  READ  the pipe was polled and stayed empty: the device NAKed every IN token")
	}
}

// isTimeout reports whether an error is the ordinary "the device had nothing to
// send" answer rather than a failure. A NAKed bulk IN is the expected outcome
// of polling an idle pipe, so it must not end the loop.
func isTimeout(err error) bool {
	var e *usb.IOError
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == ioreturn.USBTransactionTimeout || e.Code == ioreturn.Timeout
}
