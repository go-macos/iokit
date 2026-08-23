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
	device  string
	control bool
	scan    bool
	imu     bool
	seize   bool
	timeout time.Duration
	maxReq  int
}

func run(w io.Writer, args []string) error {
	var o options
	fs := flag.NewFlagSet("usbprobe", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&o.device, "device", "", "restrict to one device, as vendor:product in hex (e.g. 35ca:1201)")
	fs.BoolVar(&o.control, "control", false, "open each device and read its device descriptor (the binding's known-good control)")
	fs.BoolVar(&o.scan, "scan", false, "sweep vendor-defined device-to-host requests, looking for one the device answers")
	fs.BoolVar(&o.imu, "imu", false, "WRITE the VITURE IMU-enable packet to the device (the only flag that writes)")
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

	if !o.control && !o.scan && !o.imu {
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
	if o.imu {
		imuAttempt(w, d, o.timeout)
	}
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
