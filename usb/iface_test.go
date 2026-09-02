package usb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-macos/iokit/ioreturn"
)

func TestPipeDirString(t *testing.T) {
	for _, tc := range []struct {
		d    PipeDir
		want string
	}{{PipeOut, "out"}, {PipeIn, "in"}, {PipeAny, "any"}, {PipeDir(9), "dir(9)"}} {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("PipeDir(%d) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestPipeAddress(t *testing.T) {
	for _, tc := range []struct {
		p    Pipe
		want byte
	}{
		{Pipe{Number: 3, Dir: PipeOut}, 0x03},
		{Pipe{Number: 3, Dir: PipeIn}, 0x83},
		{Pipe{Number: 5, Dir: PipeIn}, 0x85},
		{Pipe{Number: 0x1F, Dir: PipeOut}, 0x0F}, // only the low nibble is the number
	} {
		if got := tc.p.Address(); got != tc.want {
			t.Errorf("%+v Address = %#02x, want %#02x", tc.p, got, tc.want)
		}
	}
}

func TestPipeString(t *testing.T) {
	p := Pipe{Ref: 2, Number: 3, Dir: PipeIn, Type: TransferBulk, MaxPacket: 512}
	want := "pipe 2: ep 0x83 in bulk max=512 interval=0"
	if got := p.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestInterfaceInfoString(t *testing.T) {
	i := InterfaceInfo{VendorID: 0x35CA, ProductID: 0x1201, LocationID: 0x02130000, Number: 1, Class: 10, Endpoints: 2}
	want := "35ca:1201 loc=0x02130000 if1 alt0 class=10/0/0 2 endpoint(s)"
	if got := i.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestInterfaceFilterMatch(t *testing.T) {
	info := InterfaceInfo{VendorID: 0x35CA, ProductID: 0x1201, LocationID: 0x02130000, Number: 1}
	for name, tc := range map[string]struct {
		f    InterfaceFilter
		want bool
	}{
		"empty":         {InterfaceFilter{}, true},
		"vendor ok":     {InterfaceFilter{VendorID: 0x35CA}, true},
		"vendor wrong":  {InterfaceFilter{VendorID: 0x1234}, false},
		"product ok":    {InterfaceFilter{ProductIDs: []uint16{0x1102, 0x1201}}, true},
		"product wrong": {InterfaceFilter{ProductIDs: []uint16{0x1102}}, false},
		"location ok":   {InterfaceFilter{LocationID: 0x02130000}, true},
		"location bad":  {InterfaceFilter{LocationID: 1}, false},
		"number ok":     {InterfaceFilter{Numbers: []uint8{0, 1}}, true},
		"number wrong":  {InterfaceFilter{Numbers: []uint8{5}}, false},
	} {
		if got := tc.f.Match(info); got != tc.want {
			t.Errorf("%s: Match = %v, want %v", name, got, tc.want)
		}
	}
}

// ifaceFakes swaps the interface seams for substitutes and restores them.
type ifaceFakes struct {
	infos     []InterfaceInfo
	refs      []uintptr
	enumErr   error
	openCode  ioreturn.Code
	seizeCode ioreturn.Code
	closeCode ioreturn.Code
	pipes     []Pipe
	pipesCode ioreturn.Code
	readN     int
	readCode  ioreturn.Code
	writeN    int
	writeCode ioreturn.Code
	released  []uintptr
	seizeSeen bool
}

func installIfaceFakes(t *testing.T, f *ifaceFakes) {
	t.Helper()
	old := [7]any{enumerateIfaces, openIface, closeIface, ifacePipes, pipeRead, pipeWrite, releaseIface}
	t.Cleanup(func() {
		enumerateIfaces = old[0].(func() ([]InterfaceInfo, []uintptr, error))
		openIface = old[1].(func(uintptr, bool) ioreturn.Code)
		closeIface = old[2].(func(uintptr) ioreturn.Code)
		ifacePipes = old[3].(func(uintptr) ([]Pipe, ioreturn.Code))
		pipeRead = old[4].(func(uintptr, uint8, []byte, time.Duration) (int, string, ioreturn.Code))
		pipeWrite = old[5].(func(uintptr, uint8, []byte, time.Duration) (int, ioreturn.Code))
		releaseIface = old[6].(func(uintptr))
	})
	enumerateIfaces = func() ([]InterfaceInfo, []uintptr, error) { return f.infos, f.refs, f.enumErr }
	openIface = func(_ uintptr, seize bool) ioreturn.Code {
		if seize {
			f.seizeSeen = true
			return f.seizeCode
		}
		return f.openCode
	}
	closeIface = func(uintptr) ioreturn.Code { return f.closeCode }
	ifacePipes = func(uintptr) ([]Pipe, ioreturn.Code) { return f.pipes, f.pipesCode }
	pipeRead = func(uintptr, uint8, []byte, time.Duration) (int, string, ioreturn.Code) {
		return f.readN, "ReadPipeTO", f.readCode
	}
	pipeWrite = func(uintptr, uint8, []byte, time.Duration) (int, ioreturn.Code) { return f.writeN, f.writeCode }
	releaseIface = func(tok uintptr) { f.released = append(f.released, tok) }
}

func TestInterfacesFiltersAndReleasesTheRest(t *testing.T) {
	f := &ifaceFakes{
		infos: []InterfaceInfo{
			{VendorID: 0x35CA, Number: 0},
			{VendorID: 0x35CA, Number: 1},
			{VendorID: 0x1234, Number: 1},
		},
		refs: []uintptr{10, 11, 12},
	}
	installIfaceFakes(t, f)

	got, err := Interfaces(InterfaceFilter{VendorID: 0x35CA, Numbers: []uint8{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Info().Number != 1 {
		t.Fatalf("Interfaces returned %d interface(s)", len(got))
	}
	// Everything filtered out must be released, or every listing leaks a
	// kernel reference.
	if len(f.released) != 2 {
		t.Errorf("released %v, want the two non-matching refs", f.released)
	}
	if got[0].String() == "" {
		t.Error("String should describe the interface")
	}
	if got[0].Seized() {
		t.Error("a fresh handle is not seized")
	}
}

func TestInterfacesPropagatesEnumerationErrors(t *testing.T) {
	sentinel := errors.New("no IOKit here")
	installIfaceFakes(t, &ifaceFakes{enumErr: sentinel})
	if _, err := Interfaces(InterfaceFilter{}); !errors.Is(err, sentinel) {
		t.Errorf("Interfaces: %v", err)
	}
}

func TestInterfaceOpenAndClose(t *testing.T) {
	f := &ifaceFakes{infos: []InterfaceInfo{{}}, refs: []uintptr{1}}
	installIfaceFakes(t, f)
	ifs, err := Interfaces(InterfaceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	i := ifs[0]
	if err := i.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := i.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := i.Close(); err != nil {
		t.Errorf("closing twice should be harmless: %v", err)
	}
	// Every method must answer rather than dereference a released handle.
	if err := i.Open(); !errors.Is(err, ErrReleased) {
		t.Errorf("Open after Close: %v", err)
	}
	if _, err := i.Pipes(); !errors.Is(err, ErrReleased) {
		t.Errorf("Pipes after Close: %v", err)
	}
	if _, err := i.Pipe(0x83); !errors.Is(err, ErrReleased) {
		t.Errorf("Pipe after Close: %v", err)
	}
	if _, err := i.Read(1, make([]byte, 1), time.Second); !errors.Is(err, ErrReleased) {
		t.Errorf("Read after Close: %v", err)
	}
	if _, err := i.Write(1, []byte{1}, time.Second); !errors.Is(err, ErrReleased) {
		t.Errorf("Write after Close: %v", err)
	}
}

// TestInterfaceOpenRefusalIsAResult checks that a kernel driver holding the
// interface comes back as a decodable IOError rather than a bare failure. That
// code is the finding when a device's protocol is reachable but spoken for.
func TestInterfaceOpenRefusalIsAResult(t *testing.T) {
	f := &ifaceFakes{
		infos: []InterfaceInfo{{}}, refs: []uintptr{1},
		openCode: ioreturn.ExclusiveAccess, seizeCode: ioreturn.Success,
	}
	installIfaceFakes(t, f)
	ifs, _ := Interfaces(InterfaceFilter{})
	i := ifs[0]
	defer i.Close()

	err := i.Open()
	var ioe *IOError
	if !errors.As(err, &ioe) || ioe.Code != ioreturn.ExclusiveAccess {
		t.Fatalf("Open = %v, want kIOReturnExclusiveAccess", err)
	}
	if ioe.Op != "USBInterfaceOpen" {
		t.Errorf("Op = %q", ioe.Op)
	}
	if err := i.OpenSeize(); err != nil {
		t.Fatalf("OpenSeize: %v", err)
	}
	if !f.seizeSeen {
		t.Error("OpenSeize did not ask for a seize")
	}
	if !i.Seized() {
		t.Error("Seized should report a handle taken from its previous owner")
	}
}

func TestInterfaceCloseReportsTheKernelsCode(t *testing.T) {
	f := &ifaceFakes{infos: []InterfaceInfo{{}}, refs: []uintptr{1}, closeCode: ioreturn.NotOpen}
	installIfaceFakes(t, f)
	ifs, _ := Interfaces(InterfaceFilter{})
	i := ifs[0]
	if err := i.Open(); err != nil {
		t.Fatal(err)
	}
	var ioe *IOError
	if err := i.Close(); !errors.As(err, &ioe) || ioe.Code != ioreturn.NotOpen {
		t.Errorf("Close = %v", err)
	}
}

func TestInterfacePipes(t *testing.T) {
	f := &ifaceFakes{
		infos: []InterfaceInfo{{Number: 1}}, refs: []uintptr{1},
		pipes: []Pipe{
			{Ref: 1, Number: 3, Dir: PipeOut, Type: TransferBulk, MaxPacket: 512},
			{Ref: 2, Number: 3, Dir: PipeIn, Type: TransferBulk, MaxPacket: 512},
		},
	}
	installIfaceFakes(t, f)
	ifs, _ := Interfaces(InterfaceFilter{})
	i := ifs[0]
	defer i.Close()

	ps, err := i.Pipes()
	if err != nil || len(ps) != 2 {
		t.Fatalf("Pipes = %v, %v", ps, err)
	}
	in, err := i.Pipe(0x83)
	if err != nil || in.Ref != 2 {
		t.Fatalf("Pipe(0x83) = %+v, %v", in, err)
	}
	if _, err := i.Pipe(0x81); err == nil {
		t.Error("an endpoint the interface does not have should be an error")
	}

	f.pipesCode = ioreturn.NotOpen
	if _, err := i.Pipes(); err == nil {
		t.Error("Pipes should report the kernel's refusal")
	}
	if _, err := i.Pipe(0x83); err == nil {
		t.Error("Pipe should propagate the Pipes failure")
	}
}

// TestPipeIOReportsCountsAndCodes checks the shape that matters most: a read
// that timed out reports its code, and a byte count is only ever paired with
// success.
func TestPipeIOReportsCountsAndCodes(t *testing.T) {
	f := &ifaceFakes{
		infos: []InterfaceInfo{{}}, refs: []uintptr{1},
		readN: 8, readCode: ioreturn.Success,
		writeN: 20, writeCode: ioreturn.Success,
	}
	installIfaceFakes(t, f)
	ifs, _ := Interfaces(InterfaceFilter{})
	i := ifs[0]
	defer i.Close()

	n, err := i.Read(2, make([]byte, 64), time.Second)
	if err != nil || n != 8 {
		t.Fatalf("Read = %d, %v", n, err)
	}
	n, err = i.Write(1, make([]byte, 20), time.Second)
	if err != nil || n != 20 {
		t.Fatalf("Write = %d, %v", n, err)
	}

	f.readN, f.readCode = 0, ioreturn.USBTransactionTimeout
	n, err = i.Read(2, make([]byte, 64), time.Second)
	var ioe *IOError
	if n != 0 || !errors.As(err, &ioe) || ioe.Code != ioreturn.USBTransactionTimeout {
		t.Fatalf("a NAKed pipe gave %d, %v", n, err)
	}
	f.writeCode = ioreturn.USBPipeStalled
	if _, err := i.Write(1, []byte{1}, time.Second); !errors.As(err, &ioe) || !ioe.Stalled() {
		t.Fatalf("a stalled write gave %v", err)
	}
}

// TestAReadErrorNamesTheCallItMade. IOUSBLib has two read calls and they take
// different pipes: ReadPipeTO is BULK ONLY and answers kIOReturnBadArgument for
// an interrupt pipe, while ReadPipe takes any pipe and has no timeout of its
// own. An error naming the wrong one sends the next reader to the wrong
// documentation -- and this cost an afternoon, because a probe that ignored the
// error read an XR headset's two interrupt endpoints and reported them silent
// when the requests had never left the machine.
func TestAReadErrorNamesTheCallItMade(t *testing.T) {
	for _, op := range []string{"ReadPipe", "ReadPipeTO"} {
		t.Run(op, func(t *testing.T) {
			installIfaceFakes(t, &ifaceFakes{})
			pipeRead = func(uintptr, uint8, []byte, time.Duration) (int, string, ioreturn.Code) {
				return 0, op, ioreturn.USBTransactionTimeout
			}
			h := &InterfaceHandle{ref: 1}
			_, err := h.Read(1, make([]byte, 8), time.Millisecond)
			if err == nil {
				t.Fatalf("a timeout was reported as success")
			}
			if !strings.Contains(err.Error(), op) {
				t.Errorf("the error says %q, which does not name %s", err, op)
			}
		})
	}
}
