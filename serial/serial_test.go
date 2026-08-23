package serial

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestCalloutAndDialin(t *testing.T) {
	if got := Callout("usbmodem1"); got != "/dev/cu.usbmodem1" {
		t.Errorf("Callout = %q", got)
	}
	if got := Dialin("usbmodem1"); got != "/dev/tty.usbmodem1" {
		t.Errorf("Dialin = %q", got)
	}
}

func TestBaseName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/dev/cu.usbmodem1", "usbmodem1"},
		{"/dev/tty.usbmodem1", "usbmodem1"},
		{"cu.usbmodem1", "usbmodem1"},
		{"tty.usbmodem1", "usbmodem1"},
		{"usbmodem1", "usbmodem1"},
		{"/dev/ttys004", "ttys004"},
		{"", ""},
	} {
		if got := BaseName(tc.in); got != tc.want {
			t.Errorf("BaseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestList drives List through the seam, so the filtering, deduplication and
// sorting are tested without a /dev to depend on.
func TestList(t *testing.T) {
	old := listNames
	t.Cleanup(func() { listNames = old })

	listNames = func(dir string) ([]string, error) {
		if dir != DevDir {
			t.Errorf("List read %q, want %q", dir, DevDir)
		}
		return []string{
			"null", "tty.zeta", "cu.zeta", "cu.alpha", "random",
			"tty.alpha", "cu.", "tty.", "cu.mid",
		}, nil
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}

	sentinel := errors.New("boom")
	listNames = func(string) ([]string, error) { return nil, sentinel }
	if _, err := List(); !errors.Is(err, sentinel) {
		t.Errorf("List did not propagate the directory error: %v", err)
	}
}

// TestClosedPortIsNotAPanic covers every method's closed-port path. A nil *Port
// is included because a caller who ignored an Open error will produce one, and
// answering with an error beats a nil dereference.
func TestClosedPortIsNotAPanic(t *testing.T) {
	for name, p := range map[string]*Port{"nil": nil, "closed": {}} {
		t.Run(name, func(t *testing.T) {
			if err := p.Close(); err != nil {
				t.Errorf("Close on a %s port: %v", name, err)
			}
			if _, err := p.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
				t.Errorf("Read: %v", err)
			}
			if _, err := p.Write([]byte{1}); !errors.Is(err, ErrClosed) {
				t.Errorf("Write: %v", err)
			}
			if err := p.SetReadDeadline(time.Now()); !errors.Is(err, ErrClosed) {
				t.Errorf("SetReadDeadline: %v", err)
			}
			if err := p.Configure(Config{Baud: 9600}); !errors.Is(err, ErrClosed) {
				t.Errorf("Configure: %v", err)
			}
			if _, err := p.Termios(); !errors.Is(err, ErrClosed) {
				t.Errorf("Termios: %v", err)
			}
			if _, err := p.Lines(); !errors.Is(err, ErrClosed) {
				t.Errorf("Lines: %v", err)
			}
			if err := p.SetLines(LineDTR, 0); !errors.Is(err, ErrClosed) {
				t.Errorf("SetLines: %v", err)
			}
			if err := p.Flush(); !errors.Is(err, ErrClosed) {
				t.Errorf("Flush: %v", err)
			}
		})
	}
}

func TestConfigureRejectsBadConfig(t *testing.T) {
	p := &Port{}
	var bad *ErrBadConfig
	if err := p.Configure(Config{Baud: 0}); !errors.As(err, &bad) {
		t.Errorf("Configure validated a zero baud rate: %v", err)
	}
}

func TestOpenRejectsBadConfig(t *testing.T) {
	var bad *ErrBadConfig
	if _, err := Open("/dev/null", Config{}); !errors.As(err, &bad) {
		t.Errorf("Open validated a zero-value config: %v", err)
	}
}

// TestOpenPropagatesSeamFailures drives Open's two failure paths through the
// seams: the platform refusing to open, and the platform opening but handing
// back a descriptor that cannot be wrapped.
func TestOpenPropagatesSeamFailures(t *testing.T) {
	oldOpen, oldNew, oldClose := openPort, newFile, closeFD
	t.Cleanup(func() { openPort, newFile, closeFD = oldOpen, oldNew, oldClose })

	sentinel := errors.New("refused")
	openPort = func(string, bool) (uintptr, error) { return 0, sentinel }
	if _, err := Open("/dev/whatever", Config{Baud: 9600}); !errors.Is(err, sentinel) {
		t.Errorf("Open swallowed the platform error: %v", err)
	}

	closed := false
	openPort = func(string, bool) (uintptr, error) { return 7, nil }
	newFile = func(uintptr, string) *os.File { return nil }
	closeFD = func(fd uintptr) error {
		if fd != 7 {
			t.Errorf("closed fd %d, want 7", fd)
		}
		closed = true
		return nil
	}
	if _, err := Open("/dev/whatever", Config{Baud: 9600}); err == nil {
		t.Error("Open accepted a descriptor it could not wrap")
	}
	if !closed {
		t.Error("Open leaked the descriptor it could not wrap")
	}
}

// fakeReader is a deadline-honouring reader that is not a serial port, so
// Drain's loop can be driven through every exit it has.
type fakeReader struct {
	chunks   [][]byte
	err      error
	deadline time.Time
	setErr   error
	calls    int
}

func (f *fakeReader) SetReadDeadline(t time.Time) error {
	f.deadline = t
	return f.setErr
}

func (f *fakeReader) Read(b []byte) (int, error) {
	f.calls++
	if len(f.chunks) == 0 {
		return 0, f.err
	}
	c := f.chunks[0]
	f.chunks = f.chunks[1:]
	n := copy(b, c)
	return n, nil
}

// timeoutError is what a poller-backed Read returns when a deadline passes.
type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }

func TestDrainCollectsUntilTimeout(t *testing.T) {
	r := &fakeReader{chunks: [][]byte{{1, 2}, {3}}, err: timeoutError{}}
	buf := make([]byte, 16)
	n, err := Drain(r, buf, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("a timeout is the expected ending, not an error: %v", err)
	}
	if n != 3 || buf[0] != 1 || buf[1] != 2 || buf[2] != 3 {
		t.Fatalf("Drain got %d byte(s) %v", n, buf[:n])
	}
}

func TestDrainStopsAtEOF(t *testing.T) {
	r := &fakeReader{chunks: [][]byte{{9}}, err: io.EOF}
	buf := make([]byte, 8)
	n, err := Drain(r, buf, time.Now().Add(time.Second))
	if err != nil || n != 1 {
		t.Fatalf("Drain = %d, %v; want 1, nil", n, err)
	}
}

func TestDrainReportsRealErrors(t *testing.T) {
	sentinel := errors.New("cable unplugged")
	r := &fakeReader{err: sentinel}
	if _, err := Drain(r, make([]byte, 4), time.Now().Add(time.Second)); !errors.Is(err, sentinel) {
		t.Errorf("Drain hid a real error: %v", err)
	}
}

func TestDrainReportsDeadlineFailure(t *testing.T) {
	sentinel := errors.New("no deadlines here")
	r := &fakeReader{setErr: sentinel}
	if _, err := Drain(r, make([]byte, 4), time.Now().Add(time.Second)); !errors.Is(err, sentinel) {
		t.Errorf("Drain continued without a working deadline: %v", err)
	}
}

func TestDrainStopsWhenTheBufferIsFull(t *testing.T) {
	r := &fakeReader{chunks: [][]byte{{1, 2, 3, 4}, {5}}}
	buf := make([]byte, 4)
	n, err := Drain(r, buf, time.Now().Add(time.Second))
	if err != nil || n != 4 {
		t.Fatalf("Drain = %d, %v; want 4, nil", n, err)
	}
	if r.calls != 1 {
		t.Errorf("Drain read %d time(s) into a 4-byte buffer, want 1", r.calls)
	}
}

func TestDrainStopsAtTheDeadline(t *testing.T) {
	// A reader that keeps returning bytes must still be cut off, or a device
	// that babbles turns a bounded listen into an unbounded one.
	r := &fakeReader{chunks: [][]byte{{1}, {2}, {3}}}
	n, err := Drain(r, make([]byte, 64), time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Drain read %d byte(s) past its deadline, want 1", n)
	}
}

// TestListOnAMachineWithNoDev covers the branch that keeps a Windows build of a
// probe printing "0 ports" instead of complaining about a Unix path.
func TestListOnAMachineWithNoDev(t *testing.T) {
	old := listNames
	t.Cleanup(func() { listNames = old })
	listNames = func(string) ([]string, error) {
		return nil, &os.PathError{Op: "open", Path: DevDir, Err: os.ErrNotExist}
	}
	got, err := List()
	if err != nil {
		t.Fatalf("List = %v, want no ports and no error", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want none", got)
	}
}
