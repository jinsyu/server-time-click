//go:build !darwin && !windows

package main

import "errors"

func platformInit() {}

func clickSetup() error {
	return errors.New("이 운영체제에서는 클릭을 지원하지 않습니다.")
}

func leftClick() (float64, float64) { return 0, 0 }

func ownAppFront() bool { return false }

func beep() {}
