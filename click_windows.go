//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// A window running as administrator ignores input from a normal process, so
// run this at the same privilege level as the browser.
var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procSendInput                = user32.NewProc("SendInput")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procMessageBeep              = user32.NewProc("MessageBeep")
	procSetThreadExecutionState  = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	procTimeBeginPeriod          = syscall.NewLazyDLL("winmm.dll").NewProc("timeBeginPeriod")
)

const (
	inputMouse          = 0
	mouseeventfLeftDown = 0x0002
	mouseeventfLeftUp   = 0x0004
)

// INPUT with the MOUSEINPUT member, laid out as on 64-bit Windows (40 bytes).
type mouseInput struct {
	typ       uint32
	_         uint32
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	_         uint32
	extraInfo uintptr
}

func platformInit() {
	procTimeBeginPeriod.Call(1) // 1ms timer ticks for the sleeps before the spin
	// ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED: no sleep while running.
	procSetThreadExecutionState.Call(0x80000000 | 0x00000001 | 0x00000002)
}

func clickSetup() error { return nil }

func leftClick() (float64, float64) {
	var p struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	in := [2]mouseInput{
		{typ: inputMouse, flags: mouseeventfLeftDown},
		{typ: inputMouse, flags: mouseeventfLeftUp},
	}
	procSendInput.Call(2, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	return float64(p.X), float64(p.Y)
}

// ownAppFront reports whether this program's window has focus, in which case
// the click would land here instead of the browser.
func ownAppFront() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return int(pid) == os.Getpid()
}

func beep() { go procMessageBeep.Call(0xFFFFFFFF) }
