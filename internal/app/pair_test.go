package app

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type pairHarness struct {
	mu       sync.Mutex
	service  string
	paired   bool
	events   chan string
	m        *manager
	engine   *engine
	registry *registry
}

func newPairHarness(t *testing.T, behavior func(string, string) (string, error)) (*pairHarness, string) {
	return newPairHarnessWithSaved(t, behavior, nil)
}
func newPairHarnessWithSaved(t *testing.T, behavior func(string, string) (string, error), saved *savedDevice) (*pairHarness, string) {
	t.Helper()
	r, e := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	if e != nil {
		t.Fatal(e)
	}
	if saved != nil {
		if err := r.save(*saved); err != nil {
			t.Fatal(err)
		}
	}
	h := &pairHarness{registry: r, events: make(chan string, 20)}
	h.engine = &engine{adb: "adb"}
	h.engine.execute = func(_ string, _ time.Duration, input string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		if cmd == "mdns services" {
			h.mu.Lock()
			defer h.mu.Unlock()
			if !h.paired {
				return h.service + ". _adb-tls-pairing._tcp. 192.168.1.4:1111", nil
			}
			return "", nil
		}
		if strings.HasPrefix(cmd, "pair ") {
			h.mu.Lock()
			h.paired = true
			h.mu.Unlock()
			return "Successfully paired [guid=adb-guid]", nil
		}
		return behavior(cmd, input)
	}
	h.m = newManager(h.engine, r)
	h.m.pairInterval = 10 * time.Millisecond
	payload, e := h.m.beginQR(func(state, _ string) { h.events <- state })
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(payload, ";")
	h.mu.Lock()
	h.service = strings.TrimPrefix(parts[1], "S:")
	h.mu.Unlock()
	t.Cleanup(h.m.cancelPair)
	return h, payload
}
func awaitPairState(t *testing.T, h *pairHarness, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-h.events:
			if state == want {
				return
			}
			if state == "error" || state == "expired" {
				t.Fatalf("unexpected pair state %s", state)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}
func TestQRUsesExistingAuthenticatedTransport(t *testing.T) {
	h, _ := newPairHarness(t, func(cmd, _ string) (string, error) {
		switch {
		case cmd == "devices -l":
			return "adb-guid._adb-tls-connect._tcp. device model:Pixel", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", ""), nil
		case strings.HasPrefix(cmd, "connect "):
			return "", errors.New("should use existing transport")
		}
		return "", nil
	})
	awaitPairState(t, h, "connected")
	d := h.registry.snapshot()
	if len(d) != 1 || d[0].Hardware != "phone1" || d[0].GUID != "adb-guid" {
		t.Fatal(d)
	}
}
func TestQRUsesKnownAddressWithoutBroadcast(t *testing.T) {
	saved := &savedDevice{Hardware: "phone1", GUID: "adb-guid", Address: "192.168.1.4:2222"}
	h, _ := newPairHarnessWithSaved(t, func(cmd, _ string) (string, error) {
		switch {
		case cmd == "devices -l":
			return "", nil
		case strings.HasPrefix(cmd, "connect "):
			return "connected to 192.168.1.4:2222", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", ""), nil
		}
		return "", nil
	}, saved)
	awaitPairState(t, h, "connected")
	d := h.registry.snapshot()
	if len(d) != 1 || d[0].Hardware != "phone1" {
		t.Fatal(d)
	}
}
func TestQRManualAddressCompletesSameSession(t *testing.T) {
	h, _ := newPairHarness(t, func(cmd, _ string) (string, error) {
		switch {
		case cmd == "devices -l":
			return "", nil
		case strings.HasPrefix(cmd, "connect "):
			return "connected to 192.168.1.4:2222", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", "adb-guid"), nil
		}
		return "", nil
	})
	awaitPairState(t, h, "connecting")
	for _, bad := range []string{"192.168.1.4:1111", "192.168.1.5:2222"} {
		if err := h.m.submitQRAddress(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if err := h.m.submitQRAddress("192.168.1.4:2222"); err != nil {
		t.Fatal(err)
	}
	awaitPairState(t, h, "connected")
	d := h.registry.snapshot()
	if len(d) != 1 || d[0].Address != "192.168.1.4:2222" {
		t.Fatal(d)
	}
}
func TestPairCancellationPreventsLateRegistration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h, _ := newPairHarness(t, func(cmd, _ string) (string, error) {
		switch {
		case cmd == "devices -l":
			return "", nil
		case strings.HasPrefix(cmd, "connect "):
			close(started)
			<-release
			return "connected to 192.168.1.4:2222", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", "adb-guid"), nil
		}
		return "", nil
	})
	awaitPairState(t, h, "connecting")
	if err := h.m.submitQRAddress("192.168.1.4:2222"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("connection did not start")
	}
	h.m.cancelPair()
	close(release)
	time.Sleep(50 * time.Millisecond)
	if len(h.registry.snapshot()) != 0 {
		t.Fatal("cancelled pairing registered a device")
	}
}
func TestConnectKnownChecksOriginalHardware(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	if err := r.save(savedDevice{Hardware: "original", GUID: "adb-guid", Address: "192.168.1.4:2222"}); err != nil {
		t.Fatal(err)
	}
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "devices -l":
			return "", nil
		case strings.HasPrefix(cmd, "connect "):
			return "connected to 192.168.1.4:3333", nil
		case strings.Contains(cmd, "getprop"):
			return props("different", "adb-guid"), nil
		}
		return "", nil
	}}
	m := newManager(e, r)
	if err := m.connectKnown("192.168.1.4:3333", "original"); err == nil {
		t.Fatal("different phone accepted")
	}
	if d := r.snapshot(); len(d) != 1 || d[0].Hardware != "original" {
		t.Fatal(d)
	}
}
func TestQRUsesExistingIPTransport(t *testing.T) {
	h, _ := newPairHarness(t, func(cmd, _ string) (string, error) {
		switch {
		case cmd == "devices -l":
			return "192.168.1.4:2222 device model:Pixel", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", "adb-guid"), nil
		case strings.HasPrefix(cmd, "connect "):
			return "", errors.New("should use existing IP transport")
		}
		return "", nil
	})
	awaitPairState(t, h, "connected")
	d := h.registry.snapshot()
	if len(d) != 1 || d[0].Address != "192.168.1.4:2222" {
		t.Fatal(d)
	}
}
func TestQRManualFallbackView(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	a := &app{engine: newEngine(), page: "pair", qrActive: true, qrConnecting: true, qrState: "配对成功"}
	a.manager = newManager(a.engine, r)
	tt := ui.NewTester(a.view, 640, 480)
	for _, s := range []string{"连接地址", "连接手机", "取消"} {
		if _, ok := tt.Find(s); !ok {
			t.Errorf("missing %s", s)
		}
	}
}
func TestWrongAlreadyConnectedDeviceIsNotDisconnected(t *testing.T) {
	disconnected := false
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "devices -l":
			return "192.168.1.4:3333 device", nil
		case strings.HasPrefix(cmd, "connect "):
			return "already connected to 192.168.1.4:3333", nil
		case strings.Contains(cmd, "getprop"):
			return props("other", "adb-guid"), nil
		case strings.HasPrefix(cmd, "disconnect "):
			disconnected = true
		}
		return "", nil
	}}
	if _, err := e.connectVerified("192.168.1.4:3333", "adb-guid", "original"); err == nil {
		t.Fatal("wrong device accepted")
	}
	if disconnected {
		t.Fatal("unrelated existing connection was disconnected")
	}
}
func TestAlreadyPairedWirelessDeviceIsSaved(t *testing.T) {
	r, _ := openRegistry(filepath.Join(t.TempDir(), "devices.json"))
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "devices -l":
			return "adb-guid._adb-tls-connect._tcp. device model:Pixel", nil
		case cmd == "mdns services":
			return "adb-guid _adb-tls-connect._tcp 192.168.1.4:2222", nil
		case strings.Contains(cmd, "getprop"):
			return props("phone1", ""), nil
		}
		return "", nil
	}}
	m := newManager(e, r)
	devices, _, err := m.refresh()
	if err != nil || len(devices) != 1 || devices[0].State != "device" {
		t.Fatalf("%+v %v", devices, err)
	}
	saved := r.snapshot()
	if len(saved) != 1 || saved[0].Hardware != "phone1" || saved[0].GUID != "adb-guid" || saved[0].Address != "192.168.1.4:2222" {
		t.Fatal(saved)
	}
}
