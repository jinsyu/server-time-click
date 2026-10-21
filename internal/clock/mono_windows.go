//go:build windows

package clock

import (
	"syscall"
	"unsafe"
)

// Go's own monotonic clock on Windows follows the interrupt timer (1-16ms
// steps), too coarse here, so read QueryPerformanceCounter directly.
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	procQPC  = kernel32.NewProc("QueryPerformanceCounter")
	qpcFreq  = func() float64 {
		var f int64
		kernel32.NewProc("QueryPerformanceFrequency").Call(uintptr(unsafe.Pointer(&f)))
		return float64(f)
	}()
	qpcStart = qpc()
)

func qpc() int64 {
	var n int64
	procQPC.Call(uintptr(unsafe.Pointer(&n)))
	return n
}

func monoSeconds() float64 { return float64(qpc()-qpcStart) / qpcFreq }
