package app

import (
	"image/png"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestTerminalArguments(t *testing.T) {
	for _, tc := range []struct {
		kind, line string
		want       []string
		bad        bool
	}{
		{"ADB", `adb install "C:\My files\app.apk"`, []string{"install", `C:\My files\app.apk`}, false},
		{"Fastboot", `fastboot flash boot "C:\My files\boot.img"`, []string{"flash", "boot", `C:\My files\boot.img`}, false},
		{"ADB", `-s other shell id`, nil, true},
		{"ADB", `kill-server`, nil, true},
		{"Fastboot", `-s other erase userdata`, nil, true},
	} {
		got, err := parseTerminalArgs(tc.line, tc.kind)
		if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %v %v", tc.line, got, err)
		}
	}
}
func TestFastbootConfirmation(t *testing.T) {
	for _, cmd := range []string{"flash", "erase", "flashing", "oem", "update-super", "boot"} {
		if !riskyFastboot([]string{cmd}) {
			t.Errorf("%s", cmd)
		}
	}
	for _, cmd := range []string{"getvar", "reboot"} {
		if riskyFastboot([]string{cmd}) {
			t.Errorf("%s", cmd)
		}
	}
}
func TestTerminalOutputAndDevice(t *testing.T) {
	a := &app{page: "terminal", terminal: terminalState{kind: "ADB", serial: "a"}, devices: []device{{Kind: "ADB", Serial: "a", State: "device"}}}
	if !a.terminalOnline() {
		t.Fatal("online device not found")
	}
	a.appendTerminal("first\r\nsecond\n")
	if a.terminal.output != "first\r\nsecond\n" {
		t.Fatal("output changed")
	}
	a.appendTerminal(strings.Repeat("中", 400000))
	if len(a.terminal.output) > 1<<20 || !strings.HasSuffix(a.terminal.output, "中") {
		t.Fatal("output bound")
	}
	a.devices[0].State = "offline"
	if a.terminalOnline() {
		t.Fatal("offline device accepted")
	}
	tt := ui.NewTester(a.view, 960, 680)
	if !tt.HasText("ADB 终端") {
		t.Fatal(tt.Texts())
	}
	if _, ok := tt.Find("发送命令"); !ok {
		t.Fatal("missing execute")
	}
}

func TestTerminalFileArguments(t *testing.T) {
	term := terminalState{input: "flash boot"}
	paths := []string{`C:\My files\测试 image.img`, `C:\folder\trailing\`}
	term.appendFiles(paths)
	args, err := parseTerminalArgs(term.input, "Fastboot")
	want := append([]string{"flash", "boot"}, paths...)
	if err != nil || !reflect.DeepEqual(args, want) {
		t.Fatalf("%q => %#v: %v", term.input, args, err)
	}
}
func TestTerminalKeyboardHistory(t *testing.T) {
	a := &app{engine: newEngine(), page: "terminal", terminal: terminalState{kind: "ADB", serial: "a"}, devices: []device{{Kind: "ADB", Serial: "a", State: "device"}}}
	a.terminal.rememberCommand("shell id")
	a.terminal.rememberCommand("get-state")
	a.terminal.input = "draft"
	a.terminal.output = "output"
	tt := ui.NewTester(a.view, 960, 680)
	if err := tt.Click("命令"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyUp)
	if a.terminal.input != "get-state" {
		t.Fatalf("up: %q", a.terminal.input)
	}
	tt.Key(0, ui.KeyUp)
	if a.terminal.input != "shell id" {
		t.Fatalf("up again: %q", a.terminal.input)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if a.terminal.input != "draft" {
		t.Fatalf("draft: %q", a.terminal.input)
	}
	tt.Key(ui.Ctrl, ui.KeyL)
	if a.terminal.output != "" {
		t.Fatal("clear shortcut failed")
	}
}

func TestTerminalFileTransfer(t *testing.T) {
	data, err := transfer.FileData(`C:\My files\测试.img`)
	if err != nil {
		t.Fatal(err)
	}
	term := terminalState{kind: "Fastboot", input: "flash boot"}
	if err := term.receiveFiles(data); err != nil {
		t.Fatal(err)
	}
	args, err := parseTerminalArgs(term.input, "Fastboot")
	if err != nil || !reflect.DeepEqual(args, []string{"flash", "boot", `C:\My files\测试.img`}) {
		t.Fatalf("%v %v", args, err)
	}
	term = terminalState{kind: "ADB", mode: 1, input: "ls"}
	if term.receiveFiles(data) == nil || term.input != "ls" {
		t.Fatal("device shell accepted host path")
	}
}

func TestTerminalResponsiveLayout(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {960, 680}, {1200, 900}} {
		a := &app{engine: newEngine(), page: "terminal", terminal: terminalState{kind: "ADB", serial: "a"}, devices: []device{{Kind: "ADB", Serial: "a", State: "device"}}}
		tt := ui.NewTester(a.view, size[0], size[1])
		input, ok := tt.Find("命令")
		if !ok {
			t.Fatal("no input")
		}
		output, ok := tt.Find("命令输出")
		if !ok {
			t.Fatal("no output")
		}
		if input.Y < float32(size[1])-90 || input.Y+input.H > float32(size[1]) || output.H < float32(size[1])-280 {
			t.Errorf("%v input=%+v output=%+v", size, input, output)
		}
	}
}
func TestMirrorSettingsAreOnDemand(t *testing.T) {
	a := &app{engine: newEngine(), page: "devices", size: "1280", fps: "30", bitrate: "8 Mbps", audio: true}
	tt := ui.NewTester(a.view, 960, 680)
	if tt.HasText("转发声音") {
		t.Fatal("settings visible in device list")
	}
	if err := tt.Click("应用设置"); err != nil {
		t.Fatal(err)
	}
	if a.page != "settings" {
		t.Fatalf("settings page not opened: %s", a.page)
	}
	for _, label := range []string{"转发声音", "分辨率上限", "帧率", "视频码率", "检查并下载组件"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("missing %s", label)
		}
	}
}

func TestTerminalPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_TERMINAL_PREVIEW")
	if path == "" {
		return
	}
	a := &app{engine: newEngine(), page: "terminal", terminal: terminalState{kind: "ADB", serial: "adb-example._adb-tls-connect._tcp"}, devices: []device{{Kind: "ADB", Serial: "adb-example._adb-tls-connect._tcp", State: "device"}}}
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

func TestMirrorSettingsPreview(t *testing.T) {
	path := os.Getenv("ANDROID_TOOLS_MIRROR_PREVIEW")
	if path == "" {
		return
	}
	a := &app{engine: newEngine(), page: "devices", size: "1280", fps: "30", bitrate: "8 Mbps", audio: true, devices: []device{{Kind: "ADB", Serial: "example", State: "device", Model: "Pixel"}}}
	tt := ui.NewTester(a.view, 960, 680)
	if err := tt.Click("应用设置"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsChangeQueuesNewValue(t *testing.T) {
	a := &app{engine: newEngine(), page: "settings", audio: true, size: "1280", fps: "30", preferencesQueue: make(chan preferences, 1)}
	tt := ui.NewTester(a.view, 960, 680)
	if err := tt.Click("转发声音"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-a.preferencesQueue:
		if got.Audio {
			t.Fatalf("old setting queued: %+v", got)
		}
	default:
		t.Fatal("setting not queued")
	}
}
