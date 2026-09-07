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
	// KindNotify3 carries [MsgVolume], where the film and the wear status come
	// on KindNotify2 and the brightness and the mode on KindNotify. The three
	// announcement kinds therefore do NOT split by message id alone.
	//
	// ⛔ THE GROUPING HELD; THE NAMES IN IT DID NOT. What this comment used to
	// call the brightness is the volume, measured 2026-09-07. See [MsgVolume].
	KindNotify3 byte = 0x72
	KindAck     byte = 0x21
)

// The messages, each identified by DRIVING IT TO BOTH ITS STOPS and reading
// the number it stopped at.
//
// ⛔⛔ THREE OF THESE CARRIED THE WRONG NAME UNTIL 2026-09-07, AND THE METHOD
// IS WHY. The mapping used to be made by working each control in turn five
// seconds apart and attributing the announcements BY THEIR ORDER. But this
// headset has ONE pair of buttons for the volume and the brightness, with a
// third button switching which of the two they drive -- so an order assumed is
// not an order observed, and the whole mapping had slid by one.
//
// ⭐ WHAT REPLACED IT COSTS NOTHING AND CANNOT SLIDE: the CEILING. The
// manufacturer documents a volume of [0, 15], a brightness of [0, 8] and a
// film of [0, 8] for this model, and the three ranges are enough to tell the
// first from the other two outright. Drive a control to its stop, read the
// number, and no assumption about the order of anything is needed. Every
// constant below was redone that way, glasses worn, with the wearer naming
// which button they were pressing.
const (
	// MsgVolume carries the volume step, 0 to 15.
	//
	// ⛔ IT WAS CALLED MsgBrightness. Driven to its stop it announces 15, and
	// only the volume goes past 8 on this model. The disassembly agrees:
	// SpaceWalker names volumeUpdate = 0x7201, which is this id on
	// [KindNotify3], the kind it was seen on.
	MsgVolume byte = 0x01
	// MsgWearStatus is 1 while the glasses are on a face and 0 when they are
	// not. It is the one message that moves without anybody pressing anything.
	//
	// ⭐ CONFIRMED AGAIN 2026-09-07: it announced 1 as the glasses went on and
	// 0 as they came off, unprompted, in the middle of an unrelated capture.
	MsgWearStatus byte = 0x21
	// MsgBrightness is the brightness of the display, 0 to 8, and it is
	// SETTABLE: set to 3 and read back 3, set to 7 and read back 7, and the
	// display changed. A sweep found the edges with no watching at all:
	// accepted from 0 to 8, REFUSED from 9 up. The disassembly agrees twice --
	// SpaceWalker names setBrightness = 0x0122 and brightnessUpdate = 0x7122.
	//
	// ⭐ THE DOUBT THAT USED TO SIT HERE IS SETTLED. It read "whether it is the
	// same quantity as MsgBrightness is not established ... they may be one
	// setting seen from two sides, and nothing here has shown that" -- an
	// honest hedge, and the right one to have kept. They are NOT one setting:
	// 0x01 is the volume and stops at 15, this is the brightness and stops at
	// 8, and each was driven to both its stops while the other did not move.
	MsgBrightness byte = 0x22
	// MsgElectrochromic carries the opacity of the film, 0 to 8 -- the nine
	// tint levels this model is sold with.
	//
	// ⛔⛔ IT WAS 0x43, WHICH IS NOT AN ANNOUNCEMENT AT ALL. 0x43 WRITTEN is
	// [CmdNativeDOF], so writing "film" values into it anchors the picture in
	// the room instead of tinting anything -- met on a real desk, where the
	// wearer lost their screen off to one side and had to turn their head to
	// find it. The three values "0, 1 and 2" the old comment reported as tint
	// levels are the three tracking modes.
	//
	// ⚠ AND 0x30 IS [CmdNativeRecenter] WHEN WRITTEN. Conclude nothing from
	// that: the two directions are separate namespaces on this hardware.
	//
	// The wearer named the button, drove it to both stops -- 8 and 0 -- and
	// confirmed the lenses changed tint while it moved. That last witness is
	// the only one in this file that no software interprets.
	MsgElectrochromic byte = 0x30
	// MsgNativeTracking is 0x43 announcing, which USED to be called the film.
	//
	// ⚠ NAMED FOR WHERE IT SITS, NOT FOR WHAT IT CARRIES. It has been seen
	// announcing 0 and 2, and 0x43 WRITTEN is [CmdNativeDOF], whose three modes
	// are 0, 1 and 2 -- so the tracking mode is the obvious reading. It is not
	// the established one: on 2026-09-07 the film was driven across its whole
	// range, in both directions, and THIS ID NEVER ANNOUNCED. That rules out
	// the film and nothing more. Whoever needs it should drive the tracking
	// modes and watch, the way the four above were settled.
	MsgNativeTracking byte = 0x43
	// MsgNativeDOF is 1 when the glasses anchor their picture in space and 0
	// when it follows the head.
	//
	// It went to 0 in the same second the display entered 3D, without being
	// asked: the two appear to be mutually exclusive on this hardware, which is
	// worth knowing before offering both at once in a menu.
	MsgNativeDOF byte = 0x44
)

// ⛔⛔ WHAT THE HEADSET ANNOUNCES AND WHAT IT IS TOLD ARE NUMBERED DIFFERENTLY,
// AND THE TWO COLLIDE ON THE SAME BYTES.
//
// The constants above were measured from FRAMES THE HEADSET SENT, one SDK
// function exercised at a time. The ones below are what the manufacturer's own
// library WRITES, read out of wheaney/XRLinuxDriver's libglasses.so. On the
// native family they disagree, and both are right:
//
//	byte   announced as           written as
//	0x43   electrochromic film    native DOF
//	0x44   native DOF             native side mode
//	0x30   volume                 recentre the anchored picture
//
// ⛔ CONFLATING THEM COSTS AN AFTERNOON AND SOMEBODY'S DISPLAY. Writing 1 to
// 0x44 in the belief that it was the tracking mode made the desk small and put
// it bottom-left -- a layout setting doing exactly what a layout setting does --
// and nothing anchored, because the tracking register had not been touched.
//
// The two readings were reconciled by a third measurement that belongs to
// neither: READING 0x43 while somebody worked the headset's own 3DOF button. It
// went 0 → 1 → 0 → 1 in step with the button while 0x40, 0x42 and 0x44 stood
// still. A read is in the command numbering, so 0x43 is the tracking mode
// there; the captured frames in the tests are in the announcement numbering, so
// 0x43 is the film there. Neither observation was careless and neither is
// wrong.
const (
	// CmdNativeMode is bypass (0) or native (1): whether the glasses show the
	// host's video as it comes, or composite it themselves.
	//
	// ⛔ NATIVE TRACKING NEEDS THIS FIRST. The public header says native DOF is
	// refused outright while the device is in bypass mode, and that after
	// setting this one a display mode has to be set "to complete the switch".
	CmdNativeMode byte = 0x40
	// CmdNativeDisplayMode is which native mode the composited picture is in.
	CmdNativeDisplayMode byte = 0x42
	// CmdNativeDOF is the native tracking mode: 0 none, 1 3DOF, 2 smooth follow.
	//
	// ⭐ Confirmed by the headset's own button, which is the one witness that
	// belongs to neither numbering: reading this while it was pressed gave
	// 0 → 1 → 0 → 1.
	CmdNativeDOF byte = 0x43
	// CmdNativeSideMode is the native side mode. Writing it reframes the
	// picture; it has nothing to do with tracking.
	CmdNativeSideMode byte = 0x44
	// CmdNativeRecenter puts the anchored picture back in front of the viewer.
	// The vendor library writes it with a payload of zero.
	CmdNativeRecenter byte = 0x30
	// CmdNativeDisplayDistance is how far the anchored picture sits, 1 to 10.
	CmdNativeDisplayDistance byte = 0x27
	// CmdNativeDisplaySize is how large it is, 0 to 4.
	CmdNativeDisplaySize byte = 0x28
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
	// Value is the message value, which for MsgDisplayMode is a display mode.
	Value uint16
	// Text is what follows the header, for the messages that answer with TEXT
	// rather than a number.
	//
	// ⭐ MEASURED ON A BEAST, 2026-09-07. Asking 0x3002 and 0x3003 answers a
	// board serial and a firmware version as ASCII:
	//
	//	10 00 03 50 15 00 0e 04 00 "20.0.01.027_20260825"
	//	10 00 02 50 0e 00 21 03 00 "R6PMCC613005G"
	//
	// The 16-bit field at 6..7 is a CHECKSUM on these, not a value, so Value is
	// meaningless for them and Payload is the whole answer. It is nil when the
	// frame carries none.
	//
	// ⭐ A STRING, AND DELIBERATELY. Bytes would make an Event uncomparable,
	// and callers compare Events with == today. A string also copies, so it
	// survives the report buffer the callback hands over and is reused.
	Text string
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
		Text:    textOf(b),
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
//
// ⚠ BUT DO NOT TRUST WHAT IT ANSWERS. Measured 2026-09-06 on a Beast sitting at
// 1920x1080 at 60 Hz: this read returned 0x36, which is in NEITHER table -- not
// the display modes above, not the native ones. At the same moment
// [ReadNativeDisplayMode] returned 0x31, which is exactly
// NativeMode1920x1080At60 and exactly what the panel was doing.
//
// So MsgDisplayMode ACCEPTS the values above -- writing 0x31 and 0x34 moved the
// panel between 60 Hz and 120 Hz, and 0x32 switched it to side-by-side -- while
// what it REPORTS is in some other encoding nobody here has explained. A
// caller that wants to know the current mode should ask 0x42.
func ReadDisplayMode() []byte { return command(MsgDisplayMode, 0x03, 0) }

// ReadNativeDisplayMode asks the same question of the message that answers it
// in the documented vocabulary.
//
// ⭐ THIS IS THE ONE TO ASK. Its answers land on the NativeMode constants and
// were confirmed against the panel: 0x31 while the glasses presented
// 1920x1080 at 60 Hz, 0x37 while they presented 3840x1080 side by side.
func ReadNativeDisplayMode() []byte { return command(MsgNativeDisplayMode, 0x03, 0) }

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

// textOf is the text an answer carries after its header, or "".
//
// ⛔ THE LENGTH FIELD IS NOT TRUSTED FURTHER THAN THE BUFFER. It is the
// device's own number and it counts the status byte with the text, so a frame
// claiming more than it holds is clamped rather than read past. A report that
// is all padding after the header carries nothing, and says so.
func textOf(b []byte) string {
	const header = 9 // 0..7 as above, then one status byte
	if len(b) < header {
		return ""
	}
	// Length covers the status byte and the text; the text is one shorter.
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	if n < 1 {
		return ""
	}
	end := header + n - 1
	if end > len(b) {
		end = len(b)
	}
	if end <= header {
		return ""
	}
	// ⛔ TRAILING NULs AND 0xFF PADDING ARE NOT TEXT. A serial the headset does
	// not have comes back as 0xff repeated, which is the vendor own test for
	// absent, and the fixed-width fields are NUL-padded.
	out := b[header:end]
	for len(out) > 0 && (out[len(out)-1] == 0x00 || out[len(out)-1] == 0xff) {
		out = out[:len(out)-1]
	}
	return string(out)
}
