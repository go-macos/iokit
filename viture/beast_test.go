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

// TestTheCommandThatWorks.
//
// ⭐ THE BYTES ARE NOT A DESIGN, THEY ARE A MEASUREMENT. This exact report was
// sent to a Beast and answered with status 0, and the Mac's display list
// changed to 3840x1080 in the same second; sent with 0x31 it came back to
// 1920x1080. Both directions, twice each.
//
// ⛔ AND WHAT NINETEEN FAILURES HAD WRONG IS ONE DETAIL: the value goes in
// TWICE, LITTLE-ENDIAN, packed. The device's own replies carry it
// little-endian and then BIG-endian, so that shape was copied into the command
// -- and answered with the refusal code every time. A reply and a command are
// not the same frame.
func TestTheCommandThatWorks(t *testing.T) {
	got := SetDisplayMode(Mode3840x1080At60)
	want := []byte{0x10, 0x00, 0x24, 0x01, 0x02, 0x00, 0x32, 0x00, 0x32, 0x00}
	for i, b := range want {
		if got[i] != b {
			t.Fatalf("byte %d is %#02x, want %#02x\n got %02x\nwant %02x",
				i, got[i], b, got[:10], want)
		}
	}
	if len(got) != ReportSize {
		t.Errorf("the report is %d bytes; the descriptor says %d", len(got), ReportSize)
	}
	// The tail is zero: a report is padded, not filled.
	for i := len(want); i < len(got); i++ {
		if got[i] != 0 {
			t.Errorf("byte %d of the padding is %#02x", i, got[i])
		}
	}
}

// TestAReadIsNotAWrite: they differ in the direction byte and in the length,
// and confusing them is what made nineteen attempts unreadable.
func TestAReadIsNotAWrite(t *testing.T) {
	r, w := ReadDisplayMode(), SetDisplayMode(Mode1920x1080At60)
	if r[3] != DirRead {
		t.Errorf("a read carries direction %#02x, want %#02x", r[3], DirRead)
	}
	if w[3] != DirWrite {
		t.Errorf("a write carries direction %#02x, want %#02x", w[3], DirWrite)
	}
	if r[4] == w[4] {
		t.Errorf("both carry the length byte %#02x; the device answers them differently", r[4])
	}
	if w[6] != 0x31 || w[8] != 0x31 {
		t.Errorf("the value is not written twice: %02x", w[:10])
	}
}

// TestTheReplyIsTheRequestPlusTheReplyBit.
//
// Measured by sweeping byte 3 and reading what came back: eleven of twelve
// values were answered, and every answer was the request's direction plus 0x20.
func TestTheReplyIsTheRequestPlusTheReplyBit(t *testing.T) {
	for _, c := range []struct{ req, reply byte }{
		{DirRead, 0x51},
		{DirWrite, 0x21},
		{0x00, 0x20},
		{0x41, 0x61},
	} {
		if got := c.req + ReplyBit; got != c.reply {
			t.Errorf("%#02x is answered on %#02x, want %#02x", c.req, got, c.reply)
		}
	}
}

// TestTheStatusCodesSeenOnTheWire, so the numbers are written down where the
// next person needs them rather than in a log nobody keeps.
func TestTheStatusCodesSeenOnTheWire(t *testing.T) {
	// A write's reply carries a status where a read's carries the value.
	reply := []byte{0x10, 0x00, 0x24, 0x21, 0x01, 0x00, 0x04, 0x00, 0x04}
	e, ok := ParseEvent(reply)
	if !ok {
		t.Fatal("a captured reply did not parse")
	}
	if e.Value != StatusRefused {
		t.Errorf("value %#x, want the refusal code %#x", e.Value, StatusRefused)
	}
	if e.Kind != DirWrite+ReplyBit {
		t.Errorf("kind %#02x, want a write's reply", e.Kind)
	}
	if StatusOK == StatusRefused || StatusRefused == StatusTooShort {
		t.Error("two status codes are the same number")
	}
}
