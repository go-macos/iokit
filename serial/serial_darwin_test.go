//go:build darwin

package serial

import (
	"errors"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestConstantsMatchTheRealHeaders is the reason the portable file is allowed
// to hardcode termios bits at all. Every constant is checked against
// golang.org/x/sys/unix, which generates its values from the macOS headers, so
// a typo cannot survive.
func TestConstantsMatchTheRealHeaders(t *testing.T) {
	for name, pair := range map[string][2]uint64{
		"IGNBRK": {IGNBRK, unix.IGNBRK}, "BRKINT": {BRKINT, unix.BRKINT},
		"IGNPAR": {IGNPAR, unix.IGNPAR}, "PARMRK": {PARMRK, unix.PARMRK},
		"INPCK": {INPCK, unix.INPCK}, "ISTRIP": {ISTRIP, unix.ISTRIP},
		"INLCR": {INLCR, unix.INLCR}, "IGNCR": {IGNCR, unix.IGNCR},
		"ICRNL": {ICRNL, unix.ICRNL}, "IXON": {IXON, unix.IXON},
		"IXOFF": {IXOFF, unix.IXOFF}, "IXANY": {IXANY, unix.IXANY},
		"OPOST": {OPOST, unix.OPOST}, "ONLCR": {ONLCR, unix.ONLCR},
		"CSIZE": {CSIZE, unix.CSIZE}, "CS5": {CS5, unix.CS5}, "CS6": {CS6, unix.CS6},
		"CS7": {CS7, unix.CS7}, "CS8": {CS8, unix.CS8},
		"CSTOPB": {CSTOPB, unix.CSTOPB}, "CREAD": {CREAD, unix.CREAD},
		"PARENB": {PARENB, unix.PARENB}, "PARODD": {PARODD, unix.PARODD},
		"HUPCL": {HUPCL, unix.HUPCL}, "CLOCAL": {CLOCAL, unix.CLOCAL},
		"CRTSCTS":             {CRTSCTS, unix.CRTSCTS},
		"CCTSOFLOW|CRTSIFLOW": {CCTSOFLOW | CRTSIFLOW, unix.CRTSCTS},
		"ECHOE":               {ECHOE, unix.ECHOE}, "ECHOK": {ECHOK, unix.ECHOK},
		"ECHO": {ECHO, unix.ECHO}, "ECHONL": {ECHONL, unix.ECHONL},
		"ISIG": {ISIG, unix.ISIG}, "ICANON": {ICANON, unix.ICANON},
		"IEXTEN": {IEXTEN, unix.IEXTEN},
		"VMIN":   {VMIN, unix.VMIN}, "VTIME": {VTIME, unix.VTIME},
		"LineDTR": {uint64(LineDTR), unix.TIOCM_DTR},
		"LineRTS": {uint64(LineRTS), unix.TIOCM_RTS},
		"LineCTS": {uint64(LineCTS), unix.TIOCM_CTS},
		"LineDCD": {uint64(LineDCD), unix.TIOCM_CAR},
		"LineRI":  {uint64(LineRI), unix.TIOCM_RI},
		"LineDSR": {uint64(LineDSR), unix.TIOCM_DSR},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %#x, but the system header says %#x", name, pair[0], pair[1])
		}
	}
	// NCCS has no exported name in x/sys, so it is checked against the length
	// of the control-character array in the generated struct.
	var sys unix.Termios
	if NCCS != len(sys.Cc) {
		t.Errorf("NCCS = %d, but unix.Termios.Cc holds %d", NCCS, len(sys.Cc))
	}
}

// TestTermiosLayoutMatchesTheSystem checks that the portable mirror of struct
// termios is byte-for-byte the system's, so the field-by-field copy in
// serial_darwin.go is copying between two identical shapes.
func TestTermiosLayoutMatchesTheSystem(t *testing.T) {
	var mine Termios
	var theirs unix.Termios
	if unsafe.Sizeof(mine) != unsafe.Sizeof(theirs) {
		t.Fatalf("Termios is %d bytes, unix.Termios is %d", unsafe.Sizeof(mine), unsafe.Sizeof(theirs))
	}
	for _, f := range []struct {
		name      string
		mine, sys uintptr
	}{
		{"Iflag", unsafe.Offsetof(mine.Iflag), unsafe.Offsetof(theirs.Iflag)},
		{"Oflag", unsafe.Offsetof(mine.Oflag), unsafe.Offsetof(theirs.Oflag)},
		{"Cflag", unsafe.Offsetof(mine.Cflag), unsafe.Offsetof(theirs.Cflag)},
		{"Lflag", unsafe.Offsetof(mine.Lflag), unsafe.Offsetof(theirs.Lflag)},
		{"Cc", unsafe.Offsetof(mine.Cc), unsafe.Offsetof(theirs.Cc)},
		{"Ispeed", unsafe.Offsetof(mine.Ispeed), unsafe.Offsetof(theirs.Ispeed)},
		{"Ospeed", unsafe.Offsetof(mine.Ospeed), unsafe.Offsetof(theirs.Ospeed)},
	} {
		if f.mine != f.sys {
			t.Errorf("%s is at offset %d here and %d in the system struct", f.name, f.mine, f.sys)
		}
	}
}

func TestUnixConversionRoundTrip(t *testing.T) {
	want := Termios{Iflag: 1, Oflag: 2, Cflag: 3, Lflag: 4, Ispeed: 115200, Ospeed: 115200}
	want.Cc[VMIN] = 1
	want.Cc[VTIME] = 7
	u := toUnix(want)
	if got := fromUnix(&u); got != want {
		t.Errorf("round trip changed %+v into %+v", want, got)
	}
}

// loopbackPort is the shared fixture: a pseudo-terminal pair with a real Port
// on the slave side. It is the only honest way to exercise the darwin syscalls
// without a device, and it doubles as the control that proves the reader reads.
func loopbackPort(t *testing.T, cfg Config) (*os.File, *Port) {
	t.Helper()
	master, slave, err := Loopback()
	if err != nil {
		t.Fatalf("allocating a pty: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	p, err := Open(slave, cfg)
	if err != nil {
		t.Fatalf("opening %s: %v", slave, err)
	}
	t.Cleanup(func() { p.Close() })
	return master, p
}

// TestLoopbackCarriesBytes is the control. Everything else this package claims
// about a silent device rests on the reader being able to read at all.
func TestLoopbackCarriesBytes(t *testing.T) {
	master, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	if p.Name() == "" {
		t.Error("Name should report the device path")
	}
	want := []byte{0xFF, 0xFC, 0xDE, 0xAD, 0x03}
	if _, err := master.Write(want); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := Drain(p, buf, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("read %d byte(s) %x, want %d", n, buf[:n], len(want))
	}
	for i := range want {
		if buf[i] != want[i] {
			t.Fatalf("read %x, want %x", buf[:n], want)
		}
	}
}

// TestIdleReadTimesOut is the other half of the control: silence must be
// reported, not waited on. A reader that hangs cannot conclude anything.
func TestIdleReadTimesOut(t *testing.T) {
	_, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	start := time.Now()
	n, err := Drain(p, make([]byte, 32), time.Now().Add(200*time.Millisecond))
	if err != nil {
		t.Fatalf("an idle read should time out, not fail: %v", err)
	}
	if n != 0 {
		t.Fatalf("an idle port produced %d byte(s)", n)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Fatalf("the read came back after %v: the deadline is not being honoured", waited)
	}
}

// TestVMINZeroIsTheTrap records why Config.ReadMin defaults to 1 in every
// caller. With VMIN and VTIME both zero the driver returns a zero-byte read
// immediately, Go reports that as io.EOF, and a listen that should have lasted
// seconds ends in microseconds -- looking exactly like a mute device.
func TestVMINZeroIsTheTrap(t *testing.T) {
	_, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 0})
	start := time.Now()
	n, err := Drain(p, make([]byte, 32), time.Now().Add(500*time.Millisecond))
	if err != nil || n != 0 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Skip("this kernel blocks on VMIN=0, so the trap does not reproduce here")
	}
}

func TestPortWriteAndConfigure(t *testing.T) {
	master, p := loopbackPort(t, Config{Baud: 9600, CLOCAL: true, ReadMin: 1})
	if got := p.Config().Baud; got != 9600 {
		t.Errorf("applied baud = %d, want 9600", got)
	}
	if err := p.Configure(Config{Baud: 115200, DataBits: 8, StopBits: 1, CLOCAL: true, ReadMin: 1}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if got := p.Config().Baud; got != 115200 {
		t.Errorf("reconfigured baud = %d, want 115200", got)
	}
	tio, err := p.Termios()
	if err != nil {
		t.Fatalf("Termios: %v", err)
	}
	if tio.Cflag&CLOCAL == 0 {
		t.Error("CLOCAL did not reach the kernel")
	}
	if tio.Lflag&ICANON != 0 {
		t.Error("the port is still in canonical mode")
	}

	if _, err := p.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 16)
	if err := master.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := master.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("the master read %q, %v", buf[:n], err)
	}

	if err := p.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}

// TestLinesOnAPty checks the TIOCMGET/TIOCMBIS/TIOCMBIC path. A pty reports
// modem lines that a real UART would drive, which is enough to prove the
// ioctls are wired correctly even though nothing physical moves.
func TestLinesOnAPty(t *testing.T) {
	_, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	before, err := p.Lines()
	if err != nil {
		t.Skipf("this pty does not implement TIOCMGET: %v", err)
	}
	if err := p.SetLines(LineDTR|LineRTS, 0); err != nil {
		t.Fatalf("SetLines: %v", err)
	}
	after, err := p.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if !after.Has(LineDTR | LineRTS) {
		t.Errorf("after asserting DTR|RTS the lines read %s (were %s)", after, before)
	}
	if err := p.SetLines(0, LineDTR|LineRTS); err != nil {
		t.Fatalf("SetLines clearing: %v", err)
	}
	if down, err := p.Lines(); err == nil && down&(LineDTR|LineRTS) != 0 {
		t.Errorf("after clearing DTR|RTS the lines read %s", down)
	}
	if err := p.SetLines(0, 0); err != nil {
		t.Errorf("a no-op SetLines should succeed: %v", err)
	}
}

func TestOpenMissingDevice(t *testing.T) {
	_, err := Open("/dev/cu.this-device-does-not-exist", Config{Baud: 9600})
	if err == nil {
		t.Fatal("opening a device that is not there should fail")
	}
	var pe *os.PathError
	if !errors.As(err, &pe) {
		t.Errorf("want an *os.PathError naming the device, got %v", err)
	}
}

// TestOpenBlocking exercises the blocking branch of darwinOpen, which Open
// itself never takes.
func TestOpenBlocking(t *testing.T) {
	_, slave, err := Loopback()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := openPort(slave, true)
	if err != nil {
		t.Fatalf("blocking open of %s: %v", slave, err)
	}
	if err := closeFD(fd); err != nil {
		t.Errorf("closeFD: %v", err)
	}
	if _, err := openPort("/dev/cu.absent", true); err == nil {
		t.Error("a blocking open of a missing device should fail")
	}
}

// TestListSeesTheRealDev proves the darwin listNames seam reads a directory,
// and that a machine with no serial port at all is not an error. A CI runner
// legitimately has none.
func TestListSeesTheRealDev(t *testing.T) {
	names, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("%d serial port(s): %v", len(names), names)
	if _, err := listNames("/this/directory/does/not/exist"); err == nil {
		t.Error("listing a missing directory should fail")
	}
}

// TestSetSpeedOnAPty covers the IOSSIOSPEED seam. A pty is free to refuse it;
// what is being tested is that the ioctl is issued and its result reported,
// not that a pseudo-terminal has a baud rate.
func TestSetSpeedOnAPty(t *testing.T) {
	_, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	err := p.do(func(fd uintptr) error { return setSpeed(fd, 115200) })
	t.Logf("IOSSIOSPEED on a pty: %v", err)
}

func TestTimeoutHelpersOnClosedPort(t *testing.T) {
	p := &Port{}
	if err := p.do(func(uintptr) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("do on a closed port: %v", err)
	}
}

// TestModemBitsIoctlIsIssued covers the TIOCMBIS/TIOCMBIC seam on darwin. A
// pseudo-terminal is entitled to refuse both; what is under test is that the
// ioctl is issued and whatever the kernel says is passed back, not that a pty
// has modem lines.
func TestModemBitsIoctlIsIssued(t *testing.T) {
	_, p := loopbackPort(t, Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	err := p.do(func(fd uintptr) error { return bisLines(fd, LineDTR) })
	t.Logf("TIOCMBIS on a pty: %v", err)
	err = p.do(func(fd uintptr) error { return bicLines(fd, LineDTR) })
	t.Logf("TIOCMBIC on a pty: %v", err)
}
