//go:build darwin

package hid

import (
	"math"
	"testing"
)

// ⛔⛔ A VALUE TOO BIG IS ABSENT, NOT WRAPPED. propInt reads the registry at 64
// bits because asking CFNumberGetValue for SInt32 returns FALSE on a lossy
// conversion -- a present property reported missing. Having read it widely,
// truncating it here would trade that silent failure for a worse one: a vendor
// id or a report size that is a plausible number and the wrong one.
func TestAValueTooWideIsAbsentRatherThanWrapped(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		wide int64
		want int32
		ok   bool
	}{
		{"an ordinary vendor id", 0x05AC, 0x05AC, true},
		{"zero", 0, 0, true},
		{"a negative usage page", -1, -1, true},
		{"the largest that fits", math.MaxInt32, math.MaxInt32, true},
		{"the smallest that fits", math.MinInt32, math.MinInt32, true},
		// ⛔ These are the ones that matter: truncation would make the first
		// read as 0 and the second as -1, both perfectly plausible.
		{"one past the top", math.MaxInt32 + 1, 0, false},
		{"one below the bottom", math.MinInt32 - 1, 0, false},
		{"a 64-bit location id", 0x1_0000_0000, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, ok := narrowInt32(c.wide)
			if ok != c.ok {
				t.Errorf("narrowInt32(%#x) present=%t, want %t", c.wide, ok, c.ok)
			}
			if got != c.want {
				t.Errorf("narrowInt32(%#x) = %#x, want %#x", c.wide, got, c.want)
			}
		})
	}
}
