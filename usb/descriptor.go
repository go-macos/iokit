package usb

import (
	"errors"
	"fmt"
	"strings"
)

// Descriptor types, from the USB specification. Only the ones this package
// decodes are named.
const (
	DescConfigurationDesc    byte = 0x02
	DescInterface            byte = 0x04
	DescEndpoint             byte = 0x05
	DescInterfaceAssociation byte = 0x0B
)

// USB device and interface class codes worth naming in a probe's output. A
// class this list does not name is printed as a number, which is honest: the
// list is a convenience, not a specification.
const (
	ClassAudio         uint8 = 0x01
	ClassCDCControl    uint8 = 0x02
	ClassHID           uint8 = 0x03
	ClassCDCData       uint8 = 0x0A
	ClassVideo         uint8 = 0x0E
	ClassMiscellaneous uint8 = 0xEF
	ClassVendor        uint8 = 0xFF
)

// className names a class code for a listing.
func className(c uint8) string {
	switch c {
	case ClassAudio:
		return "audio"
	case ClassCDCControl:
		return "CDC-control"
	case ClassHID:
		return "HID"
	case ClassCDCData:
		return "CDC-data"
	case ClassVideo:
		return "video"
	case ClassMiscellaneous:
		return "misc"
	case ClassVendor:
		return "vendor-specific"
	}
	return fmt.Sprintf("class %#02x", c)
}

// Errors from descriptor parsing.
var (
	// ErrBadDescriptor is returned when a descriptor block is malformed: a
	// zero or over-long bLength, or a truncated tail. A device that publishes
	// one is broken, but the parser must say so rather than loop forever.
	ErrBadDescriptor = errors.New("usb: malformed descriptor block")
)

// Raw is one descriptor as the device published it, header included.
type Raw struct {
	// Type is bDescriptorType.
	Type byte
	// Bytes is the whole descriptor, starting with bLength.
	Bytes []byte
}

// Split walks a descriptor block -- a configuration descriptor followed by
// everything that belongs to it -- and returns each descriptor in order,
// including the class-specific ones this package does not interpret.
//
// Walking by bLength rather than by expected type is what makes this work on
// real hardware: a device is free to interleave functional descriptors nobody
// documented, and a parser that assumes it knows the sequence gets lost on the
// first one.
func Split(b []byte) ([]Raw, error) {
	var out []Raw
	for i := 0; i < len(b); {
		if len(b)-i < 2 {
			return out, fmt.Errorf("%w: %d trailing byte(s) at offset %d", ErrBadDescriptor, len(b)-i, i)
		}
		n := int(b[i])
		if n < 2 || i+n > len(b) {
			return out, fmt.Errorf("%w: bLength %d at offset %d", ErrBadDescriptor, n, i)
		}
		out = append(out, Raw{Type: b[i+1], Bytes: b[i : i+n]})
		i += n
	}
	return out, nil
}

// TransferType is an endpoint's transfer type, bits 1..0 of bmAttributes.
type TransferType uint8

// The four USB transfer types.
const (
	TransferControl     TransferType = 0
	TransferIsochronous TransferType = 1
	TransferBulk        TransferType = 2
	TransferInterrupt   TransferType = 3
)

// String names the transfer type.
func (t TransferType) String() string {
	switch t {
	case TransferControl:
		return "control"
	case TransferIsochronous:
		return "isochronous"
	case TransferBulk:
		return "bulk"
	case TransferInterrupt:
		return "interrupt"
	}
	return fmt.Sprintf("TransferType(%d)", uint8(t))
}

// Endpoint describes one endpoint of an interface.
type Endpoint struct {
	// Address is bEndpointAddress: the endpoint number in bits 3..0 and the
	// direction in bit 7.
	Address byte
	// Attributes is bmAttributes.
	Attributes byte
	// MaxPacketSize is wMaxPacketSize.
	MaxPacketSize uint16
	// Interval is bInterval, the polling period for interrupt and isochronous
	// endpoints.
	Interval byte
}

// Number returns the endpoint number, without the direction bit.
func (e Endpoint) Number() int { return int(e.Address & 0x0F) }

// Direction returns which way the endpoint moves data.
func (e Endpoint) Direction() Direction {
	if e.Address&0x80 != 0 {
		return In
	}
	return Out
}

// TransferType returns the endpoint's transfer type.
func (e Endpoint) TransferType() TransferType { return TransferType(e.Attributes & 0x03) }

// String renders the endpoint the way a probe listing reads.
func (e Endpoint) String() string {
	return fmt.Sprintf("ep %#02x %s %s max=%d interval=%d",
		e.Address, e.Direction(), e.TransferType(), e.MaxPacketSize, e.Interval)
}

// Interface describes one alternate setting of one interface.
type Interface struct {
	Number    uint8
	Alternate uint8
	Class     uint8
	SubClass  uint8
	Protocol  uint8
	Endpoints []Endpoint
}

// String renders the interface the way a probe listing reads.
func (i Interface) String() string {
	return fmt.Sprintf("interface %d alt %d %s (%d/%d/%d) %d endpoint(s)",
		i.Number, i.Alternate, className(i.Class), i.Class, i.SubClass, i.Protocol, len(i.Endpoints))
}

// Association is an Interface Association Descriptor: it groups consecutive
// interfaces into one function, which is how a composite device says "these
// two interfaces together are a serial port".
type Association struct {
	FirstInterface uint8
	InterfaceCount uint8
	Class          uint8
	SubClass       uint8
	Protocol       uint8
}

// String renders the association the way a probe listing reads.
func (a Association) String() string {
	return fmt.Sprintf("function: interfaces %d..%d are one %s (%d/%d/%d)",
		a.FirstInterface, a.FirstInterface+a.InterfaceCount-1,
		className(a.Class), a.Class, a.SubClass, a.Protocol)
}

// Config is a decoded configuration descriptor block.
type Config struct {
	// Value is bConfigurationValue, the number [Device] would select.
	Value uint8
	// Attributes is bmAttributes; MaxPower is bMaxPower in 2 mA units.
	Attributes uint8
	MaxPower   uint8
	// Associations and Interfaces are in the order the device published them.
	Associations []Association
	Interfaces   []Interface
	// Unknown counts descriptors that were walked but not interpreted, which
	// are almost always class-specific functional descriptors.
	Unknown int
}

// String renders the whole configuration as an indented block.
func (c Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "configuration %d: %d mA, %d interface(s), %d uninterpreted descriptor(s)",
		c.Value, int(c.MaxPower)*2, len(c.Interfaces), c.Unknown)
	for _, a := range c.Associations {
		fmt.Fprintf(&b, "\n  %s", a)
	}
	for _, i := range c.Interfaces {
		fmt.Fprintf(&b, "\n  %s", i)
		for _, e := range i.Endpoints {
			fmt.Fprintf(&b, "\n    %s", e)
		}
	}
	return b.String()
}

// minimum descriptor lengths, so a truncated one is rejected rather than read
// past its end.
const (
	minConfigDesc      = 9
	minInterfaceDesc   = 9
	minEndpointDesc    = 7
	minAssociationDesc = 8
)

// ParseConfig decodes a configuration descriptor block: the configuration
// descriptor itself plus every interface, endpoint and association that follows
// it.
//
// Endpoints attach to the interface most recently seen, which is how the USB
// specification orders a configuration block. Descriptors of other types are
// counted in [Config.Unknown] and skipped, so a class-specific functional
// descriptor never derails the walk.
func ParseConfig(b []byte) (Config, error) {
	var c Config
	if len(b) < minConfigDesc {
		return c, fmt.Errorf("%w: configuration descriptor is %d bytes, want at least %d",
			ErrBadDescriptor, len(b), minConfigDesc)
	}
	c.Value = b[5]
	c.Attributes = b[7]
	c.MaxPower = b[8]

	raws, err := Split(b)
	if err != nil {
		return c, err
	}
	for _, r := range raws {
		switch {
		case r.Type == DescConfigurationDesc:
			// The block's own header, already decoded above.
		case r.Type == DescInterface && len(r.Bytes) >= minInterfaceDesc:
			c.Interfaces = append(c.Interfaces, Interface{
				Number:    r.Bytes[2],
				Alternate: r.Bytes[3],
				Class:     r.Bytes[5],
				SubClass:  r.Bytes[6],
				Protocol:  r.Bytes[7],
			})
		case r.Type == DescEndpoint && len(r.Bytes) >= minEndpointDesc:
			if len(c.Interfaces) == 0 {
				// An endpoint before any interface is malformed; count it
				// rather than drop it silently.
				c.Unknown++
				continue
			}
			cur := &c.Interfaces[len(c.Interfaces)-1]
			cur.Endpoints = append(cur.Endpoints, Endpoint{
				Address:       r.Bytes[2],
				Attributes:    r.Bytes[3],
				MaxPacketSize: uint16(r.Bytes[4]) | uint16(r.Bytes[5])<<8,
				Interval:      r.Bytes[6],
			})
		case r.Type == DescInterfaceAssociation && len(r.Bytes) >= minAssociationDesc:
			c.Associations = append(c.Associations, Association{
				FirstInterface: r.Bytes[2],
				InterfaceCount: r.Bytes[3],
				Class:          r.Bytes[4],
				SubClass:       r.Bytes[5],
				Protocol:       r.Bytes[6],
			})
		default:
			c.Unknown++
		}
	}
	return c, nil
}
