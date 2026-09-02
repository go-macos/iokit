package viture

import (
	"encoding/hex"
	"testing"
)

func TestParseEventReadsTheFrameSeenOnTheWire(t *testing.T) {
	// Captured from a VITURE Beast on 2026-09-01, at the second its display
	// mode changed and the Mac's own display list changed with it. Keeping the
	// real bytes here means a change to the parser is measured against the
	// hardware rather than against the parser's own idea of the hardware.
	for _, tc := range []struct {
		name string
		in   []byte
		want Event
	}{
		{
			"the switch into 3D",
			[]byte{0x10, 0x00, 0x42, 0x71, 0x01, 0x00, 0x3a, 0x00, 0x3a, 0x00},
			Event{Counter: 0x00, ID: MsgDisplayMode, Kind: KindNotify, Value: NativeMode3DSBS3840x1200At60},
		},
		{
			"and back out of it",
			[]byte{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31, 0x00},
			Event{Counter: 0x04, ID: MsgDisplayMode, Kind: KindNotify, Value: Mode1920x1080At60},
		},
		{
			"the glasses being worn",
			[]byte{0x10, 0x00, 0x21, 0x73, 0x01, 0x00, 0x01, 0x00},
			Event{Counter: 0x00, ID: MsgWearStatus, Kind: KindNotify2, Value: 1},
		},
		{
			// Volume ramps in nine steps; brightness in three. Keeping one of
			// each means the identifiers cannot be swapped without a test
			// saying so.
			"the volume, most of the way up",
			[]byte{0x10, 0x0a, 0x30, 0x73, 0x01, 0x00, 0x08, 0x00},
			Event{Counter: 0x0a, ID: MsgVolume, Kind: KindNotify2, Value: 8},
		},
		{
			"the brightness, one step up",
			[]byte{0x10, 0x03, 0x01, 0x72, 0x01, 0x00, 0x02, 0x00},
			Event{Counter: 0x03, ID: MsgBrightness, Kind: 0x72, Value: 2},
		},
		{
			// It went to zero unasked, in the same second the display entered
			// 3D. The two look mutually exclusive on this hardware.
			"the spatial anchoring giving way to 3D",
			[]byte{0x10, 0x00, 0x44, 0x71, 0x01, 0x00, 0x00, 0x00},
			Event{Counter: 0x00, ID: MsgNativeDOF, Kind: KindNotify, Value: 0},
		},
		{
			"the electrochromic film",
			[]byte{0x10, 0x00, 0x43, 0x71, 0x01, 0x00, 0x02, 0x00},
			Event{Counter: 0x00, ID: MsgElectrochromic, Kind: KindNotify, Value: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseEvent(tc.in)
			if !ok {
				t.Fatal("a frame from the wire was not recognised")
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseEventRefusesWhatIsNotThisProtocol(t *testing.T) {
	// A report of another shape is far more likely to be a different protocol
	// than a corrupted frame of this one, and treating it as the latter would
	// invent events -- which is exactly how this package came to believe the
	// glasses answered nothing.
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"nothing", nil},
		{"too short", []byte{0x10, 0x00, 0x42, 0x71, 0x01, 0x00, 0x3a}},
		{"the wrong first byte", []byte{0x11, 0x00, 0x42, 0x71, 0x01, 0x00, 0x3a, 0x00}},
		{"a non-zero fifth byte", []byte{0x10, 0x00, 0x42, 0x71, 0x01, 0x01, 0x3a, 0x00}},
		{"a frame of the OLDER protocol", EnableIMU(true, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ParseEvent(tc.in); ok {
				t.Fatal("it was accepted")
			}
		})
	}
}

func TestStereoscopicKnowsWhichModesShowTwoPictures(t *testing.T) {
	for _, m := range []uint16{
		Mode3840x1080At60, Mode3840x1080At90, Mode3840x1200At60, Mode3840x1200At90,
		NativeMode3DSBS3840x1080At60, NativeMode3DSBS3840x1080At90,
		NativeMode3DSBS3840x1080At120, NativeMode3DSBS3840x1200At60,
		NativeMode3DSBS3840x1200At90, NativeMode3DSBS3840x1200At120, ModeSideBySide60,
	} {
		if !Stereoscopic(m) {
			t.Errorf("%#x (%s) was not called stereoscopic", m, ModeName(m))
		}
	}
	for _, m := range []uint16{
		Mode1920x1080At60, Mode1920x1080At90, Mode1920x1080At120,
		Mode1920x1200At60, Mode1920x1200At90, Mode1920x1200At120,
		// Wider than one eye and NOT stereoscopic, which is why the answer
		// comes from a list and not from the resolution.
		ModeUltrawide60To120,
		0x99,
	} {
		if Stereoscopic(m) {
			t.Errorf("%#x (%s) was called stereoscopic", m, ModeName(m))
		}
	}
}

func TestModeNameSaysPlainlyWhenItDoesNotKnow(t *testing.T) {
	if got := ModeName(NativeMode3DSBS3840x1200At60); got != "native 3D side by side, 3840x1200 at 60 Hz" {
		t.Errorf("ModeName = %q", got)
	}
	for _, m := range []uint16{
		Mode1920x1080At60, Mode3840x1080At60, Mode1920x1080At90, Mode1920x1080At120,
		Mode3840x1080At90, Mode1920x1200At60, Mode3840x1200At60, Mode1920x1200At90,
		Mode1920x1200At120, Mode3840x1200At90,
		NativeMode3DSBS3840x1080At60, NativeMode3DSBS3840x1080At90,
		NativeMode3DSBS3840x1080At120, NativeMode3DSBS3840x1200At90,
		NativeMode3DSBS3840x1200At120, ModeUltrawide60To120, ModeSideBySide60,
	} {
		if ModeName(m) == "an unnamed mode" {
			t.Errorf("%#x has no name", m)
		}
	}
	if got := ModeName(0x99); got != "an unnamed mode" {
		t.Errorf("an unknown mode is called %q", got)
	}
}

// The frames below are REAL: captured on 2026-09-02 from a Beast, as a second
// HID client, while the manufacturer's application held a session open and each
// control was worked in turn. They are kept verbatim -- trailing zeroes and all
// -- because a fixture that has been tidied is no longer evidence.
var capturedBeastFrames = []struct {
	name  string
	hex   string
	id    byte
	kind  byte
	value uint16
}{
	{"brightness stepping up", "1029017201000700070000000000000000000000", MsgBrightness, KindNotify3, 7},
	{"volume stepping up", "1032307301000800080000000000000000000000", MsgVolume, KindNotify2, 8},
	{"glasses put on", "1003217301000100010000000000000000000000", MsgWearStatus, KindNotify2, 1},
	{"glasses taken off", "1038217301000000000000000000000000000000", MsgWearStatus, KindNotify2, 0},
	{"ambient light", "1037227101000600060000000000000000000000", MsgAmbient, KindNotify, 6},
	{"the film going clear", "1034437101000000000000000000000000000000", MsgElectrochromic, KindNotify, 0},
	{"the film going dark", "1033437101000200020000000000000000000000", MsgElectrochromic, KindNotify, 2},
	{"a display mode", "1002427101003d003d00000000000000000000000000", MsgDisplayMode, KindNotify, 0x3d},
}

func TestParseEventOnFramesFromRealGlasses(t *testing.T) {
	for _, c := range capturedBeastFrames {
		t.Run(c.name, func(t *testing.T) {
			b, err := hex.DecodeString(c.hex)
			if err != nil {
				t.Fatalf("the fixture is not hex: %v", err)
			}
			ev, ok := ParseEvent(b)
			if !ok {
				t.Fatalf("a frame this device really sent was refused")
			}
			if ev.ID != c.id {
				t.Errorf("message id %#02x, want %#02x", ev.ID, c.id)
			}
			if ev.Kind != c.kind {
				t.Errorf("kind %#02x, want %#02x", ev.Kind, c.kind)
			}
			if ev.Value != c.value {
				t.Errorf("value %d, want %d", ev.Value, c.value)
			}
		})
	}
}

// TestTheThreeAnnouncementKindsDoNotSplitByMessage records what the capture
// showed and what it did NOT: brightness announced itself on one kind, volume
// and wear status on another, and three more messages on a third. Whoever
// eventually finds the rule should find this test in their way if they assume a
// simpler one.
func TestTheThreeAnnouncementKindsDoNotSplitByMessage(t *testing.T) {
	seen := map[byte]byte{}
	for _, c := range capturedBeastFrames {
		if prev, ok := seen[c.id]; ok && prev != c.kind {
			t.Errorf("message %#02x was announced with both %#02x and %#02x", c.id, prev, c.kind)
		}
		seen[c.id] = c.kind
	}
	kinds := map[byte]bool{}
	for _, k := range seen {
		kinds[k] = true
	}
	if len(kinds) < 3 {
		t.Errorf("the capture shows %d announcement kind(s); it showed three, so a fixture has been lost", len(kinds))
	}
	if seen[MsgBrightness] == seen[MsgVolume] {
		t.Error("brightness and volume announced on the SAME kind, which the capture contradicts")
	}
}
