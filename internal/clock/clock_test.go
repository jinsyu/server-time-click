package clock

import (
	"math"
	"os"
	"testing"
	"time"
)

// simulate feeds apply() with replies from a fake server whose clock is
// local + trueOffset, firing each probe the way Sample does.
func simulate(t *testing.T, trueOffset, rtt float64, rounds int) *Clock {
	t.Helper()
	c := New("https://example.invalid/")
	local := Now()
	for i := 0; i < rounds; i++ {
		if c.Valid() {
			c.mu.Lock()
			lo, hi := c.lo, c.hi
			c.mu.Unlock()
			local = fireAt(local, (lo+hi)/2, c.Rtt())
		}
		t0, t1 := local, local+rtt
		serverAtMid := t0 + rtt/2 + trueOffset
		if got := c.apply(t0, t1, math.Floor(serverAtMid)); got != "ok" {
			t.Fatalf("round %d: status %q", i, got)
		}
		local = t1 + 0.3
	}
	return c
}

func TestBinarySearchConverges(t *testing.T) {
	for _, off := range []float64{-3.71, -0.5, 0, 0.0004, 0.4321, 2.999, 117.25} {
		c := simulate(t, off, 0.02, 12)
		c.mu.Lock()
		lo, hi := c.lo, c.hi
		c.mu.Unlock()
		if off < lo || off > hi {
			t.Errorf("offset %v outside [%v, %v]", off, lo, hi)
		}
		if w := (hi - lo) / 2; w > 0.02 {
			t.Errorf("offset %v: error ±%.1fms after 12 rounds, want ≤ rtt", off, w*1000)
		}
	}
}

func TestOddReplyIgnoredThenReset(t *testing.T) {
	c := simulate(t, 0.25, 0.02, 12)
	before := c.Offset()
	bad := func() string { return c.apply(100, 100.02, 105) } // 5s off
	if s := bad(); s != "conflict" {
		t.Fatalf("first odd reply: %q", s)
	}
	if s := bad(); s != "conflict" {
		t.Fatalf("second odd reply: %q", s)
	}
	if math.Abs(c.Offset()-before) > 1e-3 {
		t.Fatalf("odd replies moved the offset")
	}
	if s := bad(); s != "reset" {
		t.Fatalf("third odd reply: %q", s)
	}
}

func TestSleepJumpWidensInterval(t *testing.T) {
	c := simulate(t, 0.25, 0.02, 12)
	saved := wallNow
	defer func() { wallNow = saved }()
	wallNow = func() float64 { return saved() + 30 } // wall moved 30s, counter did not
	lo, hi := c.Bounds()
	if !(lo <= 0.25 && hi >= 30.25 && lo > 0.2 && hi < 30.3) {
		t.Fatalf("after jump [%v, %v] should cover both 0.25 and 30.25", lo, hi)
	}
}

func TestParseTOD(t *testing.T) {
	ok := map[string]float64{"16:00": 57600, "16:00:00": 57600, "9:05:30": 32730, "23:59:59.5": 86399.5, " 00:00 ": 0}
	for in, want := range ok {
		if got, err := ParseTOD(in); err != nil || got != want {
			t.Errorf("ParseTOD(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "16", "24:00", "12:60", "12:00:60", "ab:cd", "1:2:3:4"} {
		if _, err := ParseTOD(in); err == nil {
			t.Errorf("ParseTOD(%q) should fail", in)
		}
	}
}

func TestNextKST(t *testing.T) {
	// 2026-10-21 14:30:00 KST
	now := float64(time.Date(2026, 10, 21, 14, 30, 0, 0, KST).Unix())
	if got, want := NextKST(16*3600, now), float64(time.Date(2026, 10, 21, 16, 0, 0, 0, KST).Unix()); got != want {
		t.Errorf("later today: got %v want %v", got, want)
	}
	if got, want := NextKST(14*3600, now), float64(time.Date(2026, 10, 22, 14, 0, 0, 0, KST).Unix()); got != want {
		t.Errorf("already passed: got %v want %v", got, want)
	}
}

func TestFmtKST(t *testing.T) {
	e := float64(time.Date(2026, 10, 21, 16, 0, 0, 0, KST).Unix()) + 0.4229
	if got := FmtKST(e, 3); got != "16:00:00.422" {
		t.Errorf("got %q", got)
	}
	if got := FmtKST(e, 0); got != "16:00:00" {
		t.Errorf("got %q", got)
	}
}

func TestMonotonicResolution(t *testing.T) {
	// The click spins on Now(); it must tick far finer than a millisecond.
	a := Now()
	b := a
	for b == a {
		b = Now()
	}
	if d := b - a; d > 100e-6 {
		t.Errorf("Now() steps by %.3fms, want < 0.1ms", d*1000)
	}
	// and track wall time
	if d := math.Abs(Now() - float64(time.Now().UnixNano())/1e9); d > 0.05 {
		t.Errorf("Now() is %.3fs away from wall time", d)
	}
}

func TestSleepUntilPrecision(t *testing.T) {
	if testing.Short() {
		t.Skip("timing on shared CI machines is noisy")
	}
	for i := 0; i < 5; i++ {
		target := Now() + 0.1
		SleepUntil(target)
		if late := Now() - target; late > 0.001 {
			t.Errorf("SleepUntil woke %.2fms late", late*1000)
		}
	}
}

// Live check against the real server; skip with -short or SERVERTIME_OFFLINE=1.
func TestLiveSync(t *testing.T) {
	if testing.Short() || os.Getenv("SERVERTIME_OFFLINE") != "" {
		t.Skip("offline")
	}
	c := New(DefaultURL)
	if !c.InitialSync(12, func(s string) { t.Log(s) }) {
		t.Fatal("no usable sample")
	}
	t.Logf("offset %+.4fs ±%.1fms rtt %.1fms", c.Offset(), c.Error()*1000, c.Rtt()*1000)
	if e := c.Error(); e > math.Max(0.1, c.Rtt()*2) {
		t.Errorf("error ±%.0fms too wide", e*1000)
	}
}
