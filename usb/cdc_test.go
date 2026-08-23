package usb

import (
	"errors"
	"testing"
)

func TestParseLineCoding(t *testing.T) {
	// 115200 8N1 on the wire: rate little-endian, then stop bits, parity, data.
	got, err := ParseLineCoding([]byte{0x00, 0xC2, 0x01, 0x00, Stop1, ParityNone, 8})
	if err != nil {
		t.Fatal(err)
	}
	want := LineCoding{Rate: 115200, StopBits: Stop1, Parity: ParityNone, DataBits: 8}
	if got != want {
		t.Fatalf("ParseLineCoding = %+v, want %+v", got, want)
	}
	if got.Zero() {
		t.Error("a populated coding must not report itself zero")
	}
}

func TestParseLineCodingIgnoresTrailingBytes(t *testing.T) {
	got, err := ParseLineCoding([]byte{1, 0, 0, 0, Stop2, ParityEven, 7, 0xFF, 0xFF})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rate != 1 || got.StopBits != Stop2 || got.Parity != ParityEven || got.DataBits != 7 {
		t.Errorf("ParseLineCoding = %+v", got)
	}
}

func TestParseLineCodingTooShort(t *testing.T) {
	for _, n := range []int{0, 1, 6} {
		if _, err := ParseLineCoding(make([]byte, n)); !errors.Is(err, ErrShortLineCoding) {
			t.Errorf("%d byte(s): %v", n, err)
		}
	}
}

// TestZeroIsTheUnimplementedAnswer records the shape of the reply that means
// "the device ACKed the request and has no line coding": seven zero bytes.
func TestZeroIsTheUnimplementedAnswer(t *testing.T) {
	lc, err := ParseLineCoding(make([]byte, LineCodingSize))
	if err != nil {
		t.Fatal(err)
	}
	if !lc.Zero() {
		t.Error("seven zero bytes should report Zero")
	}
}

func TestLineCodingBytesRoundTrip(t *testing.T) {
	for _, want := range []LineCoding{
		{Rate: 115200, StopBits: Stop1, Parity: ParityNone, DataBits: 8},
		{Rate: 3000000, StopBits: Stop2, Parity: ParityOdd, DataBits: 5},
		{Rate: 921600, StopBits: Stop1p5, Parity: ParitySpace, DataBits: 16},
	} {
		b := want.Bytes()
		if len(b) != LineCodingSize {
			t.Fatalf("Bytes gave %d byte(s), want %d", len(b), LineCodingSize)
		}
		got, err := ParseLineCoding(b)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("round trip changed %+v into %+v", want, got)
		}
	}
}

func TestLineCodingString(t *testing.T) {
	for _, tc := range []struct {
		lc   LineCoding
		want string
	}{
		{LineCoding{115200, Stop1, ParityNone, 8}, "115200 8N1"},
		{LineCoding{9600, Stop2, ParityOdd, 7}, "9600 7O2"},
		{LineCoding{9600, Stop1p5, ParityEven, 8}, "9600 8E1.5"},
		{LineCoding{9600, Stop1, ParityMark, 8}, "9600 8M1"},
		{LineCoding{9600, Stop1, ParitySpace, 8}, "9600 8S1"},
		{LineCoding{9600, 9, 42, 8}, "9600 8?42?9"},
	} {
		if got := tc.lc.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

func TestCDCSetupPackets(t *testing.T) {
	get := GetLineCoding(1)
	if get.RequestType() != 0xA1 {
		t.Errorf("GET_LINE_CODING bmRequestType = %#02x, want 0xa1", get.RequestType())
	}
	if get.Request != ReqGetLineCoding || get.Index != 1 {
		t.Errorf("GET_LINE_CODING = %s", get)
	}

	set := SetLineCoding(0)
	if set.RequestType() != 0x21 {
		t.Errorf("SET_LINE_CODING bmRequestType = %#02x, want 0x21", set.RequestType())
	}
	if set.Request != ReqSetLineCoding {
		t.Errorf("SET_LINE_CODING = %s", set)
	}

	state := SetControlLineState(2, ControlLineDTR|ControlLineRTS)
	if state.RequestType() != 0x21 {
		t.Errorf("SET_CONTROL_LINE_STATE bmRequestType = %#02x, want 0x21", state.RequestType())
	}
	if state.Request != ReqSetControlLineState || state.Value != 3 || state.Index != 2 {
		t.Errorf("SET_CONTROL_LINE_STATE = %s", state)
	}
}
