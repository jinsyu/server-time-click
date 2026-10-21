//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework AppKit -framework Foundation
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>

// Keep App Nap from throttling our timers while the browser is in front, and
// keep the Mac and its display awake.
static void keepAwake(void) {
	static id activity = nil;
	if (activity == nil) {
		activity = [[[NSProcessInfo processInfo]
			beginActivityWithOptions:NSActivityUserInitiated | NSActivityLatencyCritical | NSActivityIdleDisplaySleepDisabled
			reason:@"timed click"] retain];
	}
}

// Without Accessibility permission posted clicks are silently dropped; this
// also makes macOS show the permission prompt.
static int trusted(void) {
	@autoreleasepool {
		NSDictionary *opts = @{(NSString *)kAXTrustedCheckOptionPrompt: @YES};
		return AXIsProcessTrustedWithOptions((CFDictionaryRef)opts) ? 1 : 0;
	}
}

// The first event call opens the WindowServer connection (~15ms); do it early.
static void warmUp(void) { CFRelease(CGEventCreate(NULL)); }

static void leftClick(double *x, double *y) {
	CGEventRef here = CGEventCreate(NULL);
	CGPoint p = CGEventGetLocation(here);
	CFRelease(here);
	*x = p.x;
	*y = p.y;
	CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, p, kCGMouseButtonLeft);
	CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, p, kCGMouseButtonLeft);
	CGEventSetIntegerValueField(down, kCGMouseEventClickState, 1);
	CGEventSetIntegerValueField(up, kCGMouseEventClickState, 1);
	CGEventPost(kCGHIDEventTap, down);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
}

static int ownAppFront(void) { return [[NSRunningApplication currentApplication] isActive] ? 1 : 0; }

static void beep(void) { NSBeep(); }
*/
import "C"

import "errors"

func platformInit() { C.keepAwake() }

func clickSetup() error {
	if C.trusted() == 0 {
		return errors.New("손쉬운 사용 권한이 없습니다. 시스템 설정 → 개인정보 보호 및 보안 → 손쉬운 사용에서 이 앱을 켠 뒤, 앱을 완전히 종료했다가 다시 여세요.")
	}
	C.warmUp()
	return nil
}

func leftClick() (float64, float64) {
	var x, y C.double
	C.leftClick(&x, &y)
	return float64(x), float64(y)
}

func ownAppFront() bool { return C.ownAppFront() != 0 }

func beep() { C.beep() }
