package app

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestADBEventStream(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	events := make(chan []device, 2)
	done := make(chan error, 1)
	go func() { done <- readADBEvents(client, func(ds []device) { events <- ds }) }()
	request := make([]byte, 24)
	if _, err := io.ReadFull(server, request); err != nil {
		t.Fatal(err)
	}
	if string(request) != "0014host:track-devices-l" {
		t.Fatalf("request %q", request)
	}
	_, _ = server.Write([]byte("OKAY"))
	body := "adb-test (2)._adb-tls-connect._tcp. device model:Pixel\n"
	_, _ = fmt.Fprintf(server, "%04x%s", len(body), body)
	select {
	case ds := <-events:
		if len(ds) != 1 || ds[0].Serial != "adb-test (2)._adb-tls-connect._tcp." || ds[0].State != "device" {
			t.Fatalf("%+v", ds)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	_, _ = server.Write([]byte("0000"))
	select {
	case ds := <-events:
		if len(ds) != 0 {
			t.Fatal(ds)
		}
	case <-time.After(time.Second):
		t.Fatal("no disconnect event")
	}
	server.Close()
	<-done
}
func TestDisconnectUpdatesWithoutWaitingForIdentity(t *testing.T) {
	current := []device{{Serial: "wifi:123", Hardware: "phone", Kind: "ADB", State: "device"}, {Serial: "fb", Kind: "Fastboot", State: "fastboot"}}
	offline := mergeADBEvent(current, nil)
	if len(offline) != 2 || offline[0].State != "offline" || offline[1].Kind != "Fastboot" {
		t.Fatalf("%+v", offline)
	}
	online := mergeADBEvent(offline, []device{{Serial: "wifi:123", Kind: "ADB", State: "device"}})
	if online[0].State != "verifying" {
		t.Fatalf("identity was bypassed: %+v", online)
	}
}
func TestFastbootMerge(t *testing.T) {
	current := []device{{Serial: "one", Kind: "ADB", State: "device"}, {Serial: "old", Kind: "Fastboot", State: "fastboot"}}
	got := mergeFastboot(current, []device{{Serial: "new", Kind: "Fastboot", State: "fastboot"}})
	if len(got) != 2 || got[0].Serial != "one" || got[1].Serial != "new" {
		t.Fatal(got)
	}
}
