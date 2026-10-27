package appicon

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"testing"
)

func TestIconResources(t *testing.T) {
	p, err := PNG(256)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(p))
	if err != nil || image.Bounds().Dx() != 256 {
		t.Fatalf("invalid PNG: %v", err)
	}
	ico, err := ICO()
	if err != nil {
		t.Fatal(err)
	}
	if len(ico) < 70 || binary.LittleEndian.Uint16(ico[4:6]) != 4 {
		t.Fatal("invalid icon directory")
	}
	if path := os.Getenv("ANDROID_TOOLS_ICON_PREVIEW"); path != "" {
		if err := os.WriteFile(path, p, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
