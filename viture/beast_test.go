package viture

import "testing"

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
			Event{Counter: 0x00, ID: MsgNativeDisplayMode, Kind: KindNotify, Value: NativeMode3DSBS3840x1200At60},
		},
		{
			"and back out of it",
			[]byte{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31, 0x00},
			Event{Counter: 0x04, ID: MsgNativeDisplayMode, Kind: KindNotify, Value: Mode1920x1080At60},
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

// TestTheMsgIDIsSixteenBitsAcrossTwoBytes.
//
// ⭐ What looked like an opaque "kind" is the HIGH byte of a 16-bit identifier
// whose top nibble is the direction. It reconciles two decodings that seemed to
// contradict each other: the table disassembled out of SpaceWalker, which gives
// getBrightness 0x3122 and setBrightness 0x0122, and the reports seen on the
// wire, where a brightness reading arrives as byte 2 = 0x22 with byte 3 = 0x51.
func TestTheMsgIDIsSixteenBitsAcrossTwoBytes(t *testing.T) {
	// A real frame, captured 2026-09-05: brightness, value 6.
	e, ok := ParseEvent([]byte{0x10, 0x00, 0x22, 0x51, 0x02, 0x00, 0x06, 0x00, 0x00, 0x06})
	if !ok {
		t.Fatal("a captured frame did not parse")
	}
	if got := e.MsgID(); got != 0x5122 {
		t.Errorf("MsgID = %#04x, want %#04x", got, 0x5122)
	}
	if got := e.Direction(); got != DirReply {
		t.Errorf("Direction = %#x, want a reply", got)
	}
	// And the same message asked for and written would be 0x3122 and 0x0122 --
	// the low byte is what identifies it, in every direction.
	if e.ID != 0x22 {
		t.Errorf("ID = %#02x", e.ID)
	}
}

// TestBothDisplayModeMessagesAreNamed.
//
// ⭐ THERE ARE TWO AND THEY ARE NOT A CONTRADICTION. Captured on 2026-09-05
// with the display at 3840x1080: MsgDisplayMode reported 0x32 and
// MsgNativeDisplayMode reported 0x37, and both describe that state -- the
// glasses hold two settings, which SpaceWalker names apart as
// R6SetDisplayModeHIDMsg and R6NewerNativeDisplayModeHIDMsg.
func TestBothDisplayModeMessagesAreNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		frame []byte
		id    byte
		value uint16
		dir   Direction
		is3D  bool
	}{
		{
			"the display mode, as a reply",
			[]byte{0x10, 0x00, 0x24, 0x51, 0x03, 0x00, 0x32, 0x00, 0x00, 0x32},
			MsgDisplayMode, Mode3840x1080At60, DirReply, true,
		},
		{
			"the native mode, announced when the button was pressed",
			[]byte{0x10, 0x01, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
			MsgNativeDisplayMode, NativeMode3DSBS3840x1080At60, DirNotify, true,
		},
		{
			"and back to 2D",
			[]byte{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
			MsgNativeDisplayMode, Mode1920x1080At60, DirNotify, false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, ok := ParseEvent(c.frame)
			if !ok {
				t.Fatal("a captured frame did not parse")
			}
			if e.ID != c.id {
				t.Errorf("ID = %#02x, want %#02x", e.ID, c.id)
			}
			if e.Value != c.value {
				t.Errorf("Value = %#x, want %#x", e.Value, c.value)
			}
			if e.Direction() != c.dir {
				t.Errorf("Direction = %#x, want %#x", e.Direction(), c.dir)
			}
			if Stereoscopic(e.Value) != c.is3D {
				t.Errorf("Stereoscopic(%#x) = %v", e.Value, Stereoscopic(e.Value))
			}
		})
	}
}

// TestTheLengthByteIsConstantPerMessage.
//
// ⛔ It is NOT the payload's length: five announcements of the native mode
// captured while the button was pressed, alternating 0x31 and 0x37, ALL carry
// 0x01 -- while a display-mode reply carries 0x03 and a brightness reply 0x02,
// in frames of the same shape. Whatever it means, it is a property of the
// MESSAGE, which is what a caller building one needs to know.
func TestTheLengthByteIsConstantPerMessage(t *testing.T) {
	for _, f := range [][]byte{
		{0x10, 0x00, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
		{0x10, 0x01, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
		{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
		{0x10, 0x05, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
		{0x10, 0x07, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
	} {
		if f[4] != 0x01 {
			t.Errorf("a native-mode frame carries length %#02x", f[4])
		}
		if _, ok := ParseEvent(f); !ok {
			t.Errorf("%02x did not parse", f)
		}
	}
}
