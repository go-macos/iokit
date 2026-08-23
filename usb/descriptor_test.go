package usb

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// beastConfig is the real configuration descriptor block the VITURE Beast XR
// Glasses (35ca:1201) published, captured with this package's own
// ConfigDescriptor on macOS. Testing the parser against a synthetic block would
// only prove it agrees with the test author; this block is what the hardware
// actually said, class-specific functional descriptors and all.
const beastConfig = "" +
	"09025b01060100c032080b00020202000409040000010202010005240010010424020205" +
	"2406000105240103010705840310000809040100020a0000000705030200020007058302" +
	"000200080b0203010020050904020000010120060924010002082e000008240a01030300" +
	"0011240203010100012000000000000000000c2403050103000301000000090403000001" +
	"022007090404000001022008090404010201022008102401030001010000000203000000" +
	"0006240201031807050205480a0408250100000000000705821104000809040402020102" +
	"200810240103000101000000020300000000062402010210070502050803040825010000" +
	"000000070582110400080904040302010220081024010300010100000002000000000006" +
	"24020102100705020568000408250100000000000705821104000809040500020300000e" +
	"0921100100012228000705850340000107050403400001"

func TestSplitOnRealHardware(t *testing.T) {
	b, err := hex.DecodeString(beastConfig)
	if err != nil {
		t.Fatal(err)
	}
	raws, err := Split(b)
	if err != nil {
		t.Fatalf("Split() = %v", err)
	}
	// Every descriptor must have been consumed exactly: the lengths must sum
	// back to the block. That is the property that proves the walk did not
	// drift.
	sum := 0
	for _, r := range raws {
		sum += len(r.Bytes)
		if int(r.Bytes[0]) != len(r.Bytes) {
			t.Errorf("descriptor type %#02x: bLength %d but %d bytes", r.Type, r.Bytes[0], len(r.Bytes))
		}
	}
	if sum != len(b) {
		t.Errorf("descriptors sum to %d bytes, block is %d", sum, len(b))
	}
}

// TestParseBeastConfig is the finding this package was written to reach,
// pinned as a test: the Beast is not merely a HID device. It publishes a
// CDC-ACM serial function with bulk endpoints alongside its audio and HID
// interfaces, and the HID interface everyone tries is the last of nine.
func TestParseBeastConfig(t *testing.T) {
	b, err := hex.DecodeString(beastConfig)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseConfig(b)
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	if c.Value != 1 {
		t.Errorf("bConfigurationValue = %d, want 1", c.Value)
	}
	if got := len(c.Interfaces); got != 9 {
		t.Fatalf("parsed %d interfaces, want 9", got)
	}
	if c.Unknown == 0 {
		t.Error("Unknown = 0; the block is full of class-specific descriptors that must be counted, not dropped")
	}
	// Interface 0/1: the CDC function.
	if got := c.Interfaces[0]; got.Class != ClassCDCControl || got.Number != 0 {
		t.Errorf("interface[0] = %v, want the CDC control interface", got)
	}
	data := c.Interfaces[1]
	if data.Class != ClassCDCData || len(data.Endpoints) != 2 {
		t.Fatalf("interface[1] = %v, want CDC data with 2 endpoints", data)
	}
	for _, tc := range []struct {
		ep   Endpoint
		addr byte
		dir  Direction
		num  int
		max  uint16
	}{
		{data.Endpoints[0], 0x03, Out, 3, 512},
		{data.Endpoints[1], 0x83, In, 3, 512},
	} {
		if tc.ep.Address != tc.addr || tc.ep.Direction() != tc.dir ||
			tc.ep.Number() != tc.num || tc.ep.MaxPacketSize != tc.max ||
			tc.ep.TransferType() != TransferBulk {
			t.Errorf("endpoint %#02x decoded as %v", tc.addr, tc.ep)
		}
	}
	// The last interface is the HID one, with its 64-byte interrupt pair.
	hid := c.Interfaces[8]
	if hid.Class != ClassHID || len(hid.Endpoints) != 2 || hid.Endpoints[0].MaxPacketSize != 64 {
		t.Errorf("interface[8] = %v, want the 64-byte HID interface", hid)
	}
	// The IAD must have been decoded: it is what says interfaces 0 and 1 are
	// one serial port rather than two unrelated interfaces.
	if len(c.Associations) != 2 {
		t.Fatalf("parsed %d associations, want 2", len(c.Associations))
	}
	if a := c.Associations[0]; a.FirstInterface != 0 || a.InterfaceCount != 2 || a.Class != ClassCDCControl {
		t.Errorf("association[0] = %v, want interfaces 0..1 as a CDC function", a)
	}
	if s := c.String(); !strings.Contains(s, "CDC-data") || !strings.Contains(s, "ep 0x83 in bulk") {
		t.Errorf("Config.String() = %q, want the CDC data endpoints named", s)
	}
}

func TestSplitRejectsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"a single trailing byte", []byte{0x09}},
		{"bLength below the header size", []byte{0x01, 0x02}},
		{"bLength past the end", []byte{0x09, 0x02, 0x00}},
		{"a good descriptor then a truncated one", []byte{0x02, 0x04, 0x09, 0x04, 0x00}},
	} {
		if _, err := Split(tc.in); !errors.Is(err, ErrBadDescriptor) {
			t.Errorf("%s: Split() = %v, want ErrBadDescriptor", tc.name, err)
		}
	}
	// Split returns what it managed to walk alongside the error, so a probe can
	// still report the good part of a broken block.
	got, err := Split([]byte{0x02, 0x04, 0x09})
	if err == nil || len(got) != 1 {
		t.Errorf("Split(partial) = %v, %v; want the first descriptor plus an error", got, err)
	}
	if empty, err := Split(nil); err != nil || empty != nil {
		t.Errorf("Split(nil) = %v, %v; want no descriptors and no error", empty, err)
	}
}

func TestParseConfigRejects(t *testing.T) {
	// Shorter than a configuration descriptor.
	if _, err := ParseConfig([]byte{9, 2, 9, 0}); !errors.Is(err, ErrBadDescriptor) {
		t.Errorf("ParseConfig(short) = %v, want ErrBadDescriptor", err)
	}
	// Long enough to be a configuration descriptor, but the block behind it is
	// malformed, so the walk fails.
	bad := []byte{9, 2, 12, 0, 1, 1, 0, 0xC0, 50, 0x00, 0x04, 0x00}
	if _, err := ParseConfig(bad); !errors.Is(err, ErrBadDescriptor) {
		t.Errorf("ParseConfig(bad block) = %v, want ErrBadDescriptor", err)
	}
}

// TestParseConfigOddCases covers the shapes real hardware occasionally emits
// and a parser must survive: a descriptor claiming a known type but too short
// to hold it, and an endpoint that arrives before any interface.
func TestParseConfigOddCases(t *testing.T) {
	cfg := []byte{9, 2, 0, 0, 1, 1, 0, 0xC0, 50}
	block := func(tail ...byte) []byte {
		b := append([]byte(nil), cfg...)
		b = append(b, tail...)
		b[2] = byte(len(b))
		return b
	}
	for _, tc := range []struct {
		name    string
		tail    []byte
		unknown int
	}{
		{"an interface descriptor too short to parse", []byte{4, DescInterface, 0, 0}, 1},
		{"an endpoint descriptor too short to parse", []byte{4, DescEndpoint, 0x81, 0x02}, 1},
		{"an association too short to parse", []byte{4, DescInterfaceAssociation, 0, 2}, 1},
		{"an endpoint before any interface", []byte{7, DescEndpoint, 0x81, 0x02, 0, 2, 0}, 1},
		{"a descriptor type this package does not decode", []byte{5, 0x24, 0, 1, 1}, 1},
	} {
		c, err := ParseConfig(block(tc.tail...))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Unknown != tc.unknown {
			t.Errorf("%s: Unknown = %d, want %d", tc.name, c.Unknown, tc.unknown)
		}
		if len(c.Interfaces) != 0 || len(c.Associations) != 0 {
			t.Errorf("%s: decoded %d interface(s) and %d association(s), want none",
				tc.name, len(c.Interfaces), len(c.Associations))
		}
	}
}

func TestClassName(t *testing.T) {
	for _, tc := range []struct {
		c    uint8
		want string
	}{
		{ClassAudio, "audio"},
		{ClassCDCControl, "CDC-control"},
		{ClassHID, "HID"},
		{ClassCDCData, "CDC-data"},
		{ClassVideo, "video"},
		{ClassMiscellaneous, "misc"},
		{ClassVendor, "vendor-specific"},
		{0x77, "class 0x77"},
	} {
		if got := className(tc.c); got != tc.want {
			t.Errorf("className(%#02x) = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestTransferTypeString(t *testing.T) {
	for _, tc := range []struct {
		t    TransferType
		want string
	}{
		{TransferControl, "control"},
		{TransferIsochronous, "isochronous"},
		{TransferBulk, "bulk"},
		{TransferInterrupt, "interrupt"},
		{TransferType(7), "TransferType(7)"},
	} {
		if got := tc.t.String(); got != tc.want {
			t.Errorf("TransferType(%d).String() = %q, want %q", uint8(tc.t), got, tc.want)
		}
	}
}

func TestInterfaceAndAssociationString(t *testing.T) {
	i := Interface{Number: 5, Alternate: 1, Class: ClassHID, SubClass: 0, Protocol: 0}
	if got := i.String(); !strings.Contains(got, "interface 5 alt 1 HID") {
		t.Errorf("Interface.String() = %q", got)
	}
	a := Association{FirstInterface: 2, InterfaceCount: 3, Class: ClassAudio}
	if got := a.String(); !strings.Contains(got, "interfaces 2..4") || !strings.Contains(got, "audio") {
		t.Errorf("Association.String() = %q", got)
	}
}
