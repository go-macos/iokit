package serial

// This file is the portable half of the termios layer: the struct layout, the
// flag constants and the pure function that turns a [Config] into the words the
// kernel wants. None of it makes a system call, so it compiles and is tested on
// every platform, and the darwin file below it does nothing but copy fields and
// call ioctl.
//
// The constants are macOS/BSD values. They are not transcribed from memory:
// serial_darwin_test.go asserts every one of them against the corresponding
// golang.org/x/sys/unix constant, and asserts that [Termios] has the same size
// and field offsets as unix.Termios. A wrong bit here is a port that opens,
// accepts writes and never speaks -- exactly the failure mode this module keeps
// running into -- so the cross-check is not decoration.

// NCCS is the number of control characters in a BSD termios.
const NCCS = 20

// Indices into [Termios.Cc].
const (
	VMIN  = 16
	VTIME = 17
)

// Input flags, termios c_iflag.
const (
	IGNBRK = 0x00000001
	BRKINT = 0x00000002
	IGNPAR = 0x00000004
	PARMRK = 0x00000008
	INPCK  = 0x00000010
	ISTRIP = 0x00000020
	INLCR  = 0x00000040
	IGNCR  = 0x00000080
	ICRNL  = 0x00000100
	IXON   = 0x00000200
	IXOFF  = 0x00000400
	IXANY  = 0x00000800
)

// Output flags, termios c_oflag.
const (
	OPOST = 0x00000001
	ONLCR = 0x00000002
)

// Control flags, termios c_cflag.
const (
	CSIZE  = 0x00000300
	CS5    = 0x00000000
	CS6    = 0x00000100
	CS7    = 0x00000200
	CS8    = 0x00000300
	CSTOPB = 0x00000400
	CREAD  = 0x00000800
	PARENB = 0x00001000
	PARODD = 0x00002000
	HUPCL  = 0x00004000
	CLOCAL = 0x00008000
	// CRTSCTS is CCTS_OFLOW|CRTS_IFLOW: BSD splits hardware flow control into
	// the two directions and defines the joint name as their union.
	CCTSOFLOW = 0x00010000
	CRTSIFLOW = 0x00020000
	CRTSCTS   = CCTSOFLOW | CRTSIFLOW
)

// Local flags, termios c_lflag.
const (
	ECHOE  = 0x00000002
	ECHOK  = 0x00000004
	ECHO   = 0x00000008
	ECHONL = 0x00000010
	ISIG   = 0x00000080
	ICANON = 0x00000100
	IEXTEN = 0x00000400
)

// Modem control line bits, as reported by TIOCMGET. They are typed [Lines] so
// that a mask built from them cannot be passed where a flag word is meant.
const (
	LineDTR Lines = 0x0002
	LineRTS Lines = 0x0004
	LineCTS Lines = 0x0020
	LineDCD Lines = 0x0040 // TIOCM_CAR
	LineRI  Lines = 0x0080
	LineDSR Lines = 0x0100
)

// Termios mirrors the macOS struct termios field for field, so a value can be
// copied into the x/sys/unix type without reinterpreting memory.
type Termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [NCCS]uint8
	Ispeed uint64
	Ospeed uint64
}

// Lines is a set of modem control line bits: [LineDTR], [LineDCD] and friends.
type Lines uint32

// Has reports whether every bit in mask is set.
func (l Lines) Has(mask Lines) bool { return l&mask == mask }

// String renders the asserted lines in a fixed order, so two readings can be
// compared by eye. An empty set prints "none".
func (l Lines) String() string {
	names := []struct {
		bit  Lines
		name string
	}{
		{LineDTR, "DTR"}, {LineRTS, "RTS"}, {LineCTS, "CTS"},
		{LineDCD, "DCD"}, {LineRI, "RI"}, {LineDSR, "DSR"},
	}
	out := ""
	for _, n := range names {
		if l&n.bit != 0 {
			if out != "" {
				out += "|"
			}
			out += n.name
		}
	}
	if out == "" {
		return "none"
	}
	return out
}

// Parity selects the parity bit scheme.
type Parity int

// The parity schemes a CDC-ACM device can be asked for.
const (
	ParityNone Parity = iota
	ParityOdd
	ParityEven
)

// String names the scheme.
func (p Parity) String() string {
	switch p {
	case ParityNone:
		return "none"
	case ParityOdd:
		return "odd"
	case ParityEven:
		return "even"
	default:
		return "invalid"
	}
}

// Config is a line configuration. The zero value is not usable: [Open] rejects
// a zero baud rate rather than quietly picking one, because "the port was
// configured for 0 baud" is precisely the kind of silent misconfiguration that
// looks like a mute device.
type Config struct {
	// Baud is the line rate. Any positive value is accepted; macOS takes
	// non-standard rates through IOSSIOSPEED after the termios call.
	Baud int
	// DataBits is 5, 6, 7 or 8. Zero means 8.
	DataBits int
	// StopBits is 1 or 2. Zero means 1.
	StopBits int
	// Parity defaults to none.
	Parity Parity
	// CLOCAL makes the driver ignore the carrier detect line. Without it a
	// read on a /dev/tty.* node blocks until DCD is asserted, which on a
	// USB CDC device that never raises DCD is forever.
	CLOCAL bool
	// HUPCL drops DTR on the last close.
	HUPCL bool
	// RTSCTS turns on hardware flow control. A device that does not drive CTS
	// will never be written to once this is set.
	RTSCTS bool
	// ReadMin is VMIN: the smallest number of bytes a blocking read returns.
	ReadMin uint8
	// ReadTimeoutDecis is VTIME, in tenths of a second.
	ReadTimeoutDecis uint8
}

// ErrBadConfig reports a configuration the kernel would reject or silently
// mangle.
type ErrBadConfig struct {
	Field  string
	Value  int
	Reason string
}

// Error renders the offending field.
func (e *ErrBadConfig) Error() string {
	return "serial: invalid " + e.Field + ": " + itoa(e.Value) + " (" + e.Reason + ")"
}

// itoa avoids pulling strconv into a file that is otherwise pure bit twiddling.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Validate reports whether the configuration is one the kernel can honour.
func (c Config) Validate() error {
	if c.Baud <= 0 {
		return &ErrBadConfig{"baud", c.Baud, "must be positive"}
	}
	switch c.DataBits {
	case 0, 5, 6, 7, 8:
	default:
		return &ErrBadConfig{"data bits", c.DataBits, "must be 5, 6, 7 or 8"}
	}
	switch c.StopBits {
	case 0, 1, 2:
	default:
		return &ErrBadConfig{"stop bits", c.StopBits, "must be 1 or 2"}
	}
	switch c.Parity {
	case ParityNone, ParityOdd, ParityEven:
	default:
		return &ErrBadConfig{"parity", int(c.Parity), "must be none, odd or even"}
	}
	return nil
}

// Termios renders the configuration as a raw-mode termios: no canonical line
// editing, no echo, no signal generation, no input or output translation. What
// arrives is what the device sent.
//
// It applies the configuration to a copy of cur rather than building from zero,
// so the bits macOS set at open that this package has no opinion about survive.
func (c Config) Termios(cur Termios) Termios {
	t := cur

	// cfmakeraw, spelled out.
	t.Iflag &^= IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON
	t.Oflag &^= OPOST
	t.Lflag &^= ECHO | ECHOE | ECHOK | ECHONL | ICANON | ISIG | IEXTEN

	t.Cflag |= CREAD
	t.Cflag &^= CSIZE
	switch c.DataBits {
	case 5:
		t.Cflag |= CS5
	case 6:
		t.Cflag |= CS6
	case 7:
		t.Cflag |= CS7
	default:
		t.Cflag |= CS8
	}

	if c.StopBits == 2 {
		t.Cflag |= CSTOPB
	} else {
		t.Cflag &^= CSTOPB
	}

	t.Cflag &^= PARENB | PARODD
	switch c.Parity {
	case ParityOdd:
		t.Cflag |= PARENB | PARODD
	case ParityEven:
		t.Cflag |= PARENB
	}

	set := func(on bool, bits uint64) {
		if on {
			t.Cflag |= bits
		} else {
			t.Cflag &^= bits
		}
	}
	set(c.CLOCAL, CLOCAL)
	set(c.HUPCL, HUPCL)
	set(c.RTSCTS, CRTSCTS)

	t.Cc[VMIN] = c.ReadMin
	t.Cc[VTIME] = c.ReadTimeoutDecis

	// BSD keeps the actual rate in the speed fields; there is no B-code table
	// to look a value up in, which is why a non-standard rate only needs the
	// extra IOSSIOSPEED call when the driver refuses it here.
	t.Ispeed = uint64(c.Baud)
	t.Ospeed = uint64(c.Baud)
	return t
}

// ConfigOf reads a termios back as a [Config]. It is the inverse of
// [Config.Termios] for the fields this package sets, and it exists so a probe
// can print what the kernel actually accepted rather than what it was asked
// for.
func ConfigOf(t Termios) Config {
	c := Config{
		Baud:             int(t.Ospeed),
		CLOCAL:           t.Cflag&CLOCAL != 0,
		HUPCL:            t.Cflag&HUPCL != 0,
		RTSCTS:           t.Cflag&CRTSCTS != 0,
		ReadMin:          t.Cc[VMIN],
		ReadTimeoutDecis: t.Cc[VTIME],
	}
	switch t.Cflag & CSIZE {
	case CS5:
		c.DataBits = 5
	case CS6:
		c.DataBits = 6
	case CS7:
		c.DataBits = 7
	default:
		c.DataBits = 8
	}
	c.StopBits = 1
	if t.Cflag&CSTOPB != 0 {
		c.StopBits = 2
	}
	switch {
	case t.Cflag&PARENB == 0:
		c.Parity = ParityNone
	case t.Cflag&PARODD != 0:
		c.Parity = ParityOdd
	default:
		c.Parity = ParityEven
	}
	return c
}

// String renders a configuration the way a terminal program would: rate, then
// the classic 8N1 shorthand, then the flags that are on.
func (c Config) String() string {
	s := itoa(c.Baud) + " " + itoa(c.DataBits)
	switch c.Parity {
	case ParityOdd:
		s += "O"
	case ParityEven:
		s += "E"
	default:
		s += "N"
	}
	s += itoa(c.StopBits)
	for _, f := range []struct {
		on   bool
		name string
	}{{c.CLOCAL, "clocal"}, {c.HUPCL, "hupcl"}, {c.RTSCTS, "rtscts"}} {
		if f.on {
			s += " " + f.name
		}
	}
	return s + " vmin=" + itoa(int(c.ReadMin)) + " vtime=" + itoa(int(c.ReadTimeoutDecis))
}
