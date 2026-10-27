// Package appicon renders the embedded Lucide smartphone icon for Windows.
package appicon

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/png"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed icon.svg
var source []byte

//go:embed LUCIDE-LICENSE.txt
var License []byte

func PNG(size int) ([]byte, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(source))
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(size, size, scanner), 1)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func ICO() ([]byte, error) {
	sizes := []int{16, 32, 48, 256}
	data := make([][]byte, len(sizes))
	for i, s := range sizes {
		b, err := PNG(s)
		if err != nil {
			return nil, err
		}
		data[i] = b
	}
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))
	offset := uint32(6 + 16*len(sizes))
	for i, s := range sizes {
		dim := byte(s % 256)
		out.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))
		_ = binary.Write(&out, binary.LittleEndian, uint16(32))
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(data[i])))
		_ = binary.Write(&out, binary.LittleEndian, offset)
		offset += uint32(len(data[i]))
	}
	for _, b := range data {
		out.Write(b)
	}
	return out.Bytes(), nil
}
