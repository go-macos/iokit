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
			Event{Counter: 0x00, ID: MsgDisplayMode, Kind: KindNotify, Value: NativeMode3DSBS3840x1200At60},
		},
		{
			"and back out of it",
			[]byte{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31, 0x00},
			Event{Counter: 0x04, ID: MsgDisplayMode, Kind: KindNotify, Value: Mode1920x1080At60},
		},
		{
			"the toggle that brackets it",
			[]byte{0x10, 0x00, 0x21, 0x73, 0x01, 0x00, 0x01, 0x00},
			Event{Counter: 0x00, ID: MsgToggle, Kind: KindNotify2, Value: 1},
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
