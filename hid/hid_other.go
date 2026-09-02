//go:build !darwin

package hid

import "context"

// On every non-darwin platform the seams answer [ErrUnsupported] rather than
// being nil, so a consumer that compiles for Linux or Windows gets a clean
// error from the same API instead of a nil-func panic. The IOReturn-returning
// seams have no error channel of their own, so they report the internal-error
// code, which [IOError] renders as a framework-load failure.
func init() {
	enumerate = func(Filter) ([]Info, []uintptr, error) { return nil, nil, ErrUnsupported }
	openDev = func(uintptr) int32 { return ioReturnInternalError }
	closeDev = func(uintptr) int32 { return ioReturnInternalError }
	setReport = func(uintptr, uint32, int64, []byte) int32 { return ioReturnInternalError }
	stream = func(context.Context, []uintptr, []int, func(int, []byte)) error { return ErrUnsupported }
	releaseRef = func(uintptr) {}
}
