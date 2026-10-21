package main

import (
	"image/png"
	"math"
	"os"
	"runtime"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"servertime/internal/clock"
)

// TestRenderUI lays the window out with the software renderer, which needs no
// GPU, so CI machines can check the layout and Korean font. Set UI_SNAPSHOT
// to a .png path to save the picture.
func TestRenderUI(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()
	font := useKoreanFont(a)
	if font == "" && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		t.Fatal("no Korean font found; labels would render as boxes")
	}
	t.Logf("font: %s", font)

	u := newUI()
	u.clockText.Text = "16:00:00.00"
	u.countText.Text = "오픈까지 00:00:09.42"
	u.infoLabel.SetText("오차 ±17ms · 서버가 내 컴퓨터보다 0.177초 빠름")
	u.logLabel.SetText("맞춤 완료 (오차 ±17ms, 왕복 34ms). 켜 둔 동안 15초마다 다시 맞춥니다.")
	u.refresh()
	w := test.NewWindow(u.layout())
	defer w.Close()
	w.Resize(fyne.NewSize(460, 680))

	img := w.Canvas().Capture()
	if out := os.Getenv("UI_SNAPSHOT"); out != "" {
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTargetDropdowns(t *testing.T) {
	test.NewApp()
	u := newUI()
	if _, ok := u.selectedTOD(); !ok {
		t.Fatal("default open time not selected")
	}
	// 2026-10-21 15:59:30 KST -> 16:00:00 next hour, 16:01:00 for practice.
	now := 1792565970.0
	u.setTarget(math.Floor(now/3600)*3600 + 3600)
	if tod, _ := u.selectedTOD(); tod != 16*3600 {
		t.Errorf("next hour: got %v", tod)
	}
	u.setTarget(math.Ceil(now/60)*60 + 60)
	if tod, _ := u.selectedTOD(); tod != 16*3600+60 {
		t.Errorf("practice: got %v", tod)
	}
	if got := clock.FmtKST(now, 0); got != "15:59:30" {
		t.Fatalf("test clock wrong: %s", got)
	}
	for in, ok := range map[string]bool{"": true, "0": true, "422": true, "10000": true, "-1": false, "abc": false, "10001": false} {
		if _, err := parseDelay(in); (err == nil) != ok {
			t.Errorf("parseDelay(%q) err=%v", in, err)
		}
	}
}
