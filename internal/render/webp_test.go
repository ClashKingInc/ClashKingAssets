package render

import (
	"bytes"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/deepteams/webp/animation"
)

func TestWriteStillWebPWithoutExternalTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	img.SetNRGBA(2, 0, color.NRGBA{B: 255, A: 255})

	var encoded bytes.Buffer
	if err := writeStillWebP(&encoded, img, normalizeExportOptions(ExportOptions{})); err != nil {
		t.Fatalf("writeStillWebP failed without external tools: %v", err)
	}

	decoded, format, err := image.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("decode encoded WebP: %v", err)
	}
	if format != "webp" {
		t.Fatalf("decoded format = %q, want webp", format)
	}
	if got, want := decoded.Bounds(), img.Bounds(); got != want {
		t.Fatalf("decoded bounds = %v, want %v", got, want)
	}
}

func TestWriteAnimatedWebPWithoutExternalToolsPreservesTiming(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	frames := make([]renderedFrame, 3)
	for i, durationMS := range []int{33, 34, 33} {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
		img.SetNRGBA(i%2, 0, color.NRGBA{R: uint8(60 + i*70), B: 220, A: 255})
		frames[i] = renderedFrame{Image: img, DelayMS: durationMS}
	}

	var encoded bytes.Buffer
	if err := writeAnimatedWebP(&encoded, frames, normalizeExportOptions(ExportOptions{})); err != nil {
		t.Fatalf("writeAnimatedWebP failed without external tools: %v", err)
	}

	info, err := animation.DecodeBytes(encoded.Bytes())
	if err != nil {
		t.Fatalf("inspect encoded animation: %v", err)
	}
	durations := make([]int, len(info.Frames))
	for i, frame := range info.Frames {
		durations[i] = int(frame.Duration.Milliseconds())
	}
	if got, want := durations, []int{33, 34, 33}; !slices.Equal(got, want) {
		t.Fatalf("frame durations = %v, want %v", got, want)
	}
}
