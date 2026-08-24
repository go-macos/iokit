# go-macos/iokit

Pure-Go (`CGO_ENABLED=0`) access to macOS IOKit through
[purego](https://github.com/ebitengine/purego). No cgo, no hidapi, no shelling
out to `ioreg`.

## `hid` — HID devices

```go
devs, err := hid.Devices(hid.Filter{VendorID: 0x35CA})
for _, d := range devs {
        defer d.Close()
        if err := d.Open(); err != nil { continue }
        d.SetReport(hid.Output, 0, payload)
}

// Stream BLOCKS and pins its goroutine: IOKit delivers reports to the run loop
// of the thread that scheduled the device.
go hid.Stream(ctx, func(d *hid.Device, report []byte) {
        // called on the pump thread; copy what you keep
}, devs...)
```

### Two things that will cost you a day if you learn them the hard way

**`SetReport` returning nil means macOS accepted the write — not that the device
understood it.** A device that does not implement the vendor protocol you are
speaking accepts every report and answers nothing. Only an actual reply proves
anything.

**`IOHIDDeviceScheduleWithRunLoop` binds a device to the *calling thread's* run
loop.** Without `runtime.LockOSThread()` the Go scheduler moves the goroutine
and the pump then services a loop nothing is attached to: zero reports, no
error. `Stream` does the pinning for you.

## `cmd/hidprobe`

```
go run ./cmd/hidprobe                      # list every HID device
go run ./cmd/hidprobe -stream 10s          # open them all and count reports
go run ./cmd/hidprobe -vendor 0x35ca -stream 30s
```

Use it as a control before blaming a device: if `hidprobe` sees traffic from
*something*, the reader is sound and the silence is the device's.

## `usb` — control transfers on endpoint 0

A HID interface is not always where a device's interesting protocol lives.
`usb` binds IOUSBLib, so you can open a device and speak to endpoint 0
directly.

```go
devs, _ := usb.Devices(usb.Filter{VendorID: 0x35CA, ProductIDs: []uint16{0x1201}})
d := devs[0]
defer d.Close()

// Needs no open, and no driver can refuse it: the kernel cached this at
// enumeration. It names every interface and endpoint the device has.
raw, _ := d.ConfigDescriptor(0)
cfg, _ := usb.ParseConfig(raw)

if err := d.Open(); err != nil {
        // kIOReturnExclusiveAccess means a kernel driver holds the DEVICE.
        // d.OpenSeize() is the documented escape hatch.
}
b, err := d.Descriptor(usb.DescDevice, 0, 0, 18)      // standard GET_DESCRIPTOR
n, err := d.Control(usb.Setup{                        // vendor request
        Direction: usb.In, Type: usb.Vendor, Recipient: usb.ToDevice,
        Request:   0x15,
}, buf, 250*time.Millisecond)
```

### What a control transfer proves that a HID report does not

USB acknowledges at the bus level, so the error is evidence:

| result | what it means |
| --- | --- |
| `nil` | the device's controller ACKed the transfer |
| `kIOUSBPipeStalled` (`IOError.Stalled()`) | the device received the setup packet and **refused** it — it does not implement this request |
| `kIOUSBTransactionTimeout` | the device never answered at all |
| `kIOReturnExclusiveAccess` on open | a kernel driver holds the device itself (holding its *interfaces* does not conflict) |

A stall is a real answer. That is the whole difference from `hid.SetReport`,
which returns success for a write macOS accepted into the void.

### Vtable indices are measured, not counted

IOUSBLib is a CFPlugIn: a pointer to a pointer to a vtable, dispatched by
index. Those indices were printed by a C program calling `offsetof()` on the
real headers, because counting them off the documented method list gets it
wrong — `DeviceRequest` is at index 26, not the 24 an eyeball count suggests.

## `serial` — the CDC-ACM port, with no cgo

A composite USB device that publishes a CDC-ACM function gets a `/dev/cu.*`
and `/dev/tty.*` pair from Apple's driver, and those bulk pipes are the one
channel `usb` cannot reach while the kernel owns them. `serial` opens the tty,
puts it in raw mode through termios, and drives the modem lines.

```go
p, err := serial.Open("/dev/cu.usbmodem14201", serial.Config{
        Baud: 115200, CLOCAL: true, ReadMin: 1,
})
defer p.Close()

p.SetLines(serial.LineDTR|serial.LineRTS, 0)   // a cu. node leaves both low
n, _ := serial.Drain(p, buf, time.Now().Add(30*time.Second))
```

### Three things that will cost you an afternoon

**`VMIN=0` with `VTIME=0` makes every port look mute.** That is the POSIX
polling mode: `read(2)` returns zero bytes immediately when the queue is empty,
Go reports a zero-byte read on a character device as `io.EOF`, and a thirty
second listen ends in one microsecond with nothing to show. `Config.ReadMin`
should be 1. The package's own control caught this on its first run.

**`/dev/cu.*` does not raise DTR and `/dev/tty.*` blocks until carrier.** A
device whose firmware waits for DTR is silent on one node and alive on the
other, with no error either way. `Open` always passes `O_NONBLOCK` so the
dial-in node cannot hang, and `SetLines` lets the question be asked of both.

**A driver with a table of standard rates rejects the whole termios over the
rate alone.** macOS answers `EINVAL` to 1000000 baud and drops the parity and
data bits with it. `Configure` retries at the rate already in force and then
insists through `IOSSIOSPEED`, which is how 3 Mbaud becomes reachable.

`serial.Loopback` allocates a pseudo-terminal so a probe can prove its reader
reads before concluding anything from silence.

## `viture` — the classic VITURE MCU protocol

Packet framing and CRC-16/CCITT-FALSE for VITURE XR glasses, with no I/O
attached, so the same packet can be tried over HID, control transfers or a
serial port. Confirmed to work only up to the VITURE Pro (`0x101D`); treat it
as a hypothesis on anything newer.

## `cmd/usbprobe`

The dangerous half is opt-in. With no flags it only reads.

```
go run ./cmd/usbprobe                                 # list + decode config descriptors, no open
go run ./cmd/usbprobe -device 35ca:1201 -control      # open + GET_DESCRIPTOR (the binding's control)
go run ./cmd/usbprobe -device 35ca:1201 -scan         # sweep vendor device-to-host requests
go run ./cmd/usbprobe -device 35ca:1201 -imu          # WRITES the VITURE IMU-enable packet
```

`-control` is the one to run first: a device descriptor read back with the
vendor and product IDs the IOKit registry reported independently proves the
whole control path carries real bytes. `-scan` runs its own canary
(`GET_STATUS`) before sweeping, because "everything stalled" is only a finding
if the instrument can recognise an answer.

## Measured: the VITURE Beast (`35ca:1201`) does not answer vendor control requests

Recorded here so nobody spends the afternoon again. On macOS 26.6.2 (arm64), with
the control above passing on the same open handle:

- The Beast **does** open at device level (`USBDeviceOpen` → success) and
  **does** answer standard requests: its device descriptor and `GET_STATUS`
  come back correctly.
- **1792 vendor device-to-host requests** — `bRequest` `0x00`–`0xFF`, recipient
  device and interfaces 0–5 — **every one stalled.**
- The documented VITURE IMU-enable packet sent as a vendor host-to-device
  request stalled on every `bRequest` tried, at both recipients.
- Class-typed device-to-host requests were swept too, in case the vendor
  tunnelled its protocol through the CDC interface's class requests. Of 3840
  tried (`bRequest` `0x00`–`0xFF` across recipients device, interface and
  endpoint, `wIndex` 0, 1, 5, `0x83`, `0x84`), exactly two answered: the CDC
  `GET_LINE_CODING`/`SET_LINE_CODING` pair on interfaces 0 and 1, returning
  seven zero bytes. That is a stub line-coding implementation, not a protocol.

So the vendor channel is not on endpoint 0. What the configuration descriptor
does show is a **CDC-ACM serial function** (interfaces 0–1, bulk `0x03` out /
`0x83` in, 512 bytes) alongside the audio and HID interfaces, which macOS binds
as `/dev/cu.usbmodem*`.

Two corrections to what that first pass concluded, both established below. The
vendor application holds an `AppleUSBHostInterfaceUserClient` on **every**
interface, 0 through 5, not on interface 0 in particular -- that is simply what
opening a device and iterating its interfaces looks like in the registry, and
it points at nothing. And on a later run the CDC line-coding requests stopped
answering at all, `kIOReturnNotResponding` rather than seven zero bytes, so
even the stub was not a standing behaviour.

## `cmd/serialprobe`

```
go run ./cmd/serialprobe                                    # list ports, open nothing
go run ./cmd/serialprobe -selftest                          # the loopback control
go run ./cmd/serialprobe -port usbmodem14201 -listen 30s    # read, send nothing
go run ./cmd/serialprobe -port usbmodem14201 -listen 5s -imu -dtr -rts
go run ./cmd/serialprobe -port usbmodem14201 -sweep -imu    # rates x nodes x lines
```

Every mode that touches hardware runs the loopback control first and refuses to
continue if it fails.

## Measured: the VITURE Beast (`35ca:1201`) serial function does not carry bytes

The second half of the same afternoon, recorded so nobody repeats it. Same
machine, macOS 26.6.2 arm64, with the vendor application running and holding a
user client on all six interfaces.

**The tty is mute in both directions.** `/dev/cu.usbmodem*` and
`/dev/tty.usbmodem*`, at 115200, 921600, 1000000 and 3000000 baud, with DTR and
RTS both asserted and both dropped, listening for thirty seconds without
sending and then again after sending the documented IMU-enable packet: **not
one byte, in any combination.** 1000000 and 3000000 are refused outright by the
ACM driver's rate table and only become settable through `IOSSIOSPEED`.

**The CDC class requests are not answered.** `GET_LINE_CODING`,
`SET_LINE_CODING` and `SET_CONTROL_LINE_STATE` on interfaces 0 and 1 all return
`kIOReturnNotResponding` -- the device NAKs until the timeout -- while
`GET_DESCRIPTOR` on the same open handle keeps working before and after. So the
firmware advertises a CDC function it does not implement.

**The bulk pipes themselves are dead.** `USBInterfaceOpen` on interface 1
**succeeds**, and `GetPipeProperties` reproduces the configuration descriptor
exactly: pipe 1 is ep `0x03` out bulk 512, pipe 2 is ep `0x83` in bulk 512.
Both `ReadPipeTO` on `0x83` and `WritePipeTO` on `0x03` return
`kIOUSBTransactionTimeout`, with timeouts up to fifteen seconds. The device
NAKs its own advertised endpoints in both directions. Interfaces 0 and 5 refuse
to open at all: `kIOReturnExclusiveAccess`.

**Two instrument bugs were caught before they became findings**, and both are
worth knowing:

- `ReadPipeTO` leaves its size word untouched when the transfer never
  completed, and IOKit overwrites the buffer with kernel scratch -- a poison
  fill put in beforehand was gone. Read naively that is 512 bytes of data per
  poll out of a device that said nothing. `usb` now reports zero bytes on any
  non-success code.
- A pipe reference is not an endpoint address, and getting the mapping wrong
  gives a plausible-looking timeout rather than an error. The check that
  settles it: IOUSBLib rejects a *read* of the OUT pipe and a *write* of the IN
  pipe with `kIOReturnBadArgument`, and answers `kIOUSBUnknownPipeErr` for any
  reference the interface does not own. Only the two real pipes reach the
  device -- which is what makes their timeouts the device's answer and not the
  instrument's.

So the serial function is a descriptor-only façade. Whatever the vendor's
application is doing, it is not sending bytes down this CDC function while a
second client is watching it -- and this module has now eliminated HID reports,
vendor control transfers, class control transfers, the tty, and the bulk pipes
underneath it.

## Status

`hid` is complete for enumeration, report writing and input streaming.
`usb` is complete for enumeration, device-level open/seize, cached
configuration descriptors, synchronous control transfers, interface
open/seize and synchronous bulk and interrupt pipe transfers. Isochronous
pipes and the asynchronous variants are not implemented.

`serial` is complete for enumeration, open, raw-mode termios, arbitrary line
rates through IOSSIOSPEED, modem control lines and deadline-bounded reads.

The portable layer of every package is at 100% statement coverage; the purego
bindings are verified on-device by `hidprobe`, `usbprobe` and an on-device test
that enumerates the real USB bus.

Licence: BSD-3-Clause.
