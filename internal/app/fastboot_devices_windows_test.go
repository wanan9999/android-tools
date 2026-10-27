package app

import (
	"context"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFastbootCompatibleIDs(t *testing.T) {
	for _, tt := range []struct {
		id   string
		want bool
	}{
		{`USB\COMPAT_VID_18D1&Class_FF&SubClass_42&Prot_03`, true},
		{`USB\Class_FF&SubClass_42&Prot_03`, true},
		{`USB\Class_FF&SubClass_42&Prot_01`, false},
		{`USB\VID_18D1&PID_D00D`, false},
	} {
		if fastbootCompatible([]string{tt.id}) != tt.want {
			t.Errorf("%s", tt.id)
		}
	}
}
func TestFastbootUnmatchedDoesNotGuess(t *testing.T) {
	c := []fastbootCandidate{{Instance: "one", Serial: "ABC"}, {Instance: "two", Serial: "XYZ"}}
	got := unmatchedFastboot(c, []device{{Serial: "abc"}})
	if len(got) != 1 || got[0].Instance != "two" {
		t.Fatal(got)
	}
	c = append(c, fastbootCandidate{Instance: "three", Serial: "abc"})
	if got = unmatchedFastboot(c, []device{{Serial: "abc"}}); len(got) != 3 {
		t.Fatal("ambiguous match suppressed", got)
	}
}
func TestFastbootPendingUI(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", fastbootCandidates: []fastbootCandidate{{Instance: "USB-test", Serial: "phone"}}}
	tt := ui.NewTester(a.view, 960, 680)
	if !tt.HasText("设备已接入 · 尚未匹配 Fastboot 设备") && !strings.Contains(strings.Join(tt.Texts(), " "), "尚未匹配 Fastboot 设备") {
		t.Fatal(tt.Texts())
	}
	if _, ok := tt.Find("Fastboot 操作"); ok {
		t.Fatal("unverified device allows operations")
	}
	if err := tt.Click("查看驱动与连接指引"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Fastboot 连接指引") {
		t.Fatal(tt.Texts())
	}
}
func TestFastbootNotificationAndCurrentDevice(t *testing.T) {
	if os.Getenv("ANDROID_TOOLS_TEST_FASTBOOT_HARDWARE") != "1" {
		t.Skip("opt-in hardware check")
	}
	c, err := scanFastbootCandidates()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Fastboot candidates: %+v", c)
	events := make(chan struct{}, 1)
	stop, err := subscribeUSB(events)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &engine{fastboot: os.Getenv("ANDROID_TOOLS_TEST_FASTBOOT")}
	results := make(chan fastbootSnapshot, 1)
	go e.watchFastboot(ctx, make(chan struct{}), func(s fastbootSnapshot) {
		select {
		case results <- s:
		default:
		}
	})
	select {
	case result := <-results:
		if result.deviceError || result.toolError {
			t.Fatalf("scan errors: %+v", result)
		}
		t.Logf("snapshot: %+v", result)
	case <-time.After(10 * time.Second):
		t.Fatal("no snapshot")
	}
}

func TestFastbootWatcherRechecksAfterEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan struct{}, 1)
	manual := make(chan struct{}, 1)
	scans := make(chan int, 5)
	count := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		runFastbootWatch(ctx, events, manual, time.Hour, func() { count++; scans <- count })
	}()
	select {
	case <-scans:
	case <-time.After(time.Second):
		t.Fatal("initial scan missing")
	}
	events <- struct{}{}
	select {
	case <-scans:
	case <-time.After(time.Second):
		t.Fatal("device event did not trigger scan")
	}
	select {
	case <-scans:
	case <-time.After(3 * time.Second):
		t.Fatal("settled scan missing")
	}
	manual <- struct{}{}
	select {
	case <-scans:
	case <-time.After(time.Second):
		t.Fatal("manual scan missing")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestDeviceManagerErrorInDialog(t *testing.T) {
	if deviceManagerError(nil) != "" || !strings.Contains(deviceManagerError(windows.ERROR_CANCELLED), "取消") || !strings.Contains(deviceManagerError(windows.ERROR_ACCESS_DENIED), "无法打开") {
		t.Fatal("incorrect device manager feedback")
	}
	a := &app{engine: newEngine(), page: "devices", driverHelp: true, driverHelpError: deviceManagerError(windows.ERROR_CANCELLED)}
	tt := ui.NewTester(a.view, 960, 680)
	if !tt.HasText("已取消权限确认，可从 Windows 开始菜单打开设备管理器。") {
		t.Fatal(tt.Texts())
	}
}
