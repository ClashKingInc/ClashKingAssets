//go:build darwin && cgo

package hevc

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestAVFoundationHEVC(t *testing.T) {
	const width, height = 256, 256
	path := filepath.Join(t.TempDir(), "animation.mov")
	encoder, err := New(path, width, height, Options{Quality: 80, RequireHardware: true})
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("hardware encoder unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer encoder.Abort()
	for frameIndex, duration := range []int{33, 34, 33} {
		frame := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := range height {
			for x := range width {
				alpha := uint8(x)
				if frameIndex == 1 {
					alpha = uint8(y)
				}
				if frameIndex == 2 && (x+y)%7 == 0 {
					alpha = 0
				}
				frame.SetNRGBA(x, y, color.NRGBA{uint8(x + frameIndex*67), uint8(y + frameIndex*31), uint8(x + y + frameIndex*83), alpha})
			}
		}
		if err := encoder.AddFrame(frame, duration); errors.Is(err, ErrUnavailable) {
			t.Skipf("hardware encoder unavailable: %v", err)
		} else if err != nil {
			t.Fatalf("AddFrame %d: %v", frameIndex, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination exists before Close: %v", err)
	}
	result, err := encoder.Close()
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("hardware encoder unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Frames != 3 || result.DurationMS != 100 {
		t.Errorf("result=%+v", result)
	}
	if !result.HardwareAccelerated || !result.HardwareAccelerationKnown {
		t.Errorf("hardware result=%+v", result)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o644 {
		t.Errorf("mode=%#o want 0644", info.Mode().Perm())
	}
	inspection, err := inspectFile(path)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if inspection.codec != fourCC("hvc1") {
		t.Errorf("codec=%#x want hvc1", inspection.codec)
	}
	if inspection.frames != 3 || inspection.durationMS != 100 {
		t.Errorf("inspection=%+v", inspection)
	}
	t.Logf("hardware HEVC: %d frames, %dms", result.Frames, result.DurationMS)
}

func BenchmarkAVFoundationHEVC512(b *testing.B) {
	const width, height, frameCount = 512, 512, 30
	frames := [2]*image.NRGBA{image.NewNRGBA(image.Rect(0, 0, width, height)), image.NewNRGBA(image.Rect(0, 0, width, height))}
	for index, frame := range frames {
		for y := range height {
			for x := range width {
				frame.SetNRGBA(x, y, color.NRGBA{uint8(x + index*37), uint8(y + index*71), uint8(x ^ y), uint8(x + y + index*97)})
			}
		}
	}
	b.SetBytes(width * height * 4 * frameCount)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		path := filepath.Join(b.TempDir(), "benchmark.mov")
		encoder, err := New(path, width, height, Options{Quality: 80, RequireHardware: true})
		if errors.Is(err, ErrUnavailable) {
			b.Skipf("hardware encoder unavailable: %v", err)
		}
		if err != nil {
			b.Fatal(err)
		}
		for index := range frameCount {
			if err := encoder.AddFrame(frames[index%2], 33); err != nil {
				encoder.Abort()
				b.Fatal(err)
			}
		}
		if _, err := encoder.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func fourCC(value string) uint32 {
	return uint32(value[0])<<24 | uint32(value[1])<<16 | uint32(value[2])<<8 | uint32(value[3])
}
