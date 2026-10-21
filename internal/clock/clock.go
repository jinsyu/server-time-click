// Package clock estimates a web server's clock from its HTTP Date header.
//
// The header only has whole-second resolution, but every reply says "the
// server's clock read D somewhere between sending (t0) and receiving (t1)", so
// the offset (server - local) lies in [D - t1, D + 1 - t0]. Each request is
// timed to land on the edge of the current guess, which halves the interval
// like a binary search; a dozen requests usually get it under the round-trip
// time. Local time comes from a monotonic high-resolution counter, so an OS
// clock correction mid-run cannot shift the server clock we show.
package clock

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultURL = "https://poticket.interpark.com/"

// Assumed worst-case drift between this computer's counter and the server's
// clock; the offset interval widens by this much per second between samples.
const drift = 50e-6

// If wall time minus monotonic time moves by more than this, the computer
// slept (the counter stops on macOS) or the wall clock was corrected.
const jumpThreshold = 0.5

var (
	KST = time.FixedZone("KST", 9*3600)

	wallNow   = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	epochBase = wallNow() - monoSeconds()
)

// Now is this computer's time in epoch seconds, read from the monotonic
// high-resolution counter.
func Now() float64 { return epochBase + monoSeconds() }

func Seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// SleepUntil sleeps most of the way, then spins the last 20ms for precision.
func SleepUntil(t float64) {
	if left := t - Now(); left > 0.03 {
		time.Sleep(Seconds(left - 0.02))
	}
	for Now() < t {
	}
}

type Clock struct {
	url string

	mu        sync.Mutex
	lo, hi    float64 // offset bounds as of `last`
	last      float64 // local time of the last update
	gap       float64 // wall - monotonic at the last update
	rtts      []float64
	conflicts int
}

func New(u string) *Clock {
	return &Clock{url: u, lo: math.Inf(-1), hi: math.Inf(1), gap: wallNow() - Now()}
}

func (c *Clock) URL() string { return c.url }

// bounds returns the offset interval at local time `at`. Callers hold c.mu.
func (c *Clock) bounds(at float64) (float64, float64) {
	if gap := wallNow() - Now(); math.Abs(gap-c.gap) > jumpThreshold {
		// After sleep the counter lags by the jump; after a wall clock fix
		// nothing changed. Cover both until fresh samples narrow it again.
		d := gap - c.gap
		c.lo, c.hi = math.Min(c.lo, c.lo+d), math.Max(c.hi, c.hi+d)
		c.gap = gap
	}
	grow := drift * math.Max(0, at-c.last)
	return c.lo - grow, c.hi + grow
}

func (c *Clock) Bounds() (float64, float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bounds(Now())
}

// Offset is server time minus local time; NaN before the first sample.
func (c *Clock) Offset() float64 { lo, hi := c.Bounds(); return (lo + hi) / 2 }
func (c *Clock) Error() float64  { lo, hi := c.Bounds(); return (hi - lo) / 2 }

// Valid reports whether the offset is known at all.
func (c *Clock) Valid() bool {
	lo, hi := c.Bounds()
	return !math.IsInf(lo, 0) && !math.IsInf(hi, 0)
}

func (c *Clock) LastRtt() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.rtts) == 0 {
		return math.Inf(1)
	}
	return c.rtts[len(c.rtts)-1]
}

func (c *Clock) Rtt() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.rtts) == 0 {
		return math.Inf(1)
	}
	s := append([]float64(nil), c.rtts...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// fireAt is when to send so the middle of the round trip hits a second
// boundary under the current best guess.
func fireAt(t, guess, rtt float64) float64 {
	at := math.Floor(t+guess) + 1 - guess - rtt/2
	for at < t+0.005 {
		at++
	}
	return at
}

// Sample sends one timed HEAD request on a connection opened beforehand, so
// TCP/TLS setup never delays the send. Returns "ok", "cached", "conflict" or "reset".
func (c *Clock) Sample() (string, error) {
	u, err := url.Parse(c.url)
	if err != nil {
		return "", err
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	conn := raw
	if u.Scheme == "https" {
		tc := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), NextProtos: []string{"http/1.1"}})
		if err := tc.Handshake(); err != nil {
			return "", err
		}
		conn = tc
	}

	path := u.RequestURI()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	path += fmt.Sprintf("%s_=%d", sep, time.Now().UnixNano())
	req := "HEAD " + path + " HTTP/1.1\r\nHost: " + u.Host +
		"\r\nCache-Control: no-cache\r\nPragma: no-cache\r\nUser-Agent: server-time-check/1.0\r\nConnection: close\r\n\r\n"

	if c.Valid() {
		SleepUntil(fireAt(Now(), c.Offset(), c.Rtt()))
	}
	t0 := Now()
	if _, err := conn.Write([]byte(req)); err != nil {
		return "", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "HEAD"})
	t1 := Now()
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	date := resp.Header.Get("Date")
	if resp.Header.Get("Age") != "" || strings.Contains(strings.ToLower(resp.Header.Get("X-Cache")), "hit") || date == "" {
		return "cached", nil
	}
	d, err := http.ParseTime(date)
	if err != nil {
		return "", err
	}
	return c.apply(t0, t1, float64(d.Unix())), nil
}

func (c *Clock) apply(t0, t1, d float64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lo, hi := c.bounds(t1)
	newLo, newHi := math.Max(lo, d-t1), math.Min(hi, d+1-t0)
	status := "ok"
	if newLo > newHi {
		// Disagrees with what we know (a load balancer node with a skewed
		// clock, say). One odd reply is ignored; three in a row mean our
		// interval is wrong, so start over from this sample.
		c.conflicts++
		if c.conflicts < 3 {
			return "conflict"
		}
		newLo, newHi = d-t1, d+1-t0
		status = "reset"
	}
	c.conflicts = 0
	c.rtts = append(c.rtts, t1-t0)
	if len(c.rtts) > 25 {
		c.rtts = c.rtts[1:]
	}
	c.lo, c.hi, c.last = newLo, newHi, t1
	return status
}

// InitialSync collects up to `rounds` usable samples (allowing as many again
// for cached or failed replies) and reports whether the offset is known.
func (c *Clock) InitialSync(rounds int, logf func(string)) bool {
	used := 0
	for i := 0; i < rounds*2 && used < rounds; i++ {
		status, err := c.Sample()
		if err != nil {
			logf("요청 실패: " + err.Error())
			time.Sleep(500 * time.Millisecond)
			continue
		}
		switch status {
		case "cached":
			logf("캐시된 응답이라 버림")
			continue
		case "conflict":
			logf("다른 응답들과 안 맞아서 버림")
			continue
		case "reset":
			logf("응답끼리 안 맞아서 다시 맞춤")
		}
		used++
		logf(fmt.Sprintf("%2d/%d  왕복 %4.0fms  오차 ±%4.0fms", used, rounds, c.LastRtt()*1000, c.Error()*1000))
	}
	return c.Valid()
}

// KeepSyncing samples until stop is closed: every interval, or every second
// while the error is wide (after a reset or a sleep). Nothing is sent while
// quiet(serverNow) is true, so no request competes with the click.
func (c *Clock) KeepSyncing(interval time.Duration, quiet func(float64) bool, stop <-chan struct{}) {
	lastTry := Now()
	for {
		select {
		case <-stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
		wait := interval.Seconds()
		if c.Error() > math.Max(0.03, c.Rtt()) {
			wait = 1
		}
		if Now()-lastTry < wait || quiet(Now()+c.Offset()) {
			continue
		}
		lastTry = Now()
		c.Sample() // a failed probe just means one sample fewer
	}
}

// ParseTOD turns "20:00", "20:00:00" or "20:00:00.5" into seconds since midnight.
func ParseTOD(text string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	if len(parts) != 3 {
		return 0, fmt.Errorf("시각은 14:00 또는 14:00:00 처럼 쓰세요")
	}
	var h, m int
	var s float64
	if _, err := fmt.Sscan(parts[0], &h); err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("시(%q)가 이상합니다", parts[0])
	}
	if _, err := fmt.Sscan(parts[1], &m); err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("분(%q)이 이상합니다", parts[1])
	}
	if _, err := fmt.Sscan(parts[2], &s); err != nil || s < 0 || s >= 60 {
		return 0, fmt.Errorf("초(%q)가 이상합니다", parts[2])
	}
	return float64(h*3600+m*60) + s, nil
}

// NextKST returns the next moment at time-of-day tod in KST, as epoch seconds.
func NextKST(tod, serverNow float64) float64 {
	t := time.Unix(int64(math.Floor(serverNow)), 0).In(KST)
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, KST)
	target := float64(midnight.Unix()) + tod
	if target <= serverNow {
		target += 86400
	}
	return target
}

// FmtKST formats an epoch time as HH:MM:SS.fff in KST with `digits` fraction digits.
func FmtKST(epoch float64, digits int) string {
	sec := math.Floor(epoch)
	t := time.Unix(int64(sec), 0).In(KST)
	s := fmt.Sprintf("%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())
	if digits > 0 {
		s += fmt.Sprintf(".%0*d", digits, int((epoch-sec)*math.Pow10(digits)))
	}
	return s
}
