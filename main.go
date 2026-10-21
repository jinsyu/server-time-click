package main

// GUI: shows the server clock, counts down to an open time, and optionally
// left-clicks once at the current mouse position at that moment. It only
// reads the clock; it never logs in or books anything.

import (
	"fmt"
	"image/color"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"servertime/internal/clock"
)

// Fyne's bundled font has no Hangul, so borrow a system font that does.
var koreanFonts = []string{
	"/System/Library/Fonts/Supplemental/AppleGothic.ttf",
	"/Library/Fonts/Arial Unicode.ttf",
	`C:\Windows\Fonts\malgun.ttf`,
}

type koreanTheme struct {
	fyne.Theme
	font fyne.Resource
}

func (t koreanTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Monospace {
		return t.Theme.Font(s)
	}
	return t.font
}

var red = color.NRGBA{R: 0xE5, G: 0x39, B: 0x35, A: 0xFF}

type ui struct {
	urlEntry, delayEntry     *widget.Entry
	hourSel, minSel, secSel  *widget.Select
	nextHourBtn, practiceBtn *widget.Button
	targetLabel              *widget.Label
	clickCheck               *widget.Check
	syncBtn, armBtn          *widget.Button
	clockText, countText     *canvas.Text
	infoLabel, logLabel      *widget.Label
	logScroll                *container.Scroll

	mu        sync.Mutex
	clock     *clock.Clock
	stopSync  chan struct{}
	armed     bool
	click     bool
	clicked   bool
	targetAt  float64
	markAt    float64 // server time of the click (or open); no requests near it
	cancelArm chan struct{}
	beeped    map[int]bool
	logs      []string
}

func main() {
	platformInit()
	a := app.NewWithID("io.github.jinsyu.servertime")
	useKoreanFont(a)
	w := a.NewWindow("서버 시각 클릭")
	u := newUI()
	w.SetContent(u.layout())
	w.Resize(fyne.NewSize(460, 680))

	go func() {
		for range time.Tick(20 * time.Millisecond) {
			fyne.Do(u.refresh)
		}
	}()
	u.startSync()
	w.ShowAndRun()
}

// useKoreanFont sets the first system font with Hangul as the UI font and
// reports which one it used ("" if none was found).
func useKoreanFont(a fyne.App) string {
	for _, p := range koreanFonts {
		if b, err := os.ReadFile(p); err == nil {
			a.Settings().SetTheme(koreanTheme{theme.DefaultTheme(), fyne.NewStaticResource(filepath.Base(p), b)})
			return p
		}
	}
	return ""
}

// twoDigits returns "00", "01", ... up to n-1.
func twoDigits(n int) []string {
	s := make([]string, n)
	for i := range s {
		s[i] = fmt.Sprintf("%02d", i)
	}
	return s
}

func newUI() *ui {
	u := &ui{}
	u.urlEntry = widget.NewEntry()
	u.urlEntry.SetText(clock.DefaultURL)
	u.hourSel = widget.NewSelect(twoDigits(24), nil)
	u.minSel = widget.NewSelect(twoDigits(60), nil)
	u.secSel = widget.NewSelect(twoDigits(60), nil)
	// The default "(Select one)" placeholder sets each dropdown's minimum width.
	u.hourSel.PlaceHolder, u.minSel.PlaceHolder, u.secSel.PlaceHolder = "시", "분", "초"
	u.setTarget(math.Floor(clock.Now()/3600)*3600 + 3600) // next full hour
	u.nextHourBtn = widget.NewButton("다음 정각", func() {
		serverNow := u.serverNow()
		u.setTarget(math.Floor(serverNow/3600)*3600 + 3600)
	})
	u.practiceBtn = widget.NewButton("연습: 1~2분 뒤", func() {
		serverNow := u.serverNow()
		u.setTarget(math.Ceil(serverNow/60)*60 + 60)
	})
	u.targetLabel = widget.NewLabel("")
	u.delayEntry = widget.NewEntry()
	u.delayEntry.SetText("50") // the button usually switches a moment after the open time
	u.delayEntry.Validator = func(s string) error {
		if _, err := parseDelay(s); err != nil {
			return err
		}
		return nil
	}
	u.clickCheck = widget.NewCheck("오픈 시각에 지금 마우스 위치를 한 번 클릭", nil)
	u.clickCheck.SetChecked(true)
	u.syncBtn = widget.NewButton("시각 다시 맞추기", u.startSync)
	u.armBtn = widget.NewButton("예약", u.toggleArm)
	u.armBtn.Importance = widget.HighImportance

	u.clockText = canvas.NewText("--:--:--.--", theme.Color(theme.ColorNameForeground))
	u.clockText.TextSize = 44
	u.clockText.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	u.countText = canvas.NewText("", theme.Color(theme.ColorNameForeground))
	u.countText.TextSize = 20
	u.countText.TextStyle = fyne.TextStyle{Bold: true}
	u.infoLabel = widget.NewLabel("")
	u.logLabel = widget.NewLabel("")
	u.logLabel.Wrapping = fyne.TextWrapWord
	u.logScroll = container.NewVScroll(u.logLabel)
	u.logScroll.SetMinSize(fyne.NewSize(0, 180))
	return u
}

func (u *ui) layout() fyne.CanvasObject {
	form := widget.NewForm(
		widget.NewFormItem("주소", u.urlEntry),
		widget.NewFormItem("오픈 시:분:초", container.NewGridWithColumns(3, u.hourSel, u.minSel, u.secSel)),
		widget.NewFormItem("", container.NewHBox(u.nextHourBtn, u.practiceBtn)),
		widget.NewFormItem("", u.targetLabel),
		widget.NewFormItem("클릭 지연(ms)", u.delayEntry),
	)
	return container.NewVBox(
		container.NewCenter(u.clockText),
		container.NewCenter(u.countText),
		container.NewCenter(u.infoLabel),
		widget.NewSeparator(),
		form,
		u.clickCheck,
		container.NewGridWithColumns(2, u.syncBtn, u.armBtn),
		widget.NewSeparator(),
		u.logScroll,
	)
}

// addLog is safe to call from any goroutine.
func (u *ui) addLog(s string) {
	u.mu.Lock()
	u.logs = append(u.logs, time.Now().Format("15:04:05 ")+s)
	if len(u.logs) > 80 {
		u.logs = u.logs[1:]
	}
	text := strings.Join(u.logs, "\n")
	u.mu.Unlock()
	fyne.Do(func() {
		u.logLabel.SetText(text)
		u.logScroll.ScrollToBottom()
	})
}

func (u *ui) refresh() {
	u.mu.Lock()
	c, armed, click, clicked, targetAt := u.clock, u.armed, u.click, u.clicked, u.targetAt
	u.mu.Unlock()
	if armed {
		u.showTarget(targetAt, u.serverNow())
	} else if tod, ok := u.selectedTOD(); ok {
		serverNow := u.serverNow()
		u.showTarget(clock.NextKST(tod, serverNow), serverNow)
	}
	if c == nil || !c.Valid() {
		return
	}
	offset := c.Offset()
	serverNow := clock.Now() + offset
	u.clockText.Text = clock.FmtKST(serverNow, 2)
	u.clockText.Refresh()

	sign := "느림"
	if offset > 0 {
		sign = "빠름"
	}
	u.infoLabel.SetText(fmt.Sprintf("오차 ±%.0fms · 서버가 내 컴퓨터보다 %.3f초 %s", c.Error()*1000, math.Abs(offset), sign))

	text, col := "", theme.Color(theme.ColorNameForeground)
	if armed {
		if left := targetAt - serverNow; left > 0 {
			text = fmt.Sprintf("오픈까지 %02d:%02d:%05.2f", int(left/3600), int(math.Mod(left, 3600)/60), math.Mod(left, 60))
			if left <= 10 {
				col = red
			}
			if click && left <= 30 && ownAppFront() {
				text += "  (브라우저를 클릭해 앞으로!)"
				col = red
			}
			for _, mark := range []int{10, 3, 2, 1} {
				if left <= float64(mark) && !u.beeped[mark] {
					u.beeped[mark] = true
					beep()
				}
			}
		} else if !click || clicked {
			text, col = "오픈!", red
		}
	}
	if u.countText.Text != text || u.countText.Color != col {
		u.countText.Text, u.countText.Color = text, col
		u.countText.Refresh()
	}
}

// serverNow is the server's time once synced, this computer's time before.
func (u *ui) serverNow() float64 {
	u.mu.Lock()
	c := u.clock
	u.mu.Unlock()
	if c != nil && c.Valid() {
		return clock.Now() + c.Offset()
	}
	return clock.Now()
}

// setTarget puts the KST time of day of epoch `at` into the dropdowns.
func (u *ui) setTarget(at float64) {
	t := time.Unix(int64(math.Floor(at)), 0).In(clock.KST)
	u.hourSel.SetSelected(fmt.Sprintf("%02d", t.Hour()))
	u.minSel.SetSelected(fmt.Sprintf("%02d", t.Minute()))
	u.secSel.SetSelected(fmt.Sprintf("%02d", t.Second()))
}

// selectedTOD is the chosen open time as seconds since midnight.
func (u *ui) selectedTOD() (float64, bool) {
	h, err1 := strconv.Atoi(u.hourSel.Selected)
	m, err2 := strconv.Atoi(u.minSel.Selected)
	s, err3 := strconv.Atoi(u.secSel.Selected)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return float64(h*3600 + m*60 + s), true
}

func parseDelay(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := strconv.ParseFloat(s, 64)
	if err != nil || d < 0 || d > 10000 {
		return 0, fmt.Errorf("0~10000 사이 숫자(ms)로 써 주세요")
	}
	return d, nil
}

// showTarget writes which day the open time falls on and how far away it is.
func (u *ui) showTarget(at, serverNow float64) {
	day := "오늘"
	if time.Unix(int64(at), 0).In(clock.KST).YearDay() != time.Unix(int64(serverNow), 0).In(clock.KST).YearDay() {
		day = "내일"
	}
	left := int(math.Max(0, at-serverNow))
	var until string
	switch {
	case left >= 3600:
		until = fmt.Sprintf("%d시간 %d분 뒤", left/3600, left%3600/60)
	case left >= 60:
		until = fmt.Sprintf("%d분 %d초 뒤", left/60, left%60)
	default:
		until = fmt.Sprintf("%d초 뒤", left)
	}
	text := fmt.Sprintf("→ %s %s  (%s)", day, clock.FmtKST(at, 0), until)
	if u.targetLabel.Text != text {
		u.targetLabel.SetText(text)
	}
}

func (u *ui) startSync() {
	raw := strings.TrimSpace(u.urlEntry.Text)
	if p, err := url.Parse(raw); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		u.addLog("주소가 이상합니다. https:// 로 시작하게 써 주세요.")
		return
	}
	u.disarm()
	u.mu.Lock()
	if u.stopSync != nil {
		close(u.stopSync)
		u.stopSync = nil
	}
	u.clock = nil
	u.mu.Unlock()
	u.clockText.Text = "--:--:--.--"
	u.clockText.Refresh()
	u.infoLabel.SetText("맞추는 중...")
	u.syncBtn.Disable()
	u.armBtn.Disable()
	u.addLog(raw + " 시각 맞추는 중...")

	go func() {
		c := clock.New(raw)
		ok := c.InitialSync(12, u.addLog)
		fyne.Do(func() {
			u.syncBtn.Enable()
			u.armBtn.Enable()
		})
		if !ok {
			u.addLog("서버 시각을 못 읽었습니다. 인터넷 연결이나 주소를 확인하고 '시각 다시 맞추기'를 누르세요.")
			fyne.Do(func() { u.infoLabel.SetText("서버 시각을 못 읽었습니다") })
			return
		}
		stop := make(chan struct{})
		u.mu.Lock()
		u.clock, u.stopSync = c, stop
		u.mu.Unlock()
		go c.KeepSyncing(15*time.Second, u.quiet, stop)
		u.addLog(fmt.Sprintf("맞춤 완료 (오차 ±%.0fms, 왕복 %.0fms). 켜 둔 동안 15초마다 다시 맞춥니다.", c.Error()*1000, c.Rtt()*1000))
	}()
}

// quiet is true from 5s before the click (or open) to 2s after.
func (u *ui) quiet(serverNow float64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	left := u.markAt - serverNow
	return u.armed && left > -2 && left < 5
}

func (u *ui) toggleArm() {
	u.mu.Lock()
	armed := u.armed
	u.mu.Unlock()
	if armed {
		u.disarm()
		u.addLog("예약 취소")
	} else {
		u.arm()
	}
}

func (u *ui) arm() {
	u.mu.Lock()
	c := u.clock
	u.mu.Unlock()
	if c == nil || !c.Valid() {
		u.addLog("서버 시각을 아직 못 맞췄습니다.")
		return
	}
	tod, ok := u.selectedTOD()
	if !ok {
		u.addLog("오픈 시각의 시·분·초를 모두 고르세요.")
		return
	}
	delay, err := parseDelay(u.delayEntry.Text)
	if err != nil {
		u.addLog("클릭 지연: " + err.Error())
		return
	}
	click := u.clickCheck.Checked
	if click {
		if err := clickSetup(); err != nil {
			u.addLog(err.Error())
			return
		}
	}

	targetAt := clock.NextKST(tod, clock.Now()+c.Offset())
	clickAt := targetAt + delay/1000
	mark := targetAt
	if click {
		mark = clickAt
	}
	cancel := make(chan struct{})
	u.mu.Lock()
	u.armed, u.click, u.clicked = true, click, false
	u.targetAt, u.markAt, u.cancelArm = targetAt, mark, cancel
	u.beeped = map[int]bool{}
	u.mu.Unlock()
	u.setInputsEnabled(false)
	u.armBtn.SetText("취소")

	if click {
		u.addLog(fmt.Sprintf("서버 시각 %s 에 클릭 예약. 브라우저를 한 번 클릭해 앞으로 두고, 마우스를 버튼 위에 올려 두세요.", clock.FmtKST(clickAt, 3)))
		go u.runClick(c, clickAt, cancel)
	} else {
		u.addLog(clock.FmtKST(targetAt, 0) + " 까지 카운트다운")
	}
}

func (u *ui) disarm() {
	u.mu.Lock()
	if u.cancelArm != nil {
		close(u.cancelArm)
		u.cancelArm = nil
	}
	u.armed = false
	u.mu.Unlock()
	u.setInputsEnabled(true)
	u.armBtn.SetText("예약")
}

func (u *ui) setInputsEnabled(on bool) {
	for _, w := range []fyne.Disableable{u.urlEntry, u.hourSel, u.minSel, u.secSel, u.nextHourBtn, u.practiceBtn, u.delayEntry, u.clickCheck, u.syncBtn} {
		if on {
			w.Enable()
		} else {
			w.Disable()
		}
	}
}

// runClick sleeps until 0.3s before the click, re-reading the offset as the
// background sync refines it, then spins the last stretch and clicks once.
func (u *ui) runClick(c *clock.Clock, at float64, cancel <-chan struct{}) {
	for {
		left := at - (clock.Now() + c.Offset())
		if left < 0.3 {
			break
		}
		select {
		case <-cancel:
			return
		case <-time.After(clock.Seconds(math.Min(left-0.3, 0.5))):
		}
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	offset := c.Offset()
	for clock.Now() < at-offset {
	}
	select {
	case <-cancel:
		return
	default:
	}
	fired := clock.Now()
	x, y := leftClick()
	u.mu.Lock()
	u.clicked = true
	u.mu.Unlock()
	late := (fired + offset - at) * 1000
	u.addLog(fmt.Sprintf("클릭함 (%.0f, %.0f)  서버 시각 %s  (목표보다 %+.1fms, 오차 ±%.0fms)", x, y, clock.FmtKST(fired+offset, 3), late, c.Error()*1000))
}
