//go:build !windows

package clock

import "time"

var monoStart = time.Now()

// monoSeconds reads Go's monotonic clock (mach_absolute_time on macOS).
func monoSeconds() float64 { return time.Since(monoStart).Seconds() }
