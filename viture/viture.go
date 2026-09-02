// Package viture builds and parses the classic VITURE XR glasses MCU protocol.
//
// The protocol is not published by the manufacturer. What is encoded here comes
// from the community reverse-engineering write-up in
// bfvogel/viture-webxr-extension (VITURE_PROTOCOL.md), and it is confirmed to
// work only up to the VITURE Pro (product 0x101D).
//
// Newer generations do NOT speak it. A Beast (0x1201) accepts these packets on
// every one of its three HID interfaces -- including padded to the full 64-byte
// report -- and answers none of them. That silence was once recorded here as
// "the glasses answer nothing at all"; it is now known to be the wrong
// conclusion. They answer readily, in a DIFFERENT frame, which beast.go
// describes and which was read off the wire rather than guessed.
//
// Everything here is pure byte manipulation with no I/O, so which transport
// carries a packet -- a HID report, a USB control transfer, a CDC-ACM serial
// port -- is the caller's choice. That separation is the point: the transport
// is exactly the variable a probe needs to vary.
package viture

import (
	"encoding/binary"
	"errors"
	"math"
)

// Packet headers. The first two bytes say who is speaking and about what.
const (
	// HeaderCommand marks a packet the host sends to the glasses' MCU.
	HeaderCommand uint16 = 0xFFFE
	// HeaderAck marks the MCU's acknowledgement of a command.
	HeaderAck uint16 = 0xFFFD
	// HeaderIMU marks an unsolicited orientation report from the MCU.
	HeaderIMU uint16 = 0xFFFC
)

// Command IDs. Only the ones a probe needs are named.
const (
	// CmdIMU turns the orientation stream on or off. Payload is a single byte:
	// 1 to enable, 0 to disable.
	CmdIMU uint16 = 0x15
)

// EndMarker terminates every command packet.
const EndMarker byte = 0x03

// Structural constants of a command packet.
const (
	// HeaderSize is the number of bytes before the payload: header, CRC,
	// length, two reserved words, command ID and message counter.
	HeaderSize = 0x12
	// lengthBase is subtracted from the total packet size to give the value of
	// the length field. The field counts the bytes from offset 8 to the end,
	// which for the canonical 20-byte IMU-enable packet makes it 12.
	lengthBase = 8
	// MinPacket is the smallest well-formed command packet: header through end
	// marker with an empty payload.
	MinPacket = HeaderSize + 1
)

// CRC16 computes CRC-16/CCITT-FALSE over b: polynomial 0x1021, initial value
// 0xFFFF, no input or output reflection, no final XOR.
//
// The canonical check value for this parameterisation is CRC16("123456789") ==
// 0x29B1, which the package's test asserts. Getting the variant wrong is the
// classic way to build a packet the device silently drops, so the check value
// is not decoration.
func CRC16(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, c := range b {
		crc ^= uint16(c) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Packet builds a command packet for cmd with the given message counter and
// payload.
//
// Layout, all multi-byte fields little-endian except the CRC, which is big:
//
//	0x00  2  header 0xFF 0xFE
//	0x02  2  CRC-16/CCITT-FALSE over everything from 0x04 to the end
//	0x04  2  length: total size minus 8
//	0x06  4  reserved, zero
//	0x0A  4  reserved, zero
//	0x0E  2  command ID
//	0x10  2  message counter
//	0x12  N  payload
//	       1  end marker 0x03
//
// The CRC is written last, over the finished packet, so it covers the length
// and the end marker as well as the payload.
func Packet(cmd, counter uint16, payload []byte) []byte {
	p := make([]byte, HeaderSize+len(payload)+1)
	binary.BigEndian.PutUint16(p[0:], HeaderCommand)
	binary.LittleEndian.PutUint16(p[4:], uint16(len(p)-lengthBase))
	binary.LittleEndian.PutUint16(p[0x0E:], cmd)
	binary.LittleEndian.PutUint16(p[0x10:], counter)
	copy(p[HeaderSize:], payload)
	p[len(p)-1] = EndMarker
	binary.BigEndian.PutUint16(p[2:], CRC16(p[4:]))
	return p
}

// EnableIMU builds the packet that starts (or stops) the orientation stream.
// counter is the caller's message counter, which the MCU echoes in its
// acknowledgement.
func EnableIMU(on bool, counter uint16) []byte {
	v := byte(0)
	if on {
		v = 1
	}
	return Packet(CmdIMU, counter, []byte{v})
}

// Valid reports whether p is a well-formed packet of any kind: long enough,
// carrying a known header, and matching its own CRC.
func Valid(p []byte) bool {
	if len(p) < 4 {
		return false
	}
	switch binary.BigEndian.Uint16(p[0:]) {
	case HeaderCommand, HeaderAck, HeaderIMU:
	default:
		return false
	}
	return binary.BigEndian.Uint16(p[2:]) == CRC16(p[4:])
}

// ErrNotIMU is returned by [ParseIMU] for bytes that are not an IMU report.
var ErrNotIMU = errors.New("viture: not an IMU report")

// eulerOffset is where the three angles begin. The report's first 18 bytes are
// the same framing every packet carries.
const eulerOffset = 18

// Euler is one orientation report.
//
// The field names follow the order the bytes arrive in -- Z, then X, then Y --
// rather than roll/pitch/yaw, deliberately. The mapping from these three axes
// to head motion was established on a VITURE Pro and has never been confirmed
// on a Beast, so naming them "roll" and "pitch" here would assert something
// this package has not measured. Callers that verify the mapping on their own
// hardware should name them at that point.
type Euler struct {
	Z, X, Y float32
}

// ParseIMU decodes an orientation report. The three angles are big-endian
// float32 starting at offset 18 -- big-endian, unlike every other multi-byte
// field in the protocol, which is the kind of detail that costs an afternoon.
func ParseIMU(p []byte) (Euler, error) {
	if len(p) < eulerOffset+12 {
		return Euler{}, ErrNotIMU
	}
	if binary.BigEndian.Uint16(p[0:]) != HeaderIMU {
		return Euler{}, ErrNotIMU
	}
	f := func(off int) float32 {
		return math.Float32frombits(binary.BigEndian.Uint32(p[off:]))
	}
	return Euler{Z: f(eulerOffset), X: f(eulerOffset + 4), Y: f(eulerOffset + 8)}, nil
}
