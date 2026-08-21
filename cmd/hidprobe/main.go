// Command hidprobe lists the machine's HID devices and, with -stream, opens
// them and prints the input reports that arrive. It is the package's own
// dogfood: everything it does goes through the public API.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/go-macos/iokit/hid"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hidprobe:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		vendor = flag.Uint("vendor", 0, "only devices with this USB vendor ID (0 = all)")
		dur    = flag.Duration("stream", 0, "open every listed device and stream input reports for this long")
	)
	flag.Parse()

	devs, err := hid.Devices(hid.Filter{VendorID: uint16(*vendor)})
	if err != nil {
		return err
	}
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()

	fmt.Printf("== %d HID device(s) ==\n", len(devs))
	for _, d := range devs {
		fmt.Printf("   %s\n", d)
	}
	if *dur <= 0 || len(devs) == 0 {
		return nil
	}

	var open []*hid.Device
	for _, d := range devs {
		if err := d.Open(); err != nil {
			fmt.Printf("   SKIP %s: %v\n", d.Info().Product, err)
			continue
		}
		open = append(open, d)
	}
	fmt.Printf("\n== streaming %s from %d/%d device(s) ==\n", *dur, len(open), len(devs))
	if len(open) == 0 {
		return nil
	}

	counts := map[string]int{}
	total := 0
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()

	start := time.Now()
	err = hid.Stream(ctx, func(d *hid.Device, data []byte) {
		i := d.Info()
		key := fmt.Sprintf("%04x:%04x usage %#x/%#x %q", i.VendorID, i.ProductID, i.UsagePage, i.Usage, i.Product)
		counts[key]++
		total++
		if counts[key] == 1 {
			fmt.Printf("   [%5.2fs] first from %s  len=%d\n", time.Since(start).Seconds(), key, len(data))
		}
	}, open...)
	if err != nil && ctx.Err() == nil {
		return err
	}

	fmt.Printf("\n== %d report(s) in %s ==\n", total, dur)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return counts[keys[a]] > counts[keys[b]] })
	for _, k := range keys {
		fmt.Printf("   %6d  %s\n", counts[k], k)
	}
	if total == 0 {
		return fmt.Errorf("no reports at all: the reader is suspect, not the devices")
	}
	return nil
}
