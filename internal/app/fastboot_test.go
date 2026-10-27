package app

import (
	"errors"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestFastbootInfoIsOnDemand(t *testing.T) {
	calls := 0
	e := &engine{fastboot: "fastboot", execute: func(_ string, _ time.Duration, _ string, _ ...string) (string, error) {
		calls++
		return "product: phone", nil
	}}
	a := &app{engine: e}
	a.openFastboot("phone")
	if calls != 0 || a.fastbootInfoOpen || a.fastbootInfoLoading {
		t.Fatal("device information loaded automatically")
	}
	a.fastbootOutput = "hidden-device-information"
	tt := ui.NewTester(a.view, 960, 680)
	if _, ok := tt.Find("查看设备信息"); !ok {
		t.Fatal("missing device information button")
	}
	if tt.HasText("hidden-device-information") {
		t.Fatal("information visible by default")
	}
	a.fastbootInfoOpen = true
	a.fastbootInfoLoading = true
	a.fastbootInfoRequest++
	request := a.fastbootInfoRequest
	a.applyFastbootInfo(request, "phone", "product: phone", nil)
	tt.Frame()
	if !tt.HasText("product: phone") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("关闭设备信息"); err != nil {
		t.Fatal(err)
	}
	a.applyFastbootInfo(request, "phone", "stale", nil)
	if a.fastbootOutput == "stale" {
		t.Fatal("closed dialog accepted late result")
	}
	a.fastbootInfoOpen = true
	a.fastbootInfoLoading = true
	a.fastbootInfoRequest++
	a.applyFastbootInfo(a.fastbootInfoRequest, "phone", "", errors.New("offline"))
	if a.fastbootInfoError == "" || a.fastbootInfoLoading {
		t.Fatal("error state missing")
	}
}
func TestFastbootButtonsAligned(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {960, 680}} {
		a := &app{engine: newEngine(), page: "fastboot", fastbootSerial: "phone"}
		tt := ui.NewTester(a.view, size[0], size[1])
		a1, ok := tt.Find("重启系统")
		if !ok {
			t.Fatal("missing restart")
		}
		b, ok := tt.Find("重启引导程序")
		if !ok {
			t.Fatal("missing bootloader")
		}
		c, ok := tt.Find("进入 Fastbootd")
		if !ok {
			t.Fatal("missing fastbootd")
		}
		d, ok := tt.Find("进入恢复模式")
		if !ok {
			t.Fatal("missing recovery")
		}
		if a1.W != b.W || a1.W != c.W || a1.W != d.W || a1.H != b.H || a1.H != c.H || a1.H != d.H || a1.Y != b.Y || c.Y != d.Y || a1.X != c.X || b.X != d.X {
			t.Fatalf("%v: %+v %+v %+v %+v", size, a1, b, c, d)
		}
	}
}
func TestFastbootPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_FASTBOOT_PREVIEW")
	if path == "" {
		t.Skip("preview")
	}
	a := &app{engine: newEngine(), page: "fastboot", fastbootSerial: "a25fcb55"}
	tt := ui.NewTester(a.view, 960, 680)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
