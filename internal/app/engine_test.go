package app

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestValidAddress(t *testing.T) {
	for _, tt := range []struct {
		address string
		valid   bool
	}{
		{"192.168.1.2:37123", true}, {"[fd00::1]:1234", true}, {"127.0.0.1:5555", false}, {"8.8.8.8:5555", false}, {"192.168.1.2:0", false}, {"192.168.1.2:65536", false}, {"192.168.1.2:abc", false}, {"example.com:5555", false},
	} {
		if got := validAddress(tt.address) == nil; got != tt.valid {
			t.Errorf("%q: valid=%v", tt.address, got)
		}
	}
}
func TestIsolatedOfficialADB(t *testing.T) {
	tool := os.Getenv("ANDROID_TOOLS_TEST_ADB")
	if tool == "" {
		t.Skip("set ANDROID_TOOLS_TEST_ADB to official adb.exe")
	}
	t.Setenv("ADB_MDNS_AUTO_CONNECT", "0")
	t.Setenv("ADB_LOCAL_TRANSPORT_MAX_PORT", "0")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	group, err := newProcessGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	e := &engine{adb: tool, socket: "tcp:" + addr, serverUSB: "android-tools-no-usb", group: group, mirrors: map[string]*exec.Cmd{}}
	if err = e.start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan []device, 1)
	go e.trackADB(ctx, func(ds []device) {
		select {
		case events <- ds:
		default:
		}
	})
	select {
	case ds := <-events:
		if len(ds) != 0 {
			t.Fatalf("unexpected tracked devices: %+v", ds)
		}
	case <-time.After(5 * time.Second):
		cancel()
		e.stop()
		t.Fatal("ADB 设备监听没有返回初始状态")
	}
	cancel()
	ds, err := e.list()
	if err != nil || len(ds) != 0 {
		e.stop()
		t.Fatalf("devices=%+v err=%v", ds, err)
	}
	e.stop()
	if _, err = e.run(tool, 2*time.Second, "", "devices"); err == nil {
		t.Fatal("client restarted a stopped server")
	}
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		c.Close()
		t.Fatal("server still listening")
	}
}
func TestChildProcess(t *testing.T) {
	if os.Getenv("ANDROID_TOOLS_TEST_CHILD") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
func TestProcessGroupTerminatesChild(t *testing.T) {
	g, err := newProcessGroup()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$")
	cmd.Env = append(os.Environ(), "ANDROID_TOOLS_TEST_CHILD=1")
	if err = cmd.Start(); err != nil {
		g.close()
		t.Fatal(err)
	}
	if err = g.attach(cmd.Process); err != nil {
		cmd.Process.Kill()
		g.close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	g.close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("job did not terminate child")
	}
}
func TestFindToolPrefersUnifiedDirectory(t *testing.T) {
	e := newEngine()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if e.adb != filepath.Join(filepath.Dir(exe), "android-tools", "adb.exe") || e.scrcpy != filepath.Join(filepath.Dir(exe), "android-tools", "scrcpy.exe") {
		t.Fatal("unexpected tool path")
	}
}
