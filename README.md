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

## Status

`hid` is complete for enumeration, report writing and input streaming. The
portable layer is at 100% statement coverage; the purego bindings are verified
on-device with `hidprobe`. Feature/Get-report polling and device
hotplug notifications are not implemented yet.

Licence: BSD-3-Clause.
