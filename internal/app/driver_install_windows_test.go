package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"android-tools/internal/tooldeps"
	"github.com/egoist/mygo/ui"
)

func TestDriverSuccessRequiresUniqueFastbootSerial(t *testing.T) {
	devices := []device{{Serial: "a25fcb55"}}
	if !fastbootHasUniqueSerial(devices, "A25FCB55") {
		t.Fatal("matching device rejected")
	}
	if fastbootHasUniqueSerial(devices, "other") || fastbootHasUniqueSerial(devices, "") {
		t.Fatal("unmatched device accepted")
	}
	devices = append(devices, device{Serial: "A25FCB55"})
	if fastbootHasUniqueSerial(devices, "a25fcb55") {
		t.Fatal("ambiguous device accepted")
	}
}

func TestDriverSearchAvailableForAnyCandidate(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", fastbootCandidates: []fastbootCandidate{{Instance: "one", Serial: "phone"}}}
	tt := ui.NewTester(a.view, 960, 680)
	if err := tt.Click("查看驱动与连接指引"); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("查找兼容驱动"); !found {
		t.Fatal("missing driver search")
	}
}

func TestDriverPackageVerification(t *testing.T) {
	if os.Getenv("ANDROID_TOOLS_TEST_DRIVER_DOWNLOAD") != "1" {
		t.Skip("opt-in download")
	}
	dir := t.TempDir()
	inf, err := prepareGoogleDriver(context.Background(), dir, tooldeps.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(inf) != "android_winusb.inf" {
		t.Fatal(inf)
	}
	if err = verifyDriverCatalog(inf); err == nil {
		t.Fatal("unsigned INF accepted as catalog")
	}
	if instance := os.Getenv("ANDROID_TOOLS_TEST_FASTBOOT_INSTANCE"); instance != "" {
		matched, err := matchDriverForDevice(instance, inf)
		if err != nil || !matched {
			t.Fatalf("设备驱动匹配失败：matched=%v err=%v", matched, err)
		}
	}
	if instance := os.Getenv("ANDROID_TOOLS_TEST_UNMATCHED_INSTANCE"); instance != "" {
		matched, err := matchDriverForDevice(instance, inf)
		if err != nil || matched {
			t.Fatalf("无关设备不应匹配：matched=%v err=%v", matched, err)
		}
	}

}

func TestShellExecuteInfoLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(shellExecuteInfo{}) != 112 {
		t.Fatal("unexpected Windows ShellExecuteEx layout")
	}
}
