package usb

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The CDC ACM class requests worth naming. They are the only requests the
// VITURE Beast was ever observed to answer, so they are also the only lever
// this package has on that device's serial function.
const (
	// ReqSetLineCoding sets the rate, stop bits, parity and data bits. It is
	// host-to-device with a 7-byte payload, recipient interface.
	ReqSetLineCoding byte = 0x20
	// ReqGetLineCoding reads the same 7 bytes back.
	ReqGetLineCoding byte = 0x21
	// ReqSetControlLineState carries DTR and RTS in wValue. On a USB CDC
	// device there is no wire to raise: this request *is* the modem line.
	ReqSetControlLineState byte = 0x22
	// ReqSendBreak asserts a break condition for wValue milliseconds.
	ReqSendBreak byte = 0x23
)

// The wValue bits of [ReqSetControlLineState].
const (
	ControlLineDTR uint16 = 1 << 0
	ControlLineRTS uint16 = 1 << 1
)

// LineCodingSize is the wire size of a CDC line coding structure.
const LineCodingSize = 7

// Stop bit encodings, CDC bCharFormat.
const (
	Stop1   byte = 0
	Stop1p5 byte = 1
	Stop2   byte = 2
)

// Parity encodings, CDC bParityType.
const (
	ParityNone  byte = 0
	ParityOdd   byte = 1
	ParityEven  byte = 2
	ParityMark  byte = 3
	ParitySpace byte = 4
)

// LineCoding is the CDC ACM line configuration: the seven bytes
// GET_LINE_CODING returns and SET_LINE_CODING takes.
//
// Reading it back is the one way to ask a CDC device what it thinks it is
// configured for, independently of what the host driver believes. A device
// that answers with the rate the host just set has a live control endpoint
// behind its serial function; one that answers with seven zero bytes after
// every setting has a stub, and its /dev node is decoration.
type LineCoding struct {
	// Rate is dwDTERate, bits per second.
	Rate uint32
	// StopBits is bCharFormat: [Stop1], [Stop1p5] or [Stop2].
	StopBits byte
	// Parity is bParityType.
	Parity byte
	// DataBits is bDataBits: 5, 6, 7, 8 or 16.
	DataBits byte
}

// ErrShortLineCoding reports a reply too short to be a line coding structure.
var ErrShortLineCoding = errors.New("usb: line coding needs 7 bytes")

// ParseLineCoding decodes the seven wire bytes.
func ParseLineCoding(b []byte) (LineCoding, error) {
	if len(b) < LineCodingSize {
		return LineCoding{}, ErrShortLineCoding
	}
	return LineCoding{
		Rate:     binary.LittleEndian.Uint32(b),
		StopBits: b[4],
		Parity:   b[5],
		DataBits: b[6],
	}, nil
}

// Bytes encodes the structure for SET_LINE_CODING.
func (lc LineCoding) Bytes() []byte {
	b := make([]byte, LineCodingSize)
	binary.LittleEndian.PutUint32(b, lc.Rate)
	b[4], b[5], b[6] = lc.StopBits, lc.Parity, lc.DataBits
	return b
}

// Zero reports whether every field is zero, which is what an unimplemented
// GET_LINE_CODING returns: the device ACKed the request and had nothing to say.
func (lc LineCoding) Zero() bool { return lc == LineCoding{} }

// String renders the coding in the usual 115200 8N1 shorthand.
func (lc LineCoding) String() string {
	parity := map[byte]string{ParityNone: "N", ParityOdd: "O", ParityEven: "E", ParityMark: "M", ParitySpace: "S"}
	p, ok := parity[lc.Parity]
	if !ok {
		p = fmt.Sprintf("?%d", lc.Parity)
	}
	stop := map[byte]string{Stop1: "1", Stop1p5: "1.5", Stop2: "2"}
	s, ok := stop[lc.StopBits]
	if !ok {
		s = fmt.Sprintf("?%d", lc.StopBits)
	}
	return fmt.Sprintf("%d %d%s%s", lc.Rate, lc.DataBits, p, s)
}

// GetLineCoding builds the setup packet that reads a CDC interface's line
// coding. iface is the communications interface number.
func GetLineCoding(iface uint16) Setup {
	return Setup{Direction: In, Type: Class, Recipient: ToInterface, Request: ReqGetLineCoding, Index: iface}
}

// SetLineCoding builds the setup packet that writes one. The seven payload
// bytes come from [LineCoding.Bytes].
func SetLineCoding(iface uint16) Setup {
	return Setup{Direction: Out, Type: Class, Recipient: ToInterface, Request: ReqSetLineCoding, Index: iface}
}

// SetControlLineState builds the setup packet that raises or drops DTR and
// RTS. lines is a combination of [ControlLineDTR] and [ControlLineRTS].
func SetControlLineState(iface, lines uint16) Setup {
	return Setup{Direction: Out, Type: Class, Recipient: ToInterface, Request: ReqSetControlLineState, Value: lines, Index: iface}
}
