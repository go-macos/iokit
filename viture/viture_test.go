package viture

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"testing"
)

// TestCRC16CheckValue is the control for the CRC implementation. 0x29B1 over
// "123456789" is the published check value for CRC-16/CCITT-FALSE, so this test
// distinguishes it from the half-dozen other CRC-16 variants that share the
// 0x1021 polynomial and would otherwise look plausible.
func TestCRC16CheckValue(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x29B1 {
		t.Errorf("CRC16(\"123456789\") = %#04x, want 0x29b1 (CRC-16/CCITT-FALSE)", got)
	}
	// The initial value is 0xFFFF, so the empty input is not zero.
	if got := CRC16(nil); got != 0xFFFF {
		t.Errorf("CRC16(nil) = %#04x, want 0xffff", got)
	}
	// A byte whose top bit forces the polynomial XOR, and one that does not,
	// so both arms of the inner loop are pinned to a value.
	if got := CRC16([]byte{0x00}); got != 0xE1F0 {
		t.Errorf("CRC16(00) = %#04x, want 0xe1f0", got)
	}
}

func TestPacketLayout(t *testing.T) {
	p := EnableIMU(true, 0)
	if len(p) != 20 {
		t.Fatalf("EnableIMU packet is %d bytes, want the documented 20", len(p))
	}
	for _, tc := range []struct {
		name string
		got  uint16
		want uint16
	}{
		{"header", binary.BigEndian.Uint16(p[0:]), HeaderCommand},
		{"length", binary.LittleEndian.Uint16(p[4:]), 12},
		{"command", binary.LittleEndian.Uint16(p[0x0E:]), CmdIMU},
		{"counter", binary.LittleEndian.Uint16(p[0x10:]), 0},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#04x, want %#04x", tc.name, tc.got, tc.want)
		}
	}
	if p[0x12] != 1 {
		t.Errorf("enable byte = %d, want 1", p[0x12])
	}
	if p[19] != EndMarker {
		t.Errorf("end marker = %#02x, want %#02x", p[19], EndMarker)
	}
	// The reserved words must be zero: a device that validates them would drop
	// a packet with anything else in there.
	for i := 6; i < 0x0E; i++ {
		if p[i] != 0 {
			t.Errorf("reserved byte %d = %#02x, want 0", i, p[i])
		}
	}
	// The CRC must cover the packet as built, end marker included.
	if got, want := binary.BigEndian.Uint16(p[2:]), CRC16(p[4:]); got != want {
		t.Errorf("CRC field = %#04x, want %#04x", got, want)
	}
	t.Logf("IMU-enable packet: %s", hex.EncodeToString(p))
}

func TestEnableIMUOff(t *testing.T) {
	if p := EnableIMU(false, 7); p[0x12] != 0 {
		t.Errorf("disable byte = %d, want 0", p[0x12])
	} else if got := binary.LittleEndian.Uint16(p[0x10:]); got != 7 {
		t.Errorf("counter = %d, want 7", got)
	}
}

func TestPacketEmptyPayload(t *testing.T) {
	p := Packet(0x1234, 1, nil)
	if len(p) != MinPacket {
		t.Errorf("empty-payload packet is %d bytes, want %d", len(p), MinPacket)
	}
	if !Valid(p) {
		t.Error("Valid() rejected a packet this package built")
	}
}

func TestValid(t *testing.T) {
	good := EnableIMU(true, 0)
	if !Valid(good) {
		t.Error("Valid(built packet) = false")
	}
	// Too short to hold a header and a CRC.
	if Valid([]byte{0xFF}) {
		t.Error("Valid(1 byte) = true")
	}
	// A header the protocol does not define.
	bad := append([]byte(nil), good...)
	bad[0], bad[1] = 0x12, 0x34
	if Valid(bad) {
		t.Error("Valid(unknown header) = true")
	}
	// A corrupted body must fail the CRC.
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)-2] ^= 0xFF
	if Valid(corrupt) {
		t.Error("Valid(corrupt body) = true")
	}
	// The ack and IMU headers are accepted too, given a matching CRC.
	for _, h := range []uint16{HeaderAck, HeaderIMU} {
		p := append([]byte(nil), good...)
		binary.BigEndian.PutUint16(p[0:], h)
		if !Valid(p) {
			t.Errorf("Valid(header %#04x) = false", h)
		}
	}
}

func TestParseIMU(t *testing.T) {
	p := make([]byte, eulerOffset+12)
	binary.BigEndian.PutUint16(p[0:], HeaderIMU)
	for i, v := range []float32{1.5, -2.25, 90} {
		binary.BigEndian.PutUint32(p[eulerOffset+4*i:], math.Float32bits(v))
	}
	e, err := ParseIMU(p)
	if err != nil {
		t.Fatalf("ParseIMU = %v", err)
	}
	if e.Z != 1.5 || e.X != -2.25 || e.Y != 90 {
		t.Errorf("ParseIMU = %+v, want {1.5 -2.25 90}", e)
	}
}

func TestParseIMURejects(t *testing.T) {
	// Too short for three angles at offset 18.
	if _, err := ParseIMU(make([]byte, eulerOffset+11)); !errors.Is(err, ErrNotIMU) {
		t.Errorf("ParseIMU(short) = %v, want ErrNotIMU", err)
	}
	// Right length, wrong header: a command echoed back is not a report.
	p := make([]byte, eulerOffset+12)
	binary.BigEndian.PutUint16(p[0:], HeaderCommand)
	if _, err := ParseIMU(p); !errors.Is(err, ErrNotIMU) {
		t.Errorf("ParseIMU(command header) = %v, want ErrNotIMU", err)
	}
}
