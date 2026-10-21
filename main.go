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
	urlEntry, targetEntry, delayEntry *widget.Entry
	clickCheck                        *widget.Check
	syncBtn, armBtn                   *widget.Button
	clockText, countText              *canvas.Text
	infoLabel, logLabel               *widget.Label
	logScroll                         *container.Scroll

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
	for _, p := range koreanFonts {
		if b, err := os.ReadFile(p); err == nil {
			a.Settings().SetTheme(koreanTheme{theme.DefaultTheme(), fyne.NewStaticResource(filepath.Base(p), b)})
			break
		}
	}
	w := a.NewWindow("서버 시각 클릭")
	u := newUI()
	w.SetContent(u.layout())
	w.Resize(fyne.NewSize(460, 620))

	go func() {
		for range time.Tick(20 * time.Millisecond) {
			fyne.Do(u.refresh)
		}
	}()
	u.startSync()
	w.ShowAndRun()
}

func newUI() *ui {
	u := &ui{}
	u.urlEntry = widget.NewEntry()
	u.urlEntry.SetText(clock.DefaultURL)
	u.targetEntry = widget.NewEntry()
	u.targetEntry.SetPlaceHolder("16:00:00")
	u.delayEntry = widget.NewEntry()
	u.delayEntry.SetText("0")
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
		widget.NewFormItem("오픈 시각", u.targetEntry),
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
				text += "  ⚠ 브라우저를 클릭해 앞으로!"
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
	tod, err := clock.ParseTOD(u.targetEntry.Text)
	if err != nil {
		u.addLog(err.Error())
		return
	}
	delay := 0.0
	if s := strings.TrimSpace(u.delayEntry.Text); s != "" {
		if delay, err = strconv.ParseFloat(s, 64); err != nil || delay < 0 || delay > 10000 {
			u.addLog("클릭 지연은 0~10000 사이 숫자(ms)로 써 주세요.")
			return
		}
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
	for _, w := range []fyne.Disableable{u.urlEntry, u.targetEntry, u.delayEntry, u.clickCheck, u.syncBtn} {
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
