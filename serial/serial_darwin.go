//go:build darwin

package serial

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// IOSSIOSPEED is the macOS-only ioctl that sets an arbitrary line rate,
// bypassing the driver's table of standard speeds. It is _IOW('T', 2, speed_t)
// with speed_t being 8 bytes on both arm64 and amd64, hence the 0x0008 size
// field. golang.org/x/sys/unix does not define it, because it is not a BSD
// ioctl -- it belongs to Apple's IOSerialFamily.
//
// It is what makes 921600, 1000000 and 3000000 reachable on a USB CDC port
// whose driver would otherwise round the request down to the nearest standard
// rate without saying so.
const IOSSIOSPEED = 0x80085402

// TIOCFLUSH takes a bitmask of the two queues to discard. These are the
// <sys/fcntl.h> FREAD and FWRITE values, which x/sys/unix does not export;
// tcflush(fd, TCIOFLUSH) is exactly ioctl(fd, TIOCFLUSH, FREAD|FWRITE).
const (
	fRead  = 0x0001
	fWrite = 0x0002
)

func init() {
	openPort = darwinOpen
	closeFD = func(fd uintptr) error { return unix.Close(int(fd)) }
	getAttr = darwinGetAttr
	setAttr = darwinSetAttr
	setSpeed = darwinSetSpeed
	getLines = darwinGetLines
	bisLines = func(fd uintptr, l Lines) error { return darwinModemBits(fd, unix.TIOCMBIS, l) }
	bicLines = func(fd uintptr, l Lines) error { return darwinModemBits(fd, unix.TIOCMBIC, l) }
	flushIO = func(fd uintptr) error { return unix.IoctlSetPointerInt(int(fd), unix.TIOCFLUSH, fRead|fWrite) }
	newFile = func(fd uintptr, name string) *os.File { return os.NewFile(fd, name) }
	loopback = darwinLoopback
	listNames = darwinList
}

// darwinOpen opens a tty node without becoming its controlling terminal and
// without waiting for carrier detect.
//
// O_NONBLOCK is not optional here. On a /dev/tty.* node a blocking open waits
// for DCD, and a USB CDC device that never drives DCD makes that wait
// permanent; the process is then stuck in open(2) with no error to report. The
// descriptor is left non-blocking afterwards so the Go poller adopts it and
// read deadlines work.
func darwinOpen(name string, blocking bool) (uintptr, error) {
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: name, Err: err}
	}
	if blocking {
		if err := unix.SetNonblock(fd, false); err != nil {
			_ = unix.Close(fd)
			return 0, &os.PathError{Op: "fcntl", Path: name, Err: err}
		}
	}
	return uintptr(fd), nil
}

// darwinGetAttr is tcgetattr.
func darwinGetAttr(fd uintptr) (Termios, error) {
	t, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	if err != nil {
		return Termios{}, err
	}
	return fromUnix(t), nil
}

// darwinSetAttr is tcsetattr with TCSANOW: apply immediately, do not wait for
// the output queue to drain. Waiting would be TIOCSETAW, and on a port whose
// device never reads, that wait does not end.
func darwinSetAttr(fd uintptr, t Termios) error {
	u := toUnix(t)
	return unix.IoctlSetTermios(int(fd), unix.TIOCSETA, &u)
}

// darwinSetSpeed insists on an exact line rate through IOSSIOSPEED.
func darwinSetSpeed(fd uintptr, baud int) error {
	speed := uint64(baud)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, IOSSIOSPEED, uintptr(unsafe.Pointer(&speed)))
	if errno != 0 {
		return errno
	}
	return nil
}

// darwinGetLines is TIOCMGET.
func darwinGetLines(fd uintptr) (Lines, error) {
	v, err := unix.IoctlGetInt(int(fd), unix.TIOCMGET)
	if err != nil {
		return 0, err
	}
	return Lines(uint32(v)), nil
}

// darwinModemBits is TIOCMBIS or TIOCMBIC: set or clear the given lines
// without disturbing the others.
func darwinModemBits(fd uintptr, req uint, l Lines) error {
	return unix.IoctlSetPointerInt(int(fd), req, int(l))
}

// fromUnix and toUnix copy field by field rather than casting the pointer.
// The layouts are identical -- serial_darwin_test.go asserts it -- but copying
// means a future divergence in x/sys is a compile error instead of silently
// misread memory.
func fromUnix(u *unix.Termios) Termios {
	t := Termios{
		Iflag:  uint64(u.Iflag),
		Oflag:  uint64(u.Oflag),
		Cflag:  uint64(u.Cflag),
		Lflag:  uint64(u.Lflag),
		Ispeed: uint64(u.Ispeed),
		Ospeed: uint64(u.Ospeed),
	}
	copy(t.Cc[:], u.Cc[:])
	return t
}

func toUnix(t Termios) unix.Termios {
	u := unix.Termios{
		Iflag:  t.Iflag,
		Oflag:  t.Oflag,
		Cflag:  t.Cflag,
		Lflag:  t.Lflag,
		Ispeed: t.Ispeed,
		Ospeed: t.Ospeed,
	}
	copy(u.Cc[:], t.Cc[:])
	return u
}

// darwinList names the entries of /dev.
func darwinList(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out, nil
}

// darwinLoopback allocates a pseudo-terminal pair by hand: /dev/ptmx, then the
// three BSD ioctls posix_openpt/grantpt/unlockpt/ptsname are made of. Doing it
// here rather than linking a pty library keeps the control instrument inside
// the package it is meant to validate.
func darwinLoopback() (*os.File, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, "", &os.PathError{Op: "open", Path: "/dev/ptmx", Err: err}
	}
	fail := func(op string, err error) (*os.File, string, error) {
		_ = unix.Close(fd)
		return nil, "", &os.PathError{Op: op, Path: "/dev/ptmx", Err: err}
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		return fail("grantpt", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		return fail("unlockpt", err)
	}
	name, err := ptsname(fd)
	if err != nil {
		return fail("ptsname", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		return fail("fcntl", err)
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), name, nil
}

// ptsname asks TIOCPTYGNAME for the slave path. The ioctl writes a
// NUL-terminated string into a fixed 128-byte buffer, which is the size the
// macOS header declares.
func ptsname(fd int) (string, error) {
	var buf [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n]), nil
}
