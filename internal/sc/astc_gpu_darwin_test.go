//go:build darwin && cgo

package sc

import (
	"bytes"
	"image/color"
	"strings"
	"sync"
	"testing"
)

var solidASTC4x4Block = []byte{
	0xfc, 0xfd, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0x22, 0x22, 0x66, 0x66, 0xaa, 0xaa, 0xff, 0xff,
}

func TestDecodeASTCMetalSolidColor(t *testing.T) {
	img, available, err := decodeASTCGPU(solidASTC4x4Block, 4, 4, 4, 4, false)
	if !available {
		t.Fatal("decodeASTCGPU() available = false on macOS with cgo")
	}
	requireMetalASTC(t, err)
	want := color.NRGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff}
	for y := range 4 {
		for x := range 4 {
			if got := img.NRGBAAt(x, y); got != want {
				t.Fatalf("decodeASTCGPU() pixel (%d,%d) = %#v, want %#v", x, y, got, want)
			}
		}
	}
}

func TestDecodeASTCMetalPreservesOrientation(t *testing.T) {
	data := []byte{
		0x51, 0x88, 0x03, 0x10, 0xf8, 0x01, 0xe0, 0x07,
		0x80, 0xff, 0xff, 0x81, 0x5f, 0x56, 0x40, 0x56,
	}
	img, _, err := decodeASTCGPU(data, 4, 4, 4, 4, false)
	requireMetalASTC(t, err)
	red := color.NRGBA{R: 255, A: 255}
	green := color.NRGBA{G: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	want := [4][4]color.NRGBA{
		{red, red, green, green},
		{red, red, green, green},
		{blue, blue, white, white},
		{blue, blue, white, white},
	}
	for y := range 4 {
		for x := range 4 {
			if got := img.NRGBAAt(x, y); got != want[y][x] {
				t.Fatalf("decodeASTCGPU() pixel (%d,%d) = %#v, want %#v", x, y, got, want[y][x])
			}
		}
	}
}

func TestDecodeASTCMetalSupportsSCTXBlockSizes(t *testing.T) {
	for _, block := range [][2]byte{{4, 4}, {5, 4}, {5, 5}, {6, 5}, {6, 6}, {8, 5}, {8, 6}, {8, 8}, {10, 5}, {10, 6}, {10, 8}, {10, 10}, {12, 10}, {12, 12}} {
		for _, srgb := range []bool{false, true} {
			img, _, err := decodeASTCGPU(solidASTC4x4Block, int(block[0]), int(block[1]), block[0], block[1], srgb)
			requireMetalASTC(t, err)
			if got := img.NRGBAAt(0, 0); got != (color.NRGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff}) {
				t.Fatalf("decodeASTCGPU(%dx%d, srgb=%t) pixel = %#v", block[0], block[1], srgb, got)
			}
		}
	}
}

func TestDecodeASTCMetalConcurrent(t *testing.T) {
	const workers = 16
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := decodeASTCGPU(solidASTC4x4Block, 4, 4, 4, 4, false)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		requireMetalASTC(t, err)
	}
}

func requireMetalASTC(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "no Metal device is available") {
		t.Skip("Metal device is unavailable in the process sandbox")
	}
	t.Fatalf("decodeASTCGPU() error = %v", err)
}

func BenchmarkDecodeASTCMetal512(b *testing.B) {
	data := bytes.Repeat(solidASTC4x4Block, 128*128)
	_, _, err := decodeASTCGPU(data, 512, 512, 4, 4, false)
	requireMetalASTC(b, err)
	b.SetBytes(512 * 512 * 4)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := decodeASTCGPU(data, 512, 512, 4, 4, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeASTCExternal512(b *testing.B) {
	if _, err := findASTCEnc(); err != nil {
		b.Skip(err)
	}
	data := bytes.Repeat(solidASTC4x4Block, 128*128)
	b.SetBytes(512 * 512 * 4)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := decodeASTCLevelExternal(data, 512, 512, 4, 4, false); err != nil {
			b.Fatal(err)
		}
	}
}
