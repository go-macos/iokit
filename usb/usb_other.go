//go:build !darwin

package usb

import (
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

// On every non-darwin platform the seams answer [ErrUnsupported] rather than
// being nil, so a consumer that compiles for Linux or Windows gets a clean
// error from the same API instead of a nil-func panic. The seams that return
// only an IOReturn have no error channel of their own, so they report
// kIOReturnUnsupported, which [IOError] renders as an unimplemented operation.
func init() {
	enumerate = func() ([]Info, []uintptr, error) { return nil, nil, ErrUnsupported }
	openDev = func(uintptr, bool) ioreturn.Code { return ioreturn.Unsupported }
	closeDev = func(uintptr) ioreturn.Code { return ioreturn.Unsupported }
	control = func(uintptr, Setup, []byte, time.Duration) (int, ioreturn.Code) {
		return 0, ioreturn.Unsupported
	}
	configDesc = func(uintptr, byte) ([]byte, ioreturn.Code) { return nil, ioreturn.Unsupported }
	releaseRef = func(uintptr) {}

	enumerateIfaces = func() ([]InterfaceInfo, []uintptr, error) { return nil, nil, ErrUnsupported }
	openIface = func(uintptr, bool) ioreturn.Code { return ioreturn.Unsupported }
	closeIface = func(uintptr) ioreturn.Code { return ioreturn.Unsupported }
	ifacePipes = func(uintptr) ([]Pipe, ioreturn.Code) { return nil, ioreturn.Unsupported }
	pipeRead = func(uintptr, uint8, []byte, time.Duration) (int, string, ioreturn.Code) {
		return 0, "ReadPipe", ioreturn.Unsupported
	}
	pipeWrite = func(uintptr, uint8, []byte, time.Duration) (int, ioreturn.Code) {
		return 0, ioreturn.Unsupported
	}
	releaseIface = func(uintptr) {}
}
