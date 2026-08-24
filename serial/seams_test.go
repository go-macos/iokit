package serial

import (
	"errors"
	"os"
	"testing"
)

// This file drives the platform seams with substitutes, so the portable half of
// the package is exercised on every operating system -- including the branches
// a working pseudo-terminal never takes, such as a driver that rejects a line
// rate outright.

// fakeSeams installs substitutes for every seam and restores them afterwards.
type fakeSeams struct {
	attr      Termios
	setErr    error
	setCalls  []Termios
	speedErr  error
	speedSeen []int
	getErr    error
	lines     Lines
	linesErr  error
	bisSeen   []Lines
	bicSeen   []Lines
	bisErr    error
	bicErr    error
	flushErr  error
	flushed   int
}

func installFakes(t *testing.T, f *fakeSeams) {
	t.Helper()
	oldGet, oldSet, oldSpeed := getAttr, setAttr, setSpeed
	oldGetL, oldBis, oldBic, oldFlush := getLines, bisLines, bicLines, flushIO
	t.Cleanup(func() {
		getAttr, setAttr, setSpeed = oldGet, oldSet, oldSpeed
		getLines, bisLines, bicLines, flushIO = oldGetL, oldBis, oldBic, oldFlush
	})
	getAttr = func(uintptr) (Termios, error) { return f.attr, f.getErr }
	setAttr = func(_ uintptr, t Termios) error {
		f.setCalls = append(f.setCalls, t)
		if f.setErr != nil && len(f.setCalls) == 1 {
			return f.setErr
		}
		f.attr = t
		return nil
	}
	setSpeed = func(_ uintptr, baud int) error {
		f.speedSeen = append(f.speedSeen, baud)
		return f.speedErr
	}
	getLines = func(uintptr) (Lines, error) { return f.lines, f.linesErr }
	bisLines = func(_ uintptr, l Lines) error { f.bisSeen = append(f.bisSeen, l); return f.bisErr }
	bicLines = func(_ uintptr, l Lines) error { f.bicSeen = append(f.bicSeen, l); return f.bicErr }
	flushIO = func(uintptr) error { f.flushed++; return f.flushErr }
}

// fakePort is a Port backed by an ordinary file, so the seam-driven tests need
// no tty at all and run identically on Linux and Windows.
func fakePort(t *testing.T) *Port {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "port")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &Port{f: f, name: f.Name()}
}

// TestConfigureFallsBackToIOSSIOSPEED is the branch that made 3 Mbaud reachable
// on a real device: the driver rejected the whole termios over the rate alone,
// so the rate is dropped from the termios and insisted on separately.
func TestConfigureFallsBackToIOSSIOSPEED(t *testing.T) {
	f := &fakeSeams{attr: Termios{Ispeed: 9600, Ospeed: 9600}, setErr: errors.New("invalid argument")}
	installFakes(t, f)
	p := fakePort(t)

	if err := p.Configure(Config{Baud: 3000000, CLOCAL: true, ReadMin: 1}); err != nil {
		t.Fatalf("Configure should have recovered through IOSSIOSPEED: %v", err)
	}
	if len(f.setCalls) != 2 {
		t.Fatalf("tcsetattr was called %d time(s), want 2", len(f.setCalls))
	}
	if f.setCalls[0].Ospeed != 3000000 {
		t.Errorf("the first attempt asked for %d, want 3000000", f.setCalls[0].Ospeed)
	}
	if f.setCalls[1].Ospeed != 9600 {
		t.Errorf("the retry asked for %d, want the rate already in force, 9600", f.setCalls[1].Ospeed)
	}
	if len(f.speedSeen) == 0 || f.speedSeen[0] != 3000000 {
		t.Errorf("IOSSIOSPEED saw %v, want 3000000 first", f.speedSeen)
	}
}

// TestConfigureGivesUpWhenThereIsNoRateToKeep covers the branch where the
// termios call failed and the port has no current rate to fall back to, so the
// original error is the honest thing to report.
func TestConfigureGivesUpWhenThereIsNoRateToKeep(t *testing.T) {
	sentinel := errors.New("invalid argument")
	f := &fakeSeams{setErr: sentinel} // attr.Ospeed is zero
	installFakes(t, f)
	if err := fakePort(t).Configure(Config{Baud: 3000000}); !errors.Is(err, sentinel) {
		t.Errorf("Configure hid the driver's refusal: %v", err)
	}
}

func TestConfigureReportsTheRetryFailing(t *testing.T) {
	sentinel := errors.New("invalid argument")
	f := &fakeSeams{attr: Termios{Ospeed: 9600}, setErr: sentinel, speedErr: errors.New("no such ioctl")}
	installFakes(t, f)
	if err := fakePort(t).Configure(Config{Baud: 3000000}); !errors.Is(err, sentinel) {
		t.Errorf("Configure should report the original refusal: %v", err)
	}
}

func TestConfigureReportsGetAttrFailing(t *testing.T) {
	sentinel := errors.New("not a terminal")
	f := &fakeSeams{getErr: sentinel}
	installFakes(t, f)
	if err := fakePort(t).Configure(Config{Baud: 9600}); !errors.Is(err, sentinel) {
		t.Errorf("Configure: %v", err)
	}
	if _, err := fakePort(t).Termios(); !errors.Is(err, sentinel) {
		t.Errorf("Termios: %v", err)
	}
}

func TestSetLinesDrivesBothIoctls(t *testing.T) {
	f := &fakeSeams{}
	installFakes(t, f)
	p := fakePort(t)

	if err := p.SetLines(LineDTR|LineRTS, LineDSR); err != nil {
		t.Fatal(err)
	}
	if len(f.bisSeen) != 1 || f.bisSeen[0] != LineDTR|LineRTS {
		t.Errorf("TIOCMBIS saw %v", f.bisSeen)
	}
	if len(f.bicSeen) != 1 || f.bicSeen[0] != LineDSR {
		t.Errorf("TIOCMBIC saw %v", f.bicSeen)
	}

	// A no-op must issue nothing: asserting an empty set would clear the lines
	// on drivers that treat the mask as absolute.
	f.bisSeen, f.bicSeen = nil, nil
	if err := p.SetLines(0, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.bisSeen) != 0 || len(f.bicSeen) != 0 {
		t.Errorf("a no-op SetLines issued ioctls: bis=%v bic=%v", f.bisSeen, f.bicSeen)
	}
}

func TestSetLinesReportsFailures(t *testing.T) {
	bis := errors.New("bis failed")
	f := &fakeSeams{bisErr: bis}
	installFakes(t, f)
	if err := fakePort(t).SetLines(LineDTR, LineRTS); !errors.Is(err, bis) {
		t.Errorf("SetLines: %v", err)
	}

	bic := errors.New("bic failed")
	f2 := &fakeSeams{bicErr: bic}
	installFakes(t, f2)
	if err := fakePort(t).SetLines(LineDTR, LineRTS); !errors.Is(err, bic) {
		t.Errorf("SetLines: %v", err)
	}
}

func TestLinesAndFlushGoThroughTheSeams(t *testing.T) {
	f := &fakeSeams{lines: LineDTR | LineDCD}
	installFakes(t, f)
	p := fakePort(t)

	got, err := p.Lines()
	if err != nil || got != LineDTR|LineDCD {
		t.Fatalf("Lines = %s, %v", got, err)
	}
	if err := p.Flush(); err != nil || f.flushed != 1 {
		t.Fatalf("Flush = %v after %d call(s)", err, f.flushed)
	}

	sentinel := errors.New("no lines here")
	f.linesErr = sentinel
	if _, err := p.Lines(); !errors.Is(err, sentinel) {
		t.Errorf("Lines: %v", err)
	}
}

// TestOpenSucceedsThroughTheSeams covers Open's happy path without a device,
// and checks that the port reports the configuration the kernel confirmed
// rather than the one it was asked for.
func TestOpenSucceedsThroughTheSeams(t *testing.T) {
	f := &fakeSeams{}
	installFakes(t, f)

	tmp, err := os.CreateTemp(t.TempDir(), "port")
	if err != nil {
		t.Fatal(err)
	}
	oldOpen, oldNew := openPort, newFile
	t.Cleanup(func() { openPort, newFile = oldOpen, oldNew })
	openPort = func(string, bool) (uintptr, error) { return 3, nil }
	newFile = func(uintptr, string) *os.File { return tmp }

	p, err := Open("/dev/cu.pretend", Config{Baud: 115200, CLOCAL: true, ReadMin: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Name() != "/dev/cu.pretend" {
		t.Errorf("Name = %q", p.Name())
	}
	if got := p.Config(); got.Baud != 115200 || !got.CLOCAL || got.ReadMin != 1 {
		t.Errorf("Config = %s", got)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("closing twice should be harmless: %v", err)
	}
}

// TestOpenReportsAConfigureFailure covers the path where the descriptor opened
// but could not be configured: the port must be closed rather than handed back
// half-set-up.
func TestOpenReportsAConfigureFailure(t *testing.T) {
	sentinel := errors.New("not a terminal")
	f := &fakeSeams{getErr: sentinel}
	installFakes(t, f)

	tmp, err := os.CreateTemp(t.TempDir(), "port")
	if err != nil {
		t.Fatal(err)
	}
	oldOpen, oldNew := openPort, newFile
	t.Cleanup(func() { openPort, newFile = oldOpen, oldNew })
	openPort = func(string, bool) (uintptr, error) { return 3, nil }
	newFile = func(uintptr, string) *os.File { return tmp }

	if _, err := Open("/dev/cu.pretend", Config{Baud: 9600}); !errors.Is(err, sentinel) {
		t.Errorf("Open: %v", err)
	}
}

// TestDoReportsARawControlFailure covers the branch where the runtime refuses
// to hand over the descriptor at all -- a file already closed underneath the
// Port, for instance.
func TestDoReportsARawControlFailure(t *testing.T) {
	sentinel := errors.New("file already closed")
	old := rawControl
	t.Cleanup(func() { rawControl = old })
	rawControl = func(*os.File, func(uintptr)) error { return sentinel }

	if err := fakePort(t).Flush(); !errors.Is(err, sentinel) {
		t.Errorf("Flush: %v", err)
	}
}

// TestConfigureReportsTheReadBackFailing covers the last branch of Configure:
// the settings applied, but the kernel could not be asked what it kept. A
// configuration that cannot be read back has not been confirmed, so it is an
// error rather than an assumption.
func TestConfigureReportsTheReadBackFailing(t *testing.T) {
	sentinel := errors.New("gone")
	oldGet, oldSet, oldSpeed := getAttr, setAttr, setSpeed
	t.Cleanup(func() { getAttr, setAttr, setSpeed = oldGet, oldSet, oldSpeed })

	calls := 0
	getAttr = func(uintptr) (Termios, error) {
		calls++
		if calls == 1 {
			return Termios{Ospeed: 9600}, nil
		}
		return Termios{}, sentinel
	}
	setAttr = func(uintptr, Termios) error { return nil }
	setSpeed = func(uintptr, int) error { return nil }

	if err := fakePort(t).Configure(Config{Baud: 9600}); !errors.Is(err, sentinel) {
		t.Errorf("Configure: %v", err)
	}
}

// TestConfigureReportsTheRetryTermiosFailing covers the branch where dropping
// the rate did not help either: the driver refused the settings themselves, and
// the original refusal is what the caller needs to see.
func TestConfigureReportsTheRetryTermiosFailing(t *testing.T) {
	sentinel := errors.New("invalid argument")
	oldGet, oldSet := getAttr, setAttr
	t.Cleanup(func() { getAttr, setAttr = oldGet, oldSet })
	getAttr = func(uintptr) (Termios, error) { return Termios{Ospeed: 9600}, nil }
	setAttr = func(uintptr, Termios) error { return sentinel }

	if err := fakePort(t).Configure(Config{Baud: 3000000}); !errors.Is(err, sentinel) {
		t.Errorf("Configure: %v", err)
	}
}
