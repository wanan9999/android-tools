package app

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func props(h, g string) string {
	return "[ro.serialno]: [" + h + "]\n[persist.adb.wifi.guid]: [" + g + "]\n[ro.product.model]: [Test]\n[ro.build.version.sdk]: [34]\n"
}
func TestIdentityAndMdns(t *testing.T) {
	id, err := parseIdentity("adb-guid._adb-tls-connect._tcp", props("physical", "adb-guid"))
	if err != nil || id.Hardware != "physical" || id.GUID != "adb-guid" {
		t.Fatalf("identity=%+v err=%v", id, err)
	}
	if _, err = parseIdentity("adb-other._adb-tls-connect._tcp", props("physical", "adb-guid")); err == nil {
		t.Fatal("mismatched GUID accepted")
	}
	for _, h := range []string{"", "unknown", "0000", "0123456789abcdef"} {
		if _, err = parseIdentity("usb", props(h, "")); err == nil {
			t.Fatalf("bad hardware %q accepted", h)
		}
	}
	if got := serviceGUID("adb-guid (2)._adb-tls-connect._tcp."); got != "adb-guid" {
		t.Fatal(got)
	}
}
func TestRegistryPreservesIdentityAndAtomicFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "devices.json")
	r, err := openRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.save(savedDevice{Hardware: "one", GUID: "g", Address: "192.168.1.3:1"}); err != nil {
		t.Fatal(err)
	}
	if err = r.save(savedDevice{Hardware: "two"}); err != nil {
		t.Fatal(err)
	}
	if err = r.save(savedDevice{Hardware: "one", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := openRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	ds := loaded.snapshot()
	if len(ds) != 2 || ds[0].GUID != "g" || ds[0].Address != "192.168.1.3:1" {
		t.Fatalf("%+v", ds)
	}
	loaded.path = filepath.Join(path, "invalid")
	if err = loaded.remove("one"); err == nil {
		t.Fatal("expected write failure")
	}
	if len(loaded.snapshot()) != 2 {
		t.Fatal("failed write mutated memory")
	}
	if err = os.WriteFile(path, []byte(`{"devices":[{"hardware":"same"},{"hardware":"same"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openRegistry(path); err == nil {
		t.Fatal("duplicate hardware accepted")
	}
}
func TestAddressReuseIsRejected(t *testing.T) {
	e := &engine{adb: "adb"}
	e.execute = func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmd, "connect "):
			return "connected to 192.168.1.2:5555", nil
		case strings.Contains(cmd, "getprop"):
			return props("different", "guid"), nil
		case strings.HasPrefix(cmd, "disconnect "):
			return "disconnected", nil
		}
		return "", errors.New("unexpected " + cmd)
	}
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	if err := r.save(savedDevice{Hardware: "original", GUID: "guid", Address: "192.168.1.2:5555"}); err != nil {
		t.Fatal(err)
	}
	m := newManager(e, r)
	if err := m.connect("192.168.1.2:5555", ""); err == nil {
		t.Fatal("reused address accepted")
	}
	if got := r.snapshot(); len(got) != 1 || got[0].Hardware != "original" {
		t.Fatalf("record changed: %+v", got)
	}
}
func TestPairSecretNotInError(t *testing.T) {
	e := &engine{adb: "adb"}
	e.execute = func(_ string, _ time.Duration, input string, args ...string) (string, error) {
		if input != "123456" {
			t.Fatal("secret not sent via stdin")
		}
		return "Successfully paired to x [guid=abc]", nil
	}
	guid, err := e.pairSecret("192.168.1.2:1234", "123456")
	if err != nil || guid != "abc" {
		t.Fatalf("%q %v", guid, err)
	}
}
func TestMainViews(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", audio: true}
	tt := ui.NewTester(a.view, 960, 680)
	if !tt.HasText("所有设备") || !tt.HasText("尚未发现设备") {
		t.Fatalf("texts %v", tt.Texts())
	}
	if _, ok := tt.Find("检查并下载组件"); !ok {
		t.Fatal("missing tools button")
	}
	if err := tt.Click("无线连接"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("扫码配对") {
		t.Fatalf("texts %v", tt.Texts())
	}
	a.page = "fastboot"
	a.fastbootSerial = "serial"
	tt.Frame()
	if !tt.HasText("刷写镜像") {
		t.Fatalf("texts %v", tt.Texts())
	}
}
func TestRenderPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_PREVIEW")
	if path == "" {
		return
	}
	a := &app{engine: newEngine(), page: "devices", audio: true, devices: []device{{Serial: "192.168.1.8:5555", Hardware: "test", State: "device", Model: "Pixel_8", Kind: "ADB"}, {Serial: "demo", State: "fastboot", Kind: "Fastboot"}}}
	tt := ui.NewTester(a.view, 960, 680)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
func TestQRPairAndVerifiedConnection(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	e := &engine{adb: "adb"}
	var serviceName string
	e.execute = func(_ string, _ time.Duration, input string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "mdns services":
			return serviceName + " _adb-tls-pairing._tcp 192.168.1.4:1111\nadb-guid _adb-tls-connect._tcp 192.168.1.4:2222", nil
		case strings.HasPrefix(cmd, "pair "):
			if input == "" {
				t.Error("missing secret")
			}
			return "Successfully paired [guid=adb-guid]", nil
		case strings.HasPrefix(cmd, "connect "):
			return "connected to 192.168.1.4:2222", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", "adb-guid"), nil
		default:
			return "", nil
		}
	}
	m := newManager(e, r)
	events := make(chan string, 4)
	payload, err := m.beginQR(func(state, _ string) { events <- state })
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(payload, ";")
	serviceName = strings.TrimPrefix(parts[1], "S:")
	deadline := time.After(8 * time.Second)
	for {
		select {
		case event := <-events:
			if event == "connected" {
				devices := r.snapshot()
				if len(devices) != 1 || devices[0].Hardware != "phone1" || devices[0].Address != "192.168.1.4:2222" {
					t.Fatalf("%+v", devices)
				}
				m.cancelPair()
				return
			}
			if event == "error" {
				t.Fatal("pair failed")
			}
		case <-deadline:
			t.Fatal("pair timed out")
		}
	}
}
func TestQRCancel(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	e := &engine{adb: "adb", execute: func(string, time.Duration, string, ...string) (string, error) {
		t.Error("cancelled session made a request")
		return "", nil
	}}
	m := newManager(e, r)
	_, err := m.beginQR(func(string, string) { t.Error("cancelled session notified") })
	if err != nil {
		t.Fatal(err)
	}
	m.cancelPair()
	time.Sleep(2200 * time.Millisecond)
	if len(r.snapshot()) != 0 {
		t.Fatal("cancelled session persisted device")
	}
}
func TestPairConnectionAddress(t *testing.T) {
	for _, tc := range []struct {
		pair, conn string
		ok         bool
	}{{"192.168.1.2:1234", "192.168.1.2:2345", true}, {"192.168.1.2:1234", "192.168.1.2:1234", false}, {"192.168.1.2:1234", "192.168.1.3:2345", false}, {"", "10.0.0.2:2345", true}} {
		if (validConnectionAddress(tc.pair, tc.conn) == nil) != tc.ok {
			t.Errorf("%+v", tc)
		}
	}
}
func TestGUIDCannotMoveToAnotherHardware(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	if err := r.save(savedDevice{Hardware: "one", GUID: "same"}); err != nil {
		t.Fatal(err)
	}
	if err := r.save(savedDevice{Hardware: "two", GUID: "same"}); err == nil {
		t.Fatal("duplicate GUID accepted")
	}
	if len(r.snapshot()) != 1 {
		t.Fatal("registry changed on failure")
	}
}
func TestNarrowView(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", devices: []device{{Serial: "one", State: "device", Kind: "ADB"}, {Serial: "two", State: "fastboot", Kind: "Fastboot"}}}
	tt := ui.NewTester(a.view, 640, 480)
	if !tt.HasText("设备") {
		t.Fatal(tt.Texts())
	}
	a.page = "pair"
	tt.Frame()
	if !tt.HasText("扫码配对") {
		t.Fatal(tt.Texts())
	}
}
func TestPairPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_PAIR_PREVIEW")
	if path == "" {
		return
	}
	a := &app{engine: newEngine(), page: "pair", size: "1280", fps: "30"}
	tt := ui.NewTester(a.view, 960, 680)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
func TestPairModeNavigation(t *testing.T) {
	a := &app{engine: newEngine(), page: "pair"}
	tt := ui.NewTester(a.view, 960, 680)
	if tt.HasText("切换窗口材质") || tt.HasText("ADB  ·  FASTBOOT") {
		t.Fatal("unexpected sidebar footer")
	}
	if !tt.HasText("使用二维码配对") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("配对码"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1  建立配对") || !tt.HasText("2  连接设备") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("已配对"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("连接已配对设备") || tt.HasText("1  建立配对") {
		t.Fatal(tt.Texts())
	}
}
func TestADBInfoRejectsStaleResults(t *testing.T) {
	a := &app{page: "adb", adbSerial: "new", adbInfoRequest: 2, adbInfoLoading: true}
	a.applyADBInfo(1, "old", identity{Hardware: "old"}, nil)
	if !a.adbInfoLoading || a.adbInfo.Hardware != "" {
		t.Fatal("stale device result applied")
	}
	a.applyADBInfo(2, "new", identity{Hardware: "new", Model: "Pixel", SDK: 34}, nil)
	if a.adbInfoLoading || a.adbInfo.Hardware != "new" {
		t.Fatalf("%+v", a)
	}
	a.backToDevices()
	a.applyADBInfo(2, "new", identity{Hardware: "late"}, nil)
	if a.adbInfo.Hardware != "new" {
		t.Fatal("result applied after leaving page")
	}
}
func TestADBViewLayoutAndBack(t *testing.T) {
	a := &app{engine: newEngine(), page: "adb", adbSerial: "device", adbInfo: identity{Hardware: "serial", Model: "Pixel", SDK: 34}}
	tt := ui.NewTester(a.view, 640, 480)
	for _, label := range []string{"设备型号", "Pixel", "Android SDK", "硬件身份", "安装 APK", "保存截图", "返回设备"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("missing %s", label)
		}
	}
	if tt.HasText("读取设备信息") {
		t.Fatal("manual load button remains")
	}
	if err := tt.Click("返回设备"); err != nil {
		t.Fatal(err)
	}
	if a.page != "devices" {
		t.Fatal(a.page)
	}
}
func TestADBDetailsPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_ADB_PREVIEW")
	if path == "" {
		return
	}
	a := &app{engine: newEngine(), page: "adb", adbSerial: "192.168.1.8:5555", adbInfo: identity{Hardware: "ABC12345", Model: "Pixel 8", SDK: 34}}
	tt := ui.NewTester(a.view, 960, 680)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
func TestDeviceInformationParsing(t *testing.T) {
	raw := props("device", "guid") + "[ro.product.brand]: [Example]\n[ro.product.manufacturer]: [Maker]\n[ro.product.device]: [code]\n[ro.build.version.release]: [14]\n[ro.build.version.security_patch]: [2025-01-05]\n[ro.build.display.id]: [build-1]\n[ro.product.cpu.abilist]: [arm64-v8a,armeabi-v7a]\n"
	id, err := parseIdentity("device", raw)
	if err != nil || id.Android != "14" || id.Brand != "Example" || id.ABI != "arm64-v8a,armeabi-v7a" || id.SecurityPatch != "2025-01-05" {
		t.Fatalf("%+v %v", id, err)
	}
}
func TestOptionalDeviceDetails(t *testing.T) {
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "wm size") {
			return "Physical size: 1080x2400\nOverride size: 720x1600", nil
		}
		if strings.Contains(cmd, "dumpsys battery") {
			return "  level: 87\n", nil
		}
		return "", errors.New("unexpected command")
	}}
	got := e.loadDeviceDetails("device")
	if got.Resolution != "720x1600" || got.Battery != "87%" {
		t.Fatalf("%+v", got)
	}
	e.execute = func(string, time.Duration, string, ...string) (string, error) { return "", errors.New("unavailable") }
	got = e.loadDeviceDetails("device")
	if got.Resolution != "" || got.Battery != "" {
		t.Fatal(got)
	}
}
func TestSidebarMaterialAndFallback(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", size: "1280", fps: "30"}
	tt := ui.NewTester(a.view, 960, 680)
	alpha := func(x, y int) uint8 { img := tt.Image(); return img.Pix[img.PixOffset(x, y)+3] }
	tt.SetVibrancy(true)
	if top, below, pane := alpha(8, 20), alpha(8, 600), alpha(800, 600); top != 0 || below != 0 || pane != 255 {
		t.Fatalf("material alpha top=%d sidebar=%d pane=%d", top, below, pane)
	}
	tt.SetVibrancy(false)
	img := tt.Image()
	top := img.RGBAAt(8, 20)
	below := img.RGBAAt(8, 600)
	if top.A != 255 || below.A != 255 || top != below {
		t.Fatalf("fallback inconsistent top=%v sidebar=%v", top, below)
	}
	tt.SetDark(true)
	tt.SetVibrancy(true)
	if top, below := alpha(8, 20), alpha(8, 600); top != 0 || below != 0 {
		t.Fatalf("dark material alpha top=%d sidebar=%d", top, below)
	}
	tt.SetVibrancy(false)
	img = tt.Image()
	top = img.RGBAAt(8, 20)
	below = img.RGBAAt(8, 600)
	if top.A != 255 || below.A != 255 || top != below {
		t.Fatalf("dark fallback inconsistent top=%v sidebar=%v", top, below)
	}
}
