package viture

import "encoding/binary"

// The frame the CURRENT generation speaks, which is not the one above.
//
// The protocol in this file was read off the wire from a VITURE Beast on
// 2026-09-01, by opening its MCU interface as a SECOND HID client while the
// manufacturer's own application drove it. macOS delivers input reports to
// every process that has a device open, so nothing had to be intercepted,
// injected or captured at bus level -- which matters, because that application
// runs with the hardened runtime and System Integrity Protection was on, so
// there was no other way in.
//
// What it corrects: this package used to say the newer generations "accept
// these packets on their HID interface and answer nothing at all". They answer
// perfectly well. They simply do not speak the older protocol -- not one frame
// observed validates against it -- and the silence was ours.
//
// The device is 35ca:1201, "VITURE Beast XR Glasses", usage page 0xff00 usage
// 0x01, 64 bytes in and out. Its two 35ca:1102 siblings are the microphone and
// the buttons, on the consumer page.
//
// A frame:
//
//	0  0x10        constant
//	1  counter     increments once per message
//	2  message id
//	3  kind        0x50..0x52 on replies to the application, 0x71 and 0x73
//	               on state the glasses announce by themselves
//	4  length      of the value
//	5  0x00
//	6.. value, little endian, and then repeated once
//
// Two payloads read as plain text and settle the layout beyond argument: a
// firmware version and the glasses' own serial number arrived in the reply
// bodies, at the offset the length field points to.

// EventHeader is the first byte of every frame of this generation.
const EventHeader byte = 0x10

// Frame kinds, as observed. A reply to something the application asked carries
// 0x50 to 0x52; a state change the glasses announce by themselves carries 0x71,
// and 0x73 was seen on a second message that toggles between one and zero.
//
// These are named because they were SEEN, not because the meaning is settled:
// nothing here claims to know why an announcement is sometimes 0x71 and
// sometimes 0x73.
const (
	KindReply   byte = 0x50
	KindNotify  byte = 0x71
	KindNotify2 byte = 0x73
)

// More kinds, seen on 2026-09-02 while the manufacturer's own application held
// a session open and every control was worked in turn.
//
// The reply kinds are not one value but four: which one comes back appears to
// depend on the shape of the answer rather than on the question, since the same
// message id was answered with different ones. KindAck is what came back when
// the application APPLIED a setting rather than asked for one.
//
// These are named because they were SEEN. Nothing here claims to know the rule.
const (
	KindReplyB byte = 0x51
	KindReplyC byte = 0x52
	KindReplyD byte = 0x54
	// KindNotify3 carried MsgBrightness, where volume and wear status came on
	// KindNotify2 and the ambient, mode and film messages on KindNotify. The
	// three announcement kinds therefore do NOT split by message id alone.
	KindNotify3 byte = 0x72
	KindAck     byte = 0x21
)

// The messages, each identified by provoking ONE function at a time and
// reading the clock.
//
// The manufacturer's SDK exposes exactly six things -- brightness, volume,
// display mode, electrochromic film, native 3DOF and wear status -- and
// wheaney/XRLinuxDriver publishes that list. On 2026-09-02 each was exercised
// in turn, five seconds apart, while this package listened. Every one of the
// six announced itself, and the spacing is what makes the mapping an
// observation rather than a guess.
//
// Two of them corroborate independently of the order they were done in:
// MsgDisplayMode was already known from the Mac's own display list changing
// with it, and MsgWearStatus read 1 from the first second -- the glasses were
// being worn -- and then 0, 1, 0 as they were taken off and put back on.
const (
	// MsgBrightness carries the brightness step. Seen 1, 2, 1, 0 while it was
	// raised twice and lowered twice.
	MsgBrightness byte = 0x01
	// MsgWearStatus is 1 while the glasses are on a face and 0 when they are
	// not. It is the one message that moves without anybody pressing anything.
	MsgWearStatus byte = 0x21
	// MsgDisplayBrightness is the SETTABLE brightness of the display, 0 to 8.
	//
	// ⛔ IT WAS CALLED MsgDisplayBrightness AND THAT WAS WRONG. The old comment read
	// "moves on its own... which is what an ambient light reading would do.
	// Named for what it appears to track, and this comment is the whole of the
	// evidence" -- an honest hedge, and refuted on 2026-09-05 by writing it:
	// set to 3 and read back 3, set to 7 and read back 7, and the display
	// changed. A sweep found the edges the same way, with no watching at all:
	// accepted from 0 to 8, REFUSED from 9 up. The disassembly agrees --
	// SpaceWalker names setBrightness = 0x0122.
	//
	// ⚠ WHETHER IT IS THE SAME QUANTITY AS [MsgBrightness] IS NOT ESTABLISHED.
	// 0x01 is what the headset ANNOUNCES when the button on the arm is pressed;
	// this is what it accepts being told. They may be one setting seen from two
	// sides, and nothing here has shown that.
	MsgDisplayBrightness byte = 0x22
	// MsgVolume carries the volume step, 0 to 8. Seen ramping all the way up
	// and all the way back down.
	MsgVolume byte = 0x30
	// MsgElectrochromic carries the opacity of the film: 0, 1 and 2 were seen.
	MsgElectrochromic byte = 0x43
	// MsgNativeDOF is 1 when the glasses anchor their picture in space and 0
	// when it follows the head.
	//
	// It went to 0 in the same second the display entered 3D, without being
	// asked: the two appear to be mutually exclusive on this hardware, which is
	// worth knowing before offering both at once in a menu.
	MsgNativeDOF byte = 0x44
)

// The two messages that carry a display mode.
//
// ⭐ THERE ARE TWO, AND THEY ARE NOT A CONTRADICTION. The glasses hold two
// settings that both describe what is in front of the eyes, and both were seen
// at once on 2026-09-05 with the display at 3840x1080: MsgDisplayMode reported
// 0x32 (Mode3840x1080At60) and MsgNativeDisplayMode reported 0x37
// (NativeMode3DSBS3840x1080At60). SpaceWalker names them apart too --
// R6SetDisplayModeHIDMsg and R6NewerNativeDisplayModeHIDMsg -- and the msgIDs
// disassembled out of it, 0x0124 and 0x0142, have exactly these low bytes.
//
// Identified by CORRELATION rather than by guessing, twice over and months
// apart. On 2026-09-02 MsgNativeDisplayMode reported 0x3A -- exactly
// NativeMode3DSBS3840x1200At60 -- as the Mac's display list changed to
// 3840x1200 in the same second, and 0x31 on the way back. On 2026-09-05 it
// went 0x31 then 0x37 as the display became 3840x1080.
const (
	// MsgDisplayMode is the mode a host sets and reads: setDisplayMode 0x0124,
	// getDisplayMode 0x3124.
	// ⭐ AND ONE OF THEM TAKES ORDERS. Writing 0x32 to MsgDisplayMode put a
	// Beast into side-by-side 3D -- the Mac's display list changed to
	// 3840x1080 in the same second, its model number 0x120 to 0x220 -- and
	// 0x31 brought it back. See SetDisplayMode.
	MsgDisplayMode byte = 0x24
	// MsgNativeDisplayMode is the headset's own native mode, 0x0142. It is the
	// one that ANNOUNCES itself when the button is pressed.
	MsgNativeDisplayMode byte = 0x42
)

// Event is one frame from the glasses.
type Event struct {
	// Counter increments once per message. It is the only thing that says two
	// identical readings are two events rather than one seen twice.
	Counter byte
	// ID says what the message is about; the two display-mode messages are
	// named above.
	ID byte
	// Kind separates a reply from an announcement.
	//
	// ⛔ ITS RULE IS NOT KNOWN, and a plausible one has already been tried and
	// refused. It looked like the high byte of a 16-bit identifier whose top
	// nibble is a direction -- SpaceWalker's disassembled table does read that
	// way, giving getBrightness 0x3122 and setBrightness 0x0122. But the frames
	// captured from these glasses announce brightness with 0x72, volume with
	// 0x73 and the ambient reading with 0x71: three values for what is plainly
	// the same direction, which that rule cannot explain. See
	// TestTheThreeAnnouncementKindsDoNotSplitByMessage.
	Kind byte
	// Value is the message's value, which for MsgDisplayMode is a display mode.
	Value uint16
}

// ParseEvent reads a frame of this generation, and reports whether it is one.
//
// It is deliberately strict about the two constant bytes: a report that is not
// this shape is far more likely to be another protocol than a corrupted frame
// of this one, and treating it as the latter would invent events.
func ParseEvent(b []byte) (Event, bool) {
	if len(b) < 8 || b[0] != EventHeader || b[5] != 0x00 {
		return Event{}, false
	}
	return Event{
		Counter: b[1],
		ID:      b[2],
		Kind:    b[3],
		Value:   binary.LittleEndian.Uint16(b[6:8]),
	}, true
}

// Display modes.
//
// These values are not guessed: they are published in wheaney/XRLinuxDriver
// (include/sdks/viture_protocol_public.h), and two of them were then seen on
// the wire here, carried by MsgDisplayMode, at the moment the Mac's own display
// list changed to match. A number that appears in an independent source AND
// explains an observation is a fact; either alone is a hypothesis.
const (
	Mode1920x1080At60  uint16 = 0x31
	Mode3840x1080At60  uint16 = 0x32 // 3D
	Mode1920x1080At90  uint16 = 0x33
	Mode1920x1080At120 uint16 = 0x34
	Mode3840x1080At90  uint16 = 0x35 // 3D
	Mode1920x1200At60  uint16 = 0x41
	Mode3840x1200At60  uint16 = 0x42 // 3D
	Mode1920x1200At90  uint16 = 0x43
	Mode1920x1200At120 uint16 = 0x44
	Mode3840x1200At90  uint16 = 0x45 // 3D

	NativeMode3DSBS3840x1080At60  uint16 = 0x37
	NativeMode3DSBS3840x1080At90  uint16 = 0x38
	NativeMode3DSBS3840x1080At120 uint16 = 0x39
	NativeMode3DSBS3840x1200At60  uint16 = 0x3A
	NativeMode3DSBS3840x1200At90  uint16 = 0x3B
	NativeMode3DSBS3840x1200At120 uint16 = 0x3C

	ModeUltrawide60To120 uint16 = 0x51
	ModeSideBySide60     uint16 = 0x61
)

// Stereoscopic reports whether a display mode puts a different picture in front
// of each eye.
//
// It is the only question most callers have, and answering it from a list is
// better than from the resolution: 3840 wide is not by itself the test, since
// an ultrawide mode is also wider than one eye.
func Stereoscopic(mode uint16) bool {
	switch mode {
	case Mode3840x1080At60, Mode3840x1080At90,
		Mode3840x1200At60, Mode3840x1200At90,
		NativeMode3DSBS3840x1080At60, NativeMode3DSBS3840x1080At90,
		NativeMode3DSBS3840x1080At120, NativeMode3DSBS3840x1200At60,
		NativeMode3DSBS3840x1200At90, NativeMode3DSBS3840x1200At120,
		ModeSideBySide60:
		return true
	}
	return false
}

// ModeName names a display mode for a log or a menu, and says plainly when it
// does not know one.
func ModeName(mode uint16) string {
	switch mode {
	case Mode1920x1080At60:
		return "1920x1080 at 60 Hz"
	case Mode3840x1080At60:
		return "3840x1080 at 60 Hz, 3D"
	case Mode1920x1080At90:
		return "1920x1080 at 90 Hz"
	case Mode1920x1080At120:
		return "1920x1080 at 120 Hz"
	case Mode3840x1080At90:
		return "3840x1080 at 90 Hz, 3D"
	case Mode1920x1200At60:
		return "1920x1200 at 60 Hz"
	case Mode3840x1200At60:
		return "3840x1200 at 60 Hz, 3D"
	case Mode1920x1200At90:
		return "1920x1200 at 90 Hz"
	case Mode1920x1200At120:
		return "1920x1200 at 120 Hz"
	case Mode3840x1200At90:
		return "3840x1200 at 90 Hz, 3D"
	case NativeMode3DSBS3840x1080At60:
		return "native 3D side by side, 3840x1080 at 60 Hz"
	case NativeMode3DSBS3840x1080At90:
		return "native 3D side by side, 3840x1080 at 90 Hz"
	case NativeMode3DSBS3840x1080At120:
		return "native 3D side by side, 3840x1080 at 120 Hz"
	case NativeMode3DSBS3840x1200At60:
		return "native 3D side by side, 3840x1200 at 60 Hz"
	case NativeMode3DSBS3840x1200At90:
		return "native 3D side by side, 3840x1200 at 90 Hz"
	case NativeMode3DSBS3840x1200At120:
		return "native 3D side by side, 3840x1200 at 120 Hz"
	case ModeUltrawide60To120:
		return "ultrawide, 60 Hz in and 120 Hz out"
	case ModeSideBySide60:
		return "side by side at 60 Hz"
	}
	return "an unnamed mode"
}

// The direction a frame carries, in byte 3 of a report.
//
// ⭐ MEASURED, by sweeping the byte and reading what came back. The device
// answers EVERY well-formed report, and its answer is the request's byte 3 plus
// 0x20: a read on 0x31 is answered on 0x51, a write on 0x01 on 0x21. Twelve
// values were tried and eleven were answered; only 0x11 is silent.
const (
	// DirWrite sets a value. The reply carries a STATUS, not the value.
	DirWrite byte = 0x01
	// DirRead asks for one. The reply carries the value.
	DirRead byte = 0x31
	// ReplyBit is what the device adds to a request's direction to make the
	// reply's: 0x31 is answered on 0x51.
	ReplyBit byte = 0x20
)

// What a write is answered with.
//
// ⛔ THIS IS THE INSTRUMENT THAT MATTERS. Nineteen attempts to set a display
// mode were judged by whether the Mac's screen changed, which cannot tell
// "refused" from "not understood" from "never arrived" -- and every one of them
// looked identical. The device says which, immediately, on every write.
const (
	// StatusOK means the command was taken.
	StatusOK uint16 = 0
	// StatusRefused is a well-formed command this device will not take: a mode
	// its panel does not have, for instance. 0x33 (120 Hz) and 0x51
	// (ultrawide) come back with this on a Beast.
	StatusRefused uint16 = 4
	// StatusTooShort is a length byte below two.
	StatusTooShort uint16 = 6
)

// SetDisplayMode is the report that puts the glasses into a display mode.
//
// ⭐ THE VALUE IS WRITTEN TWICE, LITTLE-ENDIAN, PACKED -- and that one detail is
// what nineteen failures had wrong. The device's own REPLIES carry a value
// little-endian and then again BIG-endian, so that shape was copied into the
// command; it is answered with StatusRefused. Written 32 00 32 00 with the
// length byte 0x02, the same command is answered StatusOK and the Mac's display
// list changes to 3840x1080 in the same second.
//
// A reply and a command are not the same frame. Reading one to build the other
// is what cost the nineteen.
//
// The report is 64 bytes because that is what the descriptor advertises;
// [MsgDisplayMode] and the mode constants say what may go in it.
func SetDisplayMode(mode uint16) []byte {
	return command(MsgDisplayMode, 0x02, mode)
}

// ReadDisplayMode is the report that asks which mode the glasses are in.
//
// ⛔ A READ CHANGES NOTHING, which is what makes it the right first experiment
// on any device: it is safe to repeat, and its success is visible in a way a
// command's is not. It was a read that proved these reports reach the glasses
// at all, after nineteen writes had failed to say so either way.
func ReadDisplayMode() []byte { return command(MsgDisplayMode, 0x03, 0) }

// command builds a report of this generation.
func command(msg, length byte, value uint16) []byte {
	b := make([]byte, ReportSize)
	dir := DirWrite
	if length == 0x03 && value == 0 {
		dir = DirRead
	}
	copy(b, []byte{
		EventHeader, 0x00, msg, dir, length, 0x00,
		byte(value), byte(value >> 8),
		byte(value), byte(value >> 8),
	})
	return b
}

// ReportSize is how many bytes a report to this device carries, from its own
// descriptor.
const ReportSize = 64
