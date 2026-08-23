package serial

import (
	"errors"
	"testing"
)

func TestParityString(t *testing.T) {
	for _, tc := range []struct {
		p    Parity
		want string
	}{{ParityNone, "none"}, {ParityOdd, "odd"}, {ParityEven, "even"}, {Parity(9), "invalid"}} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("Parity(%d).String() = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestLinesString(t *testing.T) {
	for _, tc := range []struct {
		l    Lines
		want string
	}{
		{0, "none"},
		{LineDTR, "DTR"},
		{LineDTR | LineRTS, "DTR|RTS"},
		{LineCTS | LineDCD | LineRI | LineDSR, "CTS|DCD|RI|DSR"},
		{LineDTR | LineRTS | LineCTS | LineDCD | LineRI | LineDSR, "DTR|RTS|CTS|DCD|RI|DSR"},
	} {
		if got := tc.l.String(); got != tc.want {
			t.Errorf("Lines(%#x).String() = %q, want %q", uint32(tc.l), got, tc.want)
		}
	}
}

func TestLinesHas(t *testing.T) {
	l := LineDTR | LineRTS
	if !l.Has(LineDTR) || !l.Has(LineDTR|LineRTS) {
		t.Error("Has should accept bits that are set")
	}
	if l.Has(LineDCD) || l.Has(LineDTR|LineDCD) {
		t.Error("Has should reject a mask that is not wholly set")
	}
}

func TestItoa(t *testing.T) {
	for _, tc := range []struct {
		v    int
		want string
	}{{0, "0"}, {1, "1"}, {9, "9"}, {10, "10"}, {115200, "115200"}, {-42, "-42"}} {
		if got := itoa(tc.v); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	ok := Config{Baud: 9600}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a plain 9600 config should be valid: %v", err)
	}
	for _, c := range []Config{{Baud: 5, DataBits: 8}, {Baud: 9600, DataBits: 5}, {Baud: 9600, DataBits: 6}, {Baud: 9600, DataBits: 7}, {Baud: 9600, StopBits: 2}, {Baud: 9600, Parity: ParityOdd}, {Baud: 9600, Parity: ParityEven}} {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", c, err)
		}
	}
	for _, tc := range []struct {
		c     Config
		field string
	}{
		{Config{Baud: 0}, "baud"},
		{Config{Baud: -1}, "baud"},
		{Config{Baud: 9600, DataBits: 9}, "data bits"},
		{Config{Baud: 9600, StopBits: 3}, "stop bits"},
		{Config{Baud: 9600, Parity: Parity(7)}, "parity"},
	} {
		err := tc.c.Validate()
		var bad *ErrBadConfig
		if !errors.As(err, &bad) {
			t.Fatalf("%+v: want *ErrBadConfig, got %v", tc.c, err)
		}
		if bad.Field != tc.field {
			t.Errorf("%+v: field = %q, want %q", tc.c, bad.Field, tc.field)
		}
		if bad.Error() == "" {
			t.Error("ErrBadConfig.Error must say something")
		}
	}
}

func TestConfigTermiosRawMode(t *testing.T) {
	// Start from a termios with every flag this package clears already set, so
	// a missing &^= shows up as a failure rather than as a coincidence.
	cur := Termios{
		Iflag: IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON | IXANY,
		Oflag: OPOST | ONLCR,
		Lflag: ECHO | ECHOE | ECHOK | ECHONL | ICANON | ISIG | IEXTEN,
		Cflag: CSIZE | CSTOPB | PARENB | PARODD | CLOCAL | HUPCL | CRTSCTS,
	}
	got := Config{Baud: 115200, ReadMin: 1}.Termios(cur)

	for name, bit := range map[string]uint64{
		"IGNBRK": IGNBRK, "BRKINT": BRKINT, "PARMRK": PARMRK, "ISTRIP": ISTRIP,
		"INLCR": INLCR, "IGNCR": IGNCR, "ICRNL": ICRNL, "IXON": IXON,
	} {
		if got.Iflag&bit != 0 {
			t.Errorf("raw mode left %s set in c_iflag", name)
		}
	}
	if got.Iflag&IXANY == 0 {
		t.Error("raw mode cleared IXANY, which it has no opinion about")
	}
	if got.Oflag&OPOST != 0 {
		t.Error("raw mode left OPOST set")
	}
	if got.Oflag&ONLCR == 0 {
		t.Error("raw mode cleared ONLCR, which it has no opinion about")
	}
	for name, bit := range map[string]uint64{
		"ECHO": ECHO, "ECHOE": ECHOE, "ECHOK": ECHOK, "ECHONL": ECHONL,
		"ICANON": ICANON, "ISIG": ISIG, "IEXTEN": IEXTEN,
	} {
		if got.Lflag&bit != 0 {
			t.Errorf("raw mode left %s set in c_lflag", name)
		}
	}
	if got.Cflag&CREAD == 0 {
		t.Error("CREAD must be set or the port never receives")
	}
	if got.Cflag&CSIZE != CS8 {
		t.Errorf("default data bits = %#x, want CS8", got.Cflag&CSIZE)
	}
	for name, bit := range map[string]uint64{
		"CSTOPB": CSTOPB, "PARENB": PARENB, "PARODD": PARODD,
		"CLOCAL": CLOCAL, "HUPCL": HUPCL, "CRTSCTS": CRTSCTS,
	} {
		if got.Cflag&bit != 0 {
			t.Errorf("%s survived a config that does not ask for it", name)
		}
	}
	if got.Ispeed != 115200 || got.Ospeed != 115200 {
		t.Errorf("speeds = %d/%d, want 115200 both", got.Ispeed, got.Ospeed)
	}
	if got.Cc[VMIN] != 1 || got.Cc[VTIME] != 0 {
		t.Errorf("VMIN/VTIME = %d/%d, want 1/0", got.Cc[VMIN], got.Cc[VTIME])
	}
}

func TestConfigTermiosDataBits(t *testing.T) {
	for _, tc := range []struct {
		bits int
		want uint64
	}{{0, CS8}, {5, CS5}, {6, CS6}, {7, CS7}, {8, CS8}} {
		got := Config{Baud: 9600, DataBits: tc.bits}.Termios(Termios{})
		if got.Cflag&CSIZE != tc.want {
			t.Errorf("%d data bits gave %#x, want %#x", tc.bits, got.Cflag&CSIZE, tc.want)
		}
	}
}

func TestConfigTermiosFraming(t *testing.T) {
	if got := (Config{Baud: 9600, StopBits: 2}).Termios(Termios{}); got.Cflag&CSTOPB == 0 {
		t.Error("two stop bits should set CSTOPB")
	}
	for _, tc := range []struct {
		p          Parity
		enb, odd   bool
		wantParity string
	}{
		{ParityNone, false, false, "none"},
		{ParityOdd, true, true, "odd"},
		{ParityEven, true, false, "even"},
	} {
		got := Config{Baud: 9600, Parity: tc.p}.Termios(Termios{})
		if (got.Cflag&PARENB != 0) != tc.enb {
			t.Errorf("%s parity: PARENB wrong", tc.wantParity)
		}
		if (got.Cflag&PARODD != 0) != tc.odd {
			t.Errorf("%s parity: PARODD wrong", tc.wantParity)
		}
	}
}

func TestConfigTermiosSwitches(t *testing.T) {
	on := Config{Baud: 9600, CLOCAL: true, HUPCL: true, RTSCTS: true}.Termios(Termios{})
	if on.Cflag&CLOCAL == 0 || on.Cflag&HUPCL == 0 || on.Cflag&CRTSCTS != CRTSCTS {
		t.Errorf("switches did not all turn on: cflag=%#x", on.Cflag)
	}
	off := Config{Baud: 9600}.Termios(on)
	if off.Cflag&(CLOCAL|HUPCL|CRTSCTS) != 0 {
		t.Errorf("switches did not all turn off: cflag=%#x", off.Cflag)
	}
}

func TestConfigOfRoundTrip(t *testing.T) {
	for _, want := range []Config{
		{Baud: 115200, DataBits: 8, StopBits: 1, Parity: ParityNone, ReadMin: 1},
		{Baud: 9600, DataBits: 7, StopBits: 2, Parity: ParityOdd, CLOCAL: true, ReadMin: 4, ReadTimeoutDecis: 3},
		{Baud: 921600, DataBits: 5, StopBits: 1, Parity: ParityEven, HUPCL: true, RTSCTS: true},
		{Baud: 4800, DataBits: 6, StopBits: 1, Parity: ParityNone},
	} {
		got := ConfigOf(want.Termios(Termios{}))
		if got != want {
			t.Errorf("round trip changed %+v into %+v", want, got)
		}
	}
}

func TestConfigOfDefaultsDataBits(t *testing.T) {
	// A cflag whose CSIZE bits are none of CS5/6/7 must still report a width.
	if got := ConfigOf(Termios{Cflag: CS8, Ospeed: 9600}); got.DataBits != 8 {
		t.Errorf("DataBits = %d, want 8", got.DataBits)
	}
}

func TestConfigString(t *testing.T) {
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{Baud: 115200, DataBits: 8, StopBits: 1}, "115200 8N1 vmin=0 vtime=0"},
		{Config{Baud: 9600, DataBits: 7, StopBits: 2, Parity: ParityOdd}, "9600 7O2 vmin=0 vtime=0"},
		{Config{Baud: 9600, DataBits: 8, StopBits: 1, Parity: ParityEven}, "9600 8E1 vmin=0 vtime=0"},
		{Config{Baud: 1200, DataBits: 8, StopBits: 1, CLOCAL: true, HUPCL: true, RTSCTS: true, ReadMin: 1, ReadTimeoutDecis: 5},
			"1200 8N1 clocal hupcl rtscts vmin=1 vtime=5"},
	} {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}
