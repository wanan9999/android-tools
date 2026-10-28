package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesShareRegistryWithoutOverwritingDevices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	r, err := openRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.save(savedDevice{Hardware: "device"}); err != nil {
		t.Fatal(err)
	}
	want := preferences{Audio: false, Size: "1920", FPS: "60", Bitrate: "12 Mbps"}
	if err = r.savePreferences(want); err != nil {
		t.Fatal(err)
	}
	if err = r.save(savedDevice{Hardware: "second"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := openRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.preferences(); got != want {
		t.Fatalf("preferences: %+v", got)
	}
	if len(loaded.snapshot()) != 2 {
		t.Fatal("device records lost")
	}
	if err = loaded.savePreferences(preferences{Size: "invalid", FPS: "30"}); err == nil {
		t.Fatal("invalid preferences accepted")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"devices":[],"preferences":{"size":"invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openRegistry(path); err == nil {
		t.Fatal("invalid preferences loaded")
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
