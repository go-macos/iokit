// Package serial opens and configures a macOS serial port with no cgo.
//
// It exists because a USB device's interesting protocol is not always on a HID
// interface or on endpoint 0. A composite device that publishes a CDC-ACM
// function gets a /dev/cu.* and /dev/tty.* pair from Apple's AppleUSBACMData
// driver, and that pair is a channel the sibling hid and usb packages in this
// module cannot reach: the kernel driver owns the bulk pipes, so the only way
// in is the tty.
//
// # cu versus tty, which is the whole trap
//
// macOS publishes two nodes for one port.
//
//   - /dev/tty.NAME is the dial-IN node. Opening it blocks until the device
//     asserts carrier detect, and it raises DTR. A USB CDC device that never
//     drives DCD will hang an open forever unless the caller passes
//     O_NONBLOCK, which [Open] always does, and then sets CLOCAL.
//   - /dev/cu.NAME is the call-OUT node. It opens immediately and does not
//     wait for carrier -- and it does not raise DTR either.
//
// A device whose firmware only starts talking once DTR is asserted is
// therefore mute on /dev/cu.* and alive on /dev/tty.*, with no error either
// way. [Port.SetLines] asserts the lines explicitly so the question can be
// asked of both nodes rather than guessed.
//
// # What an accepted write proves
//
// Nothing, on its own. The lesson the sibling packages record applies here
// unchanged: write(2) to a tty returns the byte count as soon as the line
// discipline has queued the bytes, whether or not the device on the other end
// understood a single one of them. Only bytes coming back are evidence, which
// is why [Loopback] exists -- a probe that has not proved its reader on a
// known-good port has not proved that silence means anything.
package serial

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

// ErrUnsupported is returned by every entry point on a platform that has no
// BSD termios: Linux and Windows builds compile, and fail cleanly.
var ErrUnsupported = errors.New("serial: unsupported on this platform")

// ErrClosed is returned by a method called after [Port.Close].
var ErrClosed = errors.New("serial: port is closed")

// DevDir is where macOS publishes tty nodes.
const DevDir = "/dev"

// Prefixes of the two node families macOS publishes for one port.
const (
	// CalloutPrefix names the dial-out node, which opens without waiting for
	// carrier and leaves DTR low.
	CalloutPrefix = "cu."
	// DialinPrefix names the dial-in node, which raises DTR and waits for
	// carrier.
	DialinPrefix = "tty."
)

// Seams. The darwin file fills them with real system calls; the stub file fills
// them with ErrUnsupported. Everything above this line is portable and tested
// on every platform.
var (
	openPort  func(name string, blocking bool) (uintptr, error)
	closeFD   func(fd uintptr) error
	getAttr   func(fd uintptr) (Termios, error)
	setAttr   func(fd uintptr, t Termios) error
	setSpeed  func(fd uintptr, baud int) error
	getLines  func(fd uintptr) (Lines, error)
	bisLines  func(fd uintptr, l Lines) error
	bicLines  func(fd uintptr, l Lines) error
	flushIO   func(fd uintptr) error
	newFile   func(fd uintptr, name string) *os.File
	loopback  func() (*os.File, string, error)
	listNames func(dir string) ([]string, error)
)

// Callout turns a port's base name into its /dev/cu.* path.
func Callout(base string) string { return DevDir + "/" + CalloutPrefix + base }

// Dialin turns a port's base name into its /dev/tty.* path.
func Dialin(base string) string { return DevDir + "/" + DialinPrefix + base }

// BaseName strips the directory and the cu./tty. prefix from a device path,
// returning the name the two nodes share. A path that is not a serial node
// comes back unchanged.
func BaseName(path string) string {
	n := path
	if i := strings.LastIndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	for _, p := range []string{CalloutPrefix, DialinPrefix} {
		if strings.HasPrefix(n, p) {
			return n[len(p):]
		}
	}
	return n
}

// List returns the base names of every serial port macOS currently publishes,
// sorted and deduplicated, so a port that has both nodes appears once. A
// platform with no /dev directory reports no ports rather than an error.
func List() ([]string, error) {
	names, err := listNames(DevDir)
	if errors.Is(err, fs.ErrNotExist) {
		// A platform with no /dev at all has no serial ports. That is an
		// answer, not a failure, and it keeps a Windows build of a probe
		// printing "0 ports" instead of an error about a Unix path.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, CalloutPrefix) && !strings.HasPrefix(n, DialinPrefix) {
			continue
		}
		b := BaseName(n)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	sort.Strings(out)
	return out, nil
}

// Port is an open serial port.
//
// Reads and writes go through an *os.File on a non-blocking descriptor, so the
// Go runtime's poller services them and [Port.SetReadDeadline] works. That
// matters more than it sounds: a probe that cannot time out a read cannot tell
// "the device sent nothing" from "the program is wedged".
type Port struct {
	f    *os.File
	name string
	cfg  Config
}

// Open opens the named device -- a full path such as /dev/cu.usbmodem14201 --
// and applies cfg.
//
// The descriptor is always opened O_NONBLOCK|O_NOCTTY, so opening a /dev/tty.*
// node does not hang waiting for carrier detect and the port does not become
// the process's controlling terminal. If cfg.CLOCAL is set the carrier
// requirement is then dropped for good; if it is not, a later blocking read on
// a dial-in node can still stall, which is the caller's choice to make.
func Open(name string, cfg Config) (*Port, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	fd, err := openPort(name, false)
	if err != nil {
		return nil, err
	}
	p := &Port{f: newFile(fd, name), name: name, cfg: cfg}
	if p.f == nil {
		_ = closeFD(fd)
		return nil, errors.New("serial: could not wrap descriptor for " + name)
	}
	if err := p.Configure(cfg); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

// Name is the device path the port was opened with.
func (p *Port) Name() string { return p.name }

// Config is the configuration last successfully applied.
func (p *Port) Config() Config { return p.cfg }

// do runs fn with the port's descriptor.
//
// It goes through SyscallConn rather than File.Fd because Fd hands the caller a
// descriptor the runtime poller has given up on, which would silently disable
// every read deadline in this package -- and a probe that cannot time out a
// read cannot report that a device said nothing.
func (p *Port) do(fn func(fd uintptr) error) error {
	if p == nil || p.f == nil {
		return ErrClosed
	}
	var inner error
	if err := rawControl(p.f, func(fd uintptr) { inner = fn(fd) }); err != nil {
		return err
	}
	return inner
}

// rawControl runs fn with a file's descriptor while the runtime holds it open.
//
// It is a variable so a test can drive do's failure path without a broken
// file, and it is the same on every platform, so it lives here rather than in
// the platform files.
var rawControl = func(f *os.File, fn func(fd uintptr)) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(fn)
}

// Configure applies cfg to an already open port.
//
// The termios is read back after the write and compared, because tcsetattr is
// documented to succeed when it applied *some* of what it was asked for. A
// driver that silently refused the requested rate is the difference between a
// mute device and a misconfigured one, and this is the only place that
// distinction can be caught.
func (p *Port) Configure(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var got Termios
	err := p.do(func(fd uintptr) error {
		cur, err := getAttr(fd)
		if err != nil {
			return err
		}
		if err := setAttr(fd, cfg.Termios(cur)); err != nil {
			// A driver with a table of standard rates answers EINVAL to any
			// rate not in it, and rejects the whole termios -- parity, data
			// bits and all -- over that one field. Apply everything else at
			// the rate already in force, then insist on the rate through
			// IOSSIOSPEED, which is what macOS offers precisely for this.
			keep := cfg
			keep.Baud = int(cur.Ospeed)
			if keep.Baud <= 0 {
				return err
			}
			if err2 := setAttr(fd, keep.Termios(cur)); err2 != nil {
				return err
			}
			if err2 := setSpeed(fd, cfg.Baud); err2 != nil {
				return err
			}
		}
		// Even when the termios call took the rate, IOSSIOSPEED is asked again:
		// BSD stores the literal number, but a USB CDC driver is free to round
		// it to its nearest table entry without saying so. A failure here is not
		// fatal, because the read-back below is what decides.
		_ = setSpeed(fd, cfg.Baud)
		got, err = getAttr(fd)
		return err
	})
	if err != nil {
		return err
	}
	p.cfg = ConfigOf(got)
	return nil
}

// Termios returns the port's current termios as the kernel holds it.
func (p *Port) Termios() (Termios, error) {
	var t Termios
	err := p.do(func(fd uintptr) error {
		var err error
		t, err = getAttr(fd)
		return err
	})
	return t, err
}

// Lines reads the modem control lines.
func (p *Port) Lines() (Lines, error) {
	var l Lines
	err := p.do(func(fd uintptr) error {
		var err error
		l, err = getLines(fd)
		return err
	})
	return l, err
}

// SetLines asserts the bits in set and clears the bits in clear. Passing
// LineDTR|LineRTS in set is the handshake a CDC device that waits for a host
// expects, and the one a /dev/cu.* node never performs on its own.
func (p *Port) SetLines(set, clear Lines) error {
	return p.do(func(fd uintptr) error {
		if set != 0 {
			if err := bisLines(fd, set); err != nil {
				return err
			}
		}
		if clear != 0 {
			if err := bicLines(fd, clear); err != nil {
				return err
			}
		}
		return nil
	})
}

// Flush discards whatever the kernel has buffered in both directions, so a
// listen that follows starts from a known-empty queue.
func (p *Port) Flush() error {
	return p.do(flushIO)
}

// Read reads from the port. It honours the deadline set by
// [Port.SetReadDeadline], returning an error for which os.IsTimeout is true.
func (p *Port) Read(b []byte) (int, error) {
	if p == nil || p.f == nil {
		return 0, ErrClosed
	}
	return p.f.Read(b)
}

// Write writes to the port. A successful return means the line discipline
// queued the bytes; it says nothing at all about the device.
func (p *Port) Write(b []byte) (int, error) {
	if p == nil || p.f == nil {
		return 0, ErrClosed
	}
	return p.f.Write(b)
}

// SetReadDeadline bounds subsequent reads.
func (p *Port) SetReadDeadline(t time.Time) error {
	if p == nil || p.f == nil {
		return ErrClosed
	}
	return p.f.SetReadDeadline(t)
}

// Close closes the port. Closing twice is not an error.
func (p *Port) Close() error {
	if p == nil || p.f == nil {
		return nil
	}
	f := p.f
	p.f = nil
	return f.Close()
}

// Loopback returns a pseudo-terminal pair: an *os.File on the master side and
// the /dev/ttys* path of the slave, which [Open] accepts like any other tty.
//
// It is the control instrument. Bytes written to the master come out of a Port
// opened on the slave path, so a probe can prove its reader works before
// concluding that a real device's silence means anything. The HID half of this
// module learned that lesson the expensive way.
func Loopback() (*os.File, string, error) { return loopback() }

// Drain reads from r until the deadline passes or the buffer is full,
// returning everything that arrived. A timeout is not an error: it is the
// expected way for the call to end, and the byte count is the finding.
//
// r must honour a read deadline, which is why it is asked for one: a reader
// that cannot time out would turn this into a hang.
func Drain(r deadlineReader, buf []byte, until time.Time) (int, error) {
	n := 0
	for n < len(buf) {
		if err := r.SetReadDeadline(until); err != nil {
			return n, err
		}
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			if os.IsTimeout(err) || errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, err
		}
		if !time.Now().Before(until) {
			return n, nil
		}
	}
	return n, nil
}

// deadlineReader is the part of [Port] that [Drain] needs, named so a test can
// substitute a reader that is not a serial port at all.
type deadlineReader interface {
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
}
