// Package ioreturn names the IOKit IOReturn codes and turns one into a
// sentence a human can act on.
//
// It exists so that every IOKit binding in this module -- HID, USB, and
// whatever comes next -- decodes a failure from the same table instead of
// keeping its own copy of the handful of hex constants it happened to hit.
// The values here were not transcribed from documentation: they were printed
// by a C program that includes <IOKit/IOReturn.h> and <IOKit/usb/USB.h>, which
// is the only way to be sure a packed system/subsystem/code word is right.
//
// An IOReturn is a packed word: bits 31..26 are the system (0x38 for IOKit),
// bits 25..14 the subsystem (0 for the common codes, 1 for USB), and bits 13..0
// the code. Two different subsystems therefore reuse the same low code for
// unrelated failures, which is why [Name] matches on the whole word.
package ioreturn

import "fmt"

// Code is an IOKit IOReturn value.
type Code int32

// The common IOKit codes, subsystem 0.
const (
	Success         Code = 0
	Err             Code = -536870212 // 0xE00002BC kIOReturnError
	NoMemory        Code = -536870211 // 0xE00002BD
	NoResources     Code = -536870210 // 0xE00002BE
	IPCError        Code = -536870209 // 0xE00002BF
	NoDevice        Code = -536870208 // 0xE00002C0
	NotPrivileged   Code = -536870207 // 0xE00002C1
	BadArgument     Code = -536870206 // 0xE00002C2
	LockedRead      Code = -536870205 // 0xE00002C3
	LockedWrite     Code = -536870204 // 0xE00002C4
	ExclusiveAccess Code = -536870203 // 0xE00002C5
	BadMessageID    Code = -536870202 // 0xE00002C6
	Unsupported     Code = -536870201 // 0xE00002C7
	VMError         Code = -536870200 // 0xE00002C8
	InternalError   Code = -536870199 // 0xE00002C9
	IOError         Code = -536870198 // 0xE00002CA
	NotOpen         Code = -536870195 // 0xE00002CD
	NotReadable     Code = -536870194 // 0xE00002CE
	NotWritable     Code = -536870193 // 0xE00002CF
	Busy            Code = -536870187 // 0xE00002D5
	Timeout         Code = -536870186 // 0xE00002D6
	Offline         Code = -536870185 // 0xE00002D7
	NotAttached     Code = -536870183 // 0xE00002D9
	NoInterrupt     Code = -536870177 // 0xE00002DF
	NoFrames        Code = -536870176 // 0xE00002E0
	NotPermitted    Code = -536870174 // 0xE00002E2
	NoPower         Code = -536870173 // 0xE00002E3
	Aborted         Code = -536870165 // 0xE00002EB
	NotResponding   Code = -536870163 // 0xE00002ED
	Invalid         Code = -536870911 // 0xE0000001
)

// The USB codes, subsystem 1. A control transfer that the device refuses
// answers with [USBPipeStalled]; one it simply never answers with
// [USBTransactionTimeout]. Telling those two apart is the whole point of
// probing a protocol nobody documented.
const (
	USBDevicePortWasNotSuspended  Code = -536854457 // 0xE0004047
	USBClearPipeStallNotRecursive Code = -536854456 // 0xE0004048
	USBDeviceNotHighSpeed         Code = -536854455 // 0xE0004049
	USBInterfaceNotFound          Code = -536854450 // 0xE000404E
	USBPipeStalled                Code = -536854449 // 0xE000404F
	USBTransactionReturned        Code = -536854448 // 0xE0004050
	USBTransactionTimeout         Code = -536854447 // 0xE0004051
	USBConfigNotFound             Code = -536854442 // 0xE0004056
	USBEndpointNotFound           Code = -536854441 // 0xE0004057
	USBNoAsyncPortErr             Code = -536854433 // 0xE000405F
	USBTooManyPipesErr            Code = -536854432 // 0xE0004060
	USBUnknownPipeErr             Code = -536854431 // 0xE0004061
)

// names is the whole table, keyed by the packed word.
var names = map[Code]string{
	Success:                       "kIOReturnSuccess",
	Err:                           "kIOReturnError",
	NoMemory:                      "kIOReturnNoMemory",
	NoResources:                   "kIOReturnNoResources",
	IPCError:                      "kIOReturnIPCError",
	NoDevice:                      "kIOReturnNoDevice",
	NotPrivileged:                 "kIOReturnNotPrivileged",
	BadArgument:                   "kIOReturnBadArgument",
	LockedRead:                    "kIOReturnLockedRead",
	LockedWrite:                   "kIOReturnLockedWrite",
	ExclusiveAccess:               "kIOReturnExclusiveAccess",
	BadMessageID:                  "kIOReturnBadMessageID",
	Unsupported:                   "kIOReturnUnsupported",
	VMError:                       "kIOReturnVMError",
	InternalError:                 "kIOReturnInternalError",
	IOError:                       "kIOReturnIOError",
	NotOpen:                       "kIOReturnNotOpen",
	NotReadable:                   "kIOReturnNotReadable",
	NotWritable:                   "kIOReturnNotWritable",
	Busy:                          "kIOReturnBusy",
	Timeout:                       "kIOReturnTimeout",
	Offline:                       "kIOReturnOffline",
	NotAttached:                   "kIOReturnNotAttached",
	NoInterrupt:                   "kIOReturnNoInterrupt",
	NoFrames:                      "kIOReturnNoFrames",
	NotPermitted:                  "kIOReturnNotPermitted",
	NoPower:                       "kIOReturnNoPower",
	Aborted:                       "kIOReturnAborted",
	NotResponding:                 "kIOReturnNotResponding",
	Invalid:                       "kIOReturnInvalid",
	USBDevicePortWasNotSuspended:  "kIOUSBDevicePortWasNotSuspended",
	USBClearPipeStallNotRecursive: "kIOUSBClearPipeStallNotRecursive",
	USBDeviceNotHighSpeed:         "kIOUSBDeviceNotHighSpeed",
	USBInterfaceNotFound:          "kIOUSBInterfaceNotFound",
	USBPipeStalled:                "kIOUSBPipeStalled",
	USBTransactionReturned:        "kIOUSBTransactionReturned",
	USBTransactionTimeout:         "kIOUSBTransactionTimeout",
	USBConfigNotFound:             "kIOUSBConfigNotFound",
	USBEndpointNotFound:           "kIOUSBEndpointNotFound",
	USBNoAsyncPortErr:             "kIOUSBNoAsyncPortErr",
	USBTooManyPipesErr:            "kIOUSBTooManyPipesErr",
	USBUnknownPipeErr:             "kIOUSBUnknownPipeErr",
}

// meanings explains the codes a caller can actually do something about. A code
// absent from this map renders as its name alone.
var meanings = map[Code]string{
	NoDevice:               "the device was unplugged",
	NotPrivileged:          "the process lacks the privilege; an entitlement or root may be required",
	ExclusiveAccess:        "another client -- usually a kernel driver -- holds the device open",
	Unsupported:            "the object does not implement this operation",
	NotOpen:                "the device must be opened first",
	NotPermitted:           "macOS denied the operation; a driver has claimed it, or user consent is missing",
	NotResponding:          "the device did not answer",
	Timeout:                "the operation ran out of time",
	BadArgument:            "a parameter was rejected before it reached the device",
	USBPipeStalled:         "the device STALLed the request: it understood the packet and refused it",
	USBTransactionTimeout:  "the device never answered the request",
	USBTransactionReturned: "the transaction was returned before completing",
	USBEndpointNotFound:    "no such endpoint on this device",
	USBConfigNotFound:      "no such configuration on this device",
	USBUnknownPipeErr:      "the pipe reference is not one this interface owns",
}

// Name returns the kIOReturn... identifier for c, or a hex rendering when the
// code is not one of the named ones. IOReturn is a namespaced word, so an
// unknown value is not a bug: a family this package knows nothing about is free
// to define its own.
func Name(c Code) string {
	if n, ok := names[c]; ok {
		return n
	}
	return fmt.Sprintf("IOReturn(0x%08x)", uint32(c))
}

// Describe renders c as its name plus, when there is one, a sentence saying
// what a caller should conclude. The result never ends in punctuation, so it
// composes into a larger error message.
func Describe(c Code) string {
	name := Name(c)
	if m, ok := meanings[c]; ok {
		return name + ": " + m
	}
	return name
}

// String makes Code an fmt.Stringer, rendering the same text as [Describe]
// together with the raw word, because the raw word is what a bug report needs.
func (c Code) String() string {
	return fmt.Sprintf("0x%08x %s", uint32(c), Describe(c))
}

// IsSuccess reports whether c is kIOReturnSuccess. IOKit reserves zero for
// success across every subsystem.
func IsSuccess(c Code) bool { return c == Success }
