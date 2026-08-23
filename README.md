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

So the vendor channel is not on endpoint 0. What the configuration descriptor
does show is a **CDC-ACM serial function** (interfaces 0–1, bulk `0x03` out /
`0x83` in, 512 bytes) alongside the audio and HID interfaces — macOS binds it
as `/dev/cu.usbmodem*`, and the vendor's own app holds an
`AppleUSBHostInterfaceUserClient` on interface 0. That, not endpoint 0, is
where the protocol lives.

## Status

`hid` is complete for enumeration, report writing and input streaming.
`usb` is complete for enumeration, device-level open/seize, cached
configuration descriptors and synchronous control transfers; bulk and
interrupt pipes are not implemented yet.

The portable layer of every package is at 100% statement coverage; the purego
bindings are verified on-device by `hidprobe`, `usbprobe` and an on-device test
that enumerates the real USB bus.

Licence: BSD-3-Clause.
