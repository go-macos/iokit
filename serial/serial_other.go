//go:build !darwin

package serial

import "os"

// On every non-darwin platform the seams answer [ErrUnsupported] rather than
// being nil, so a consumer that compiles for Linux or Windows gets a clean
// error from the same API instead of a nil-func panic. The portable half of the
// package -- [Config], [Termios], [BaseName], [List]'s filtering, [Drain] --
// works everywhere and is tested everywhere; only the system calls are missing.
func init() {
	openPort = func(string, bool) (uintptr, error) { return 0, ErrUnsupported }
	closeFD = func(uintptr) error { return ErrUnsupported }
	getAttr = func(uintptr) (Termios, error) { return Termios{}, ErrUnsupported }
	setAttr = func(uintptr, Termios) error { return ErrUnsupported }
	setSpeed = func(uintptr, int) error { return ErrUnsupported }
	getLines = func(uintptr) (Lines, error) { return 0, ErrUnsupported }
	bisLines = func(uintptr, Lines) error { return ErrUnsupported }
	bicLines = func(uintptr, Lines) error { return ErrUnsupported }
	flushIO = func(uintptr) error { return ErrUnsupported }
	newFile = func(uintptr, string) *os.File { return nil }
	loopback = func() (*os.File, string, error) { return nil, "", ErrUnsupported }
	listNames = func(dir string) ([]string, error) {
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
}
