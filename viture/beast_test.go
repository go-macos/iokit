package viture

import (
	"encoding/hex"
	"fmt"
	"strings"
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
			// ⛔⛔ THIS PAIR USED TO SAY "volume" AND "brightness", AND BOTH WERE
			// WRONG. The comment here read: "Volume ramps in nine steps;
			// brightness in three. Keeping one of each means the identifiers
			// cannot be swapped without a test saying so." The guard was the
			// right idea and it did not fire, because the two were not swapped
			// with each other -- they were BOTH misnamed, so the wrong pair was
			// pinned to the wrong names and the test agreed with itself. A
			// guard against a swap is no guard against a common error.
			//
			// What settles them is the CEILING, and TestEachAnnouncementStops-
			// WhereItsRangeEnds below now pins that instead of the names.
			"the film, most of the way up",
			[]byte{0x10, 0x0a, 0x30, 0x73, 0x01, 0x00, 0x08, 0x00},
			Event{Counter: 0x0a, ID: MsgElectrochromic, Kind: KindNotify2, Value: 8},
		},
		{
			"the volume, one step up",
			[]byte{0x10, 0x03, 0x01, 0x72, 0x01, 0x00, 0x02, 0x00},
			Event{Counter: 0x03, ID: MsgVolume, Kind: 0x72, Value: 2},
		},
		{
			// It went to zero unasked, in the same second the display entered
			// 3D. The two look mutually exclusive on this hardware.
			"the spatial anchoring giving way to 3D",
			[]byte{0x10, 0x00, 0x44, 0x71, 0x01, 0x00, 0x00, 0x00},
			Event{Counter: 0x00, ID: MsgNativeDOF, Kind: KindNotify, Value: 0},
		},
		{
			// Not the film: the film announces on 0x30, driven across its whole
			// range in both directions on 2026-09-07 while THIS id stayed
			// silent. What it is instead has not been established.
			"0x43 announcing, which is not the film",
			[]byte{0x10, 0x00, 0x43, 0x71, 0x01, 0x00, 0x02, 0x00},
			Event{Counter: 0x00, ID: MsgNativeTracking, Kind: KindNotify, Value: 2},
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
	{"volume stepping up", "1029017201000700070000000000000000000000", MsgVolume, KindNotify3, 7},
	{"the film stepping up", "1032307301000800080000000000000000000000", MsgElectrochromic, KindNotify2, 8},
	{"glasses put on", "1003217301000100010000000000000000000000", MsgWearStatus, KindNotify2, 1},
	{"glasses taken off", "1038217301000000000000000000000000000000", MsgWearStatus, KindNotify2, 0},
	{"the brightness", "1037227101000600060000000000000000000000", MsgBrightness, KindNotify, 6},
	// ⛔ THE FIRST THREE NAMES ABOVE, AND THESE TWO, WERE WRONG UNTIL
	// 2026-09-07 -- not the BYTES, which are evidence and untouched, only what
	// this file said they meant. These two were "the film going clear" and "the
	// film going dark"; the film is 0x30, and 0x43 stayed silent throughout a
	// full sweep of the film in both directions.
	{"0x43 announcing 0", "1034437101000000000000000000000000000000", MsgNativeTracking, KindNotify, 0},
	{"0x43 announcing 2", "1033437101000200020000000000000000000000", MsgNativeTracking, KindNotify, 2},
	{"a display mode", "1002427101003d003d00000000000000000000000000", MsgNativeDisplayMode, KindNotify, 0x3d},
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

// TestBothDisplayModeMessagesAreNamed.
//
// ⭐ THERE ARE TWO AND THEY ARE NOT A CONTRADICTION. Captured on 2026-09-05
// with the display at 3840x1080, both were present in the same session:
// MsgDisplayMode reported 0x32 (Mode3840x1080At60) and MsgNativeDisplayMode
// reported 0x37 (NativeMode3DSBS3840x1080At60). The glasses hold two settings
// that both describe what is in front of the eyes, and SpaceWalker names them
// apart -- R6SetDisplayModeHIDMsg and R6NewerNativeDisplayModeHIDMsg.
func TestBothDisplayModeMessagesAreNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		frame []byte
		id    byte
		value uint16
		is3D  bool
	}{
		{
			"the display mode, in a reply",
			[]byte{0x10, 0x00, 0x24, 0x51, 0x03, 0x00, 0x32, 0x00, 0x00, 0x32},
			MsgDisplayMode, Mode3840x1080At60, true,
		},
		{
			"the native mode, announced when the button was pressed",
			[]byte{0x10, 0x01, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
			MsgNativeDisplayMode, NativeMode3DSBS3840x1080At60, true,
		},
		{
			"and back to 2D",
			[]byte{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
			MsgNativeDisplayMode, Mode1920x1080At60, false,
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
			if Stereoscopic(e.Value) != c.is3D {
				t.Errorf("Stereoscopic(%#x) = %v", e.Value, Stereoscopic(e.Value))
			}
		})
	}
}

// TestTheLengthByteIsConstantPerMessage.
//
// ⛔ IT IS NOT THE PAYLOAD'S LENGTH, which is what its position invites anybody
// to assume. Five announcements of the native mode were captured while the
// button was pressed, alternating 0x31 and 0x37, and ALL carry 0x01 -- while a
// display-mode reply carries 0x03 and an ambient reading 0x02, in frames of the
// same shape and the same payload width. Whatever it means, it is a property of
// the MESSAGE and not of the value, which is what somebody building a frame
// needs to know.
func TestTheLengthByteIsConstantPerMessage(t *testing.T) {
	native := [][]byte{
		{0x10, 0x00, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
		{0x10, 0x01, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
		{0x10, 0x04, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
		{0x10, 0x05, 0x42, 0x71, 0x01, 0x00, 0x37, 0x00, 0x37},
		{0x10, 0x07, 0x42, 0x71, 0x01, 0x00, 0x31, 0x00, 0x31},
	}
	values := map[uint16]bool{}
	for _, f := range native {
		if f[4] != 0x01 {
			t.Errorf("a native-mode frame carries length %#02x", f[4])
		}
		e, ok := ParseEvent(f)
		if !ok {
			t.Fatalf("%02x did not parse", f)
		}
		values[e.Value] = true
	}
	if len(values) < 2 {
		t.Error("every captured frame carried the same value, so this proves nothing " +
			"about the length byte being independent of it")
	}
	// And two other messages, of the same shape, carrying other lengths.
	for _, c := range []struct {
		frame  []byte
		length byte
	}{
		{[]byte{0x10, 0x00, 0x24, 0x51, 0x03, 0x00, 0x32, 0x00, 0x00, 0x32}, 0x03},
		{[]byte{0x10, 0x00, 0x22, 0x51, 0x02, 0x00, 0x06, 0x00, 0x00, 0x06}, 0x02},
	} {
		if c.frame[4] != c.length {
			t.Errorf("frame %02x carries length %#02x, want %#02x", c.frame, c.frame[4], c.length)
		}
	}
}

// TestTheTwoModeReadsAreDifferentQuestions.
//
// ⚠ MEASURED 2026-09-06 on a Beast sitting at 1920x1080 at 60 Hz: a read of
// MsgDisplayMode answered 0x36, which is in NEITHER table -- not the display
// modes, not the native ones -- while at the same moment MsgNativeDisplayMode
// answered 0x31, exactly NativeMode1920x1080At60 and exactly what the panel
// was doing.
//
// So the two reads are not interchangeable, and a caller reaching for "the
// current mode" must reach for the second. This pins that they at least ASK
// different messages, which is the part a test can hold.
func TestTheTwoModeReadsAreDifferentQuestions(t *testing.T) {
	a, b := ReadDisplayMode(), ReadNativeDisplayMode()
	if len(a) != ReportSize || len(b) != ReportSize {
		t.Fatalf("reports are %d and %d bytes, want %d", len(a), len(b), ReportSize)
	}
	if a[2] != MsgDisplayMode {
		t.Errorf("ReadDisplayMode asks %#02x", a[2])
	}
	if b[2] != MsgNativeDisplayMode {
		t.Errorf("ReadNativeDisplayMode asks %#02x", b[2])
	}
	// Both are READS: they must not carry a value, or they are commands.
	if a[3] != DirRead || b[3] != DirRead {
		t.Errorf("one of them is not a read: %#02x %#02x", a[3], b[3])
	}
	for i := 6; i < 10; i++ {
		if a[i] != 0 || b[i] != 0 {
			t.Errorf("a read carries a value at byte %d: % x / % x", i, a[6:10], b[6:10])
			break
		}
	}
}

// ⛔⛔ THE ANNOUNCEMENT NUMBERING AND THE COMMAND NUMBERING COLLIDE, and this is
// the test that stops them being quietly merged again.
//
// 0x43 announces something not yet named and is written to set the tracking
// mode. 0x44 announces the tracking mode and is written to set the side mode.
// 0x30 announces the electrochromic film and is written to recentre.
//
// Somebody tidying these into one list would produce a package that reframes a
// person's display when asked to anchor it. That happened -- and then it
// happened AGAIN on 2026-09-07, from the other direction: values meant for the
// film were written to 0x43, which anchored a worn headset's picture off to one
// side while its wearer hunted for it.
func TestTheTwoNumberingsAreNotOneList(t *testing.T) {
	if MsgNativeTracking != CmdNativeDOF {
		t.Errorf("0x43 announces on one numbering and sets the tracking mode on the "+
			"other; they are the same byte and this test exists to say so: %#x vs %#x",
			MsgNativeTracking, CmdNativeDOF)
	}
	if MsgNativeDOF != CmdNativeSideMode {
		t.Errorf("0x44 is announced as the tracking mode and written as the side "+
			"mode: %#x vs %#x", MsgNativeDOF, CmdNativeSideMode)
	}
	if MsgElectrochromic != CmdNativeRecenter {
		t.Errorf("0x30 is announced as the film and written to recentre: %#x vs %#x",
			MsgElectrochromic, CmdNativeRecenter)
	}
	// And the ones that do NOT collide, so that a future edit which shifts a
	// command id is caught rather than absorbed.
	for name, got := range map[string]byte{
		"CmdNativeMode": CmdNativeMode, "CmdNativeDisplayMode": CmdNativeDisplayMode,
		"CmdNativeDisplayDistance": CmdNativeDisplayDistance,
		"CmdNativeDisplaySize":     CmdNativeDisplaySize,
	} {
		want := map[string]byte{
			"CmdNativeMode": 0x40, "CmdNativeDisplayMode": 0x42,
			"CmdNativeDisplayDistance": 0x27, "CmdNativeDisplaySize": 0x28,
		}[name]
		if got != want {
			t.Errorf("%s = %#x, and libglasses.so writes %#x", name, got, want)
		}
	}
}

// TestAnAnswerCanCarryTextAndNotANumber.
//
// ⭐ MEASURED ON A BEAST, 2026-09-07, by asking the vendor's own read ids.
// These are the bytes that came back, not bytes anybody composed:
//
//	0x3003 firmware -> "20.0.01.027_20260825"
//	0x3002 board SN -> "R6PMCC613005G"
//	0x3005 package SN -> 0xff repeated, which is how this headset says it has none
//
// The 16-bit field at 6..7 is a CHECKSUM on these, so Event.Value is
// meaningless for them and the text is the whole answer.
func TestAnAnswerCanCarryTextAndNotANumber(t *testing.T) {
	frame := func(hexHeader string, text []byte) []byte {
		b := make([]byte, ReportSize)
		var head []byte
		for i := 0; i+1 < len(hexHeader); i += 2 {
			var v byte
			fmt.Sscanf(hexHeader[i:i+2], "%02x", &v)
			head = append(head, v)
		}
		copy(b, head)
		copy(b[len(head):], text)
		return b
	}

	for _, c := range []struct {
		what, header string
		text         []byte
		want         string
	}{
		{"firmware", "100003501500 0e0400", []byte("20.0.01.027_20260825"), "20.0.01.027_20260825"},
		{"board serial", "100002500e00 210300", []byte("R6PMCC613005G"), "R6PMCC613005G"},
		// ⛔ 0xff REPEATED IS NOT A SERIAL. It is how the headset says it has
		// none, and the vendor's own tool tests exactly that byte.
		{"absent serial", "100005502100 e01f00", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ""},
	} {
		e, ok := ParseEvent(frame(strings.ReplaceAll(c.header, " ", ""), c.text))
		if !ok {
			t.Fatalf("%s: the frame did not parse", c.what)
		}
		if e.Text != c.want {
			t.Errorf("%s: Text = %q, want %q", c.what, e.Text, c.want)
		}
	}
}

// A frame with nothing after its header carries no text, and a length that
// overruns the report is clamped rather than believed.
func TestTextIsBoundedByTheReportAndNotByTheDevice(t *testing.T) {
	b := make([]byte, ReportSize)
	copy(b, []byte{EventHeader, 0x00, 0x03, 0x50, 0x01, 0x00, 0, 0, 0})
	if e, _ := ParseEvent(b); e.Text != "" {
		t.Errorf("a header-only frame gave %q", e.Text)
	}

	// ⛔ THE DEVICE OWN LENGTH IS NOT A PROMISE. 0xff is the largest a frame can
	// even express -- the header check refuses a non-zero high byte -- and 9 +
	// 255 already runs off the end of a 64-byte report.
	over := make([]byte, ReportSize)
	copy(over, []byte{EventHeader, 0x00, 0x03, 0x50, 0xff, 0x00, 0, 0, 0})
	copy(over[9:], []byte("ok"))
	if e, _ := ParseEvent(over); e.Text != "ok" {
		t.Errorf("an overrunning length gave %q, want the bytes actually there", e.Text)
	}
}

// The ceilings, captured on 2026-09-07 from a worn Beast while its wearer named
// which button they were pressing and drove each control to its stop. The bytes
// are the significant prefix the listener printed; the report is 64 bytes with
// the rest zero, and the fixtures above keep one at full length to show that.
var beastCeilingFrames = []struct {
	what  string
	frame []byte
	id    byte
	kind  byte
	// max is what the manufacturer documents for this model, and it is the
	// whole point: the three ranges are not the same, so a control driven to
	// its stop names itself without anybody having to remember what was
	// pressed first.
	max uint16
}{
	{"volume", []byte{0x10, 0xa2, 0x01, 0x72, 0x01, 0x00, 0x0f, 0x00, 0x0f}, MsgVolume, KindNotify3, 15},
	{"brightness", []byte{0x10, 0xa8, 0x22, 0x71, 0x01, 0x00, 0x08, 0x00, 0x08}, MsgBrightness, KindNotify, 8},
	{"electrochromic film", []byte{0x10, 0x72, 0x30, 0x73, 0x01, 0x00, 0x08, 0x00, 0x08}, MsgElectrochromic, KindNotify2, 8},
}

// TestEachAnnouncementStopsWhereItsRangeEnds pins the CEILING of each
// announcement, because the ceiling is what told these apart after their names
// had been wrong for five days.
//
// ⛔⛔ THE NAMES WERE NOT EVIDENCE, AND NOTHING IN THIS FILE HAD NOTICED. The
// mapping was first made by working each control in turn five seconds apart and
// attributing the announcements BY THEIR ORDER -- on a headset whose volume and
// brightness share ONE pair of buttons, with a third button switching which.
// Three of the four names were wrong, and the test that was supposed to catch a
// swap did not, because they were not swapped with each other: they were all
// misnamed together, so the wrong frames were pinned to the wrong names and
// everything agreed with itself.
//
// ⭐ A RANGE CANNOT SLIDE. The manufacturer documents a volume of [0, 15] for
// this model against a brightness and a film of [0, 8]. The volume frame below
// carries 15, which the old naming called the brightness -- so this test, had
// it existed, would have failed on the day the mistake was made.
func TestEachAnnouncementStopsWhereItsRangeEnds(t *testing.T) {
	for _, c := range beastCeilingFrames {
		t.Run(c.what, func(t *testing.T) {
			got, ok := ParseEvent(c.frame)
			if !ok {
				t.Fatal("a frame from the wire was not recognised")
			}
			if got.ID != c.id || got.Kind != c.kind {
				t.Fatalf("got id %#02x kind %#02x, want %#02x and %#02x",
					got.ID, got.Kind, c.id, c.kind)
			}
			if got.Value != c.max {
				t.Errorf("the %s stopped at %d, and its documented range ends at %d: "+
					"either the capture is not of the %s or the range is not this one",
					c.what, got.Value, c.max, c.what)
			}
		})
	}
	// ⛔ AND THE RANGES MUST STAY TELLING. If a future edit gave two of these
	// the same ceiling, the measurement that settled them would stop settling
	// anything and nothing here would say so.
	if beastCeilingFrames[0].max == beastCeilingFrames[1].max {
		t.Error("the volume and the brightness now claim the same ceiling, which is " +
			"what made them distinguishable at all")
	}
}
