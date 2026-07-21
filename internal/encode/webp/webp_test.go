package webp

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deepteams/webp/animation"
)

func TestInProcessAnimationPreservesMillisecondDurations(t *testing.T) {
	var encoded bytes.Buffer
	encoder := NewInProcessEncoder()
	animationEncoder, err := encoder.NewAnimation(&encoded, 2, 1, Options{Quality: 88, Method: 0})
	if err != nil {
		t.Fatalf("NewAnimation failed: %v", err)
	}

	for i, durationMS := range []int{33, 34, 33} {
		frame := image.NewNRGBA(image.Rect(0, 0, 2, 1))
		frame.SetNRGBA(i%2, 0, color.NRGBA{R: uint8(80 + i*50), G: uint8(30 + i*20), B: 200, A: 255})
		if err := animationEncoder.AddFrame(frame, durationMS); err != nil {
			t.Fatalf("AddFrame %d failed: %v", i, err)
		}
	}
	if err := animationEncoder.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	decoded, err := animation.DecodeBytes(encoded.Bytes())
	if err != nil {
		t.Fatalf("decode animation: %v", err)
	}
	if got, want := len(decoded.Frames), 3; got != want {
		t.Fatalf("frame count = %d, want %d", got, want)
	}
	for i, wantMS := range []int{33, 34, 33} {
		if got := decoded.Frames[i].Duration; got != time.Duration(wantMS)*time.Millisecond {
			t.Errorf("frame %d duration = %v, want %dms", i, got, wantMS)
		}
	}
	if got, want := decoded.TotalDuration(), 100*time.Millisecond; got != want {
		t.Fatalf("total duration = %v, want %v", got, want)
	}
}

func TestInProcessAnimationPreservesAlpha(t *testing.T) {
	wantAlpha := [][]uint8{{0, 128, 255}, {255, 0, 64}}
	var encoded bytes.Buffer
	encoder := NewInProcessEncoder()
	animationEncoder, err := encoder.NewAnimation(&encoded, 3, 1, Options{Quality: 88, Method: 0})
	if err != nil {
		t.Fatalf("NewAnimation failed: %v", err)
	}
	for frameIndex, alphas := range wantAlpha {
		frame := image.NewNRGBA(image.Rect(0, 0, 3, 1))
		for x, alpha := range alphas {
			frame.SetNRGBA(x, 0, color.NRGBA{R: uint8(50 + frameIndex*100), G: uint8(40 + x*50), B: 190, A: alpha})
		}
		if err := animationEncoder.AddFrame(frame, 25); err != nil {
			t.Fatalf("AddFrame %d failed: %v", frameIndex, err)
		}
	}
	if err := animationEncoder.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	parsed, err := animation.DecodeBytes(encoded.Bytes())
	if err != nil {
		t.Fatalf("decode animation: %v", err)
	}
	if err := parsed.DecodeFrames(); err != nil {
		t.Fatalf("decode animation pixels: %v", err)
	}
	decoder, err := animation.NewAnimDecoder(parsed)
	if err != nil {
		t.Fatalf("create animation decoder: %v", err)
	}
	for frameIndex, alphas := range wantAlpha {
		decoded, _, err := decoder.NextFrame()
		if err != nil {
			t.Fatalf("decode frame %d: %v", frameIndex, err)
		}
		for x, want := range alphas {
			got := decoded.NRGBAAt(x, 0).A
			if got != want {
				t.Errorf("frame %d alpha[%d] = %d, want %d", frameIndex, x, got, want)
			}
		}
	}
}

func TestInProcessAnimationUsesEvenOffsetDeltaFrames(t *testing.T) {
	const width, height = 64, 48
	first := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			first.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 5), B: 140, A: 255})
		}
	}
	second := image.NewNRGBA(first.Bounds())
	copy(second.Pix, first.Pix)
	for y := 17; y < 20; y++ {
		for x := 31; x < 34; x++ {
			second.SetNRGBA(x, y, color.NRGBA{})
		}
	}
	second.SetNRGBA(34, 20, color.NRGBA{R: 240, G: 30, B: 80, A: 93})

	var encoded bytes.Buffer
	animationEncoder, err := NewInProcessEncoder().NewAnimation(&encoded, width, height, Options{Quality: 100, Method: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []struct {
		image    image.Image
		duration int
	}{{first, 20}, {second, 25}, {second, 35}} {
		if err := animationEncoder.AddFrame(frame.image, frame.duration); err != nil {
			t.Fatal(err)
		}
	}
	if err := animationEncoder.Close(); err != nil {
		t.Fatal(err)
	}

	parsed, err := animation.DecodeBytes(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(parsed.Frames); got != 2 {
		t.Fatalf("encoded frame count = %d, want 2 after merging the identical frame", got)
	}
	delta := parsed.Frames[1]
	if delta.OffsetX%2 != 0 || delta.OffsetY%2 != 0 {
		t.Fatalf("delta offset = (%d,%d), want even coordinates", delta.OffsetX, delta.OffsetY)
	}
	if delta.Duration != 60*time.Millisecond {
		t.Fatalf("merged delta duration = %v, want 60ms", delta.Duration)
	}
	if err := parsed.DecodeFrames(); err != nil {
		t.Fatal(err)
	}
	if parsed.Frames[1].Image.Bounds().Dx() >= width || parsed.Frames[1].Image.Bounds().Dy() >= height {
		t.Fatalf("delta frame was not cropped: %v", parsed.Frames[1].Image.Bounds())
	}
	decoder, err := animation.NewAnimDecoder(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.NextFrame(); err != nil {
		t.Fatal(err)
	}
	decoded, _, err := decoder.NextFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.NRGBAAt(31, 17).A; got != 0 {
		t.Fatalf("cleared pixel alpha = %d, want 0", got)
	}
	if got := decoded.NRGBAAt(34, 20).A; got != 93 {
		t.Fatalf("semi-transparent pixel alpha = %d, want 93", got)
	}
}

func TestLibWebPDecodesInProcessOutput(t *testing.T) {
	webpmuxPath, err := exec.LookPath("webpmux")
	if err != nil {
		t.Skip("webpmux reference tool is unavailable")
	}
	dwebpPath, err := exec.LookPath("dwebp")
	if err != nil {
		t.Skip("dwebp reference tool is unavailable")
	}

	frames := make([]*image.NRGBA, 3)
	for i := range frames {
		frames[i] = image.NewNRGBA(image.Rect(0, 0, 32, 24))
		for y := 4 + i; y < 18+i; y++ {
			for x := 3 + i*4; x < 17+i*4; x++ {
				frames[i].SetNRGBA(x, y, color.NRGBA{R: uint8(70 + i*70), G: uint8(x * 8), B: 210, A: uint8(100 + (x+y)%156)})
			}
		}
	}

	encoder := NewInProcessEncoder()
	var still bytes.Buffer
	if err := encoder.EncodeStill(&still, frames[0], Options{Quality: 88, Method: 0}); err != nil {
		t.Fatalf("EncodeStill failed: %v", err)
	}
	dir := t.TempDir()
	stillPath := filepath.Join(dir, "still.webp")
	if err := os.WriteFile(stillPath, still.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(dwebpPath, stillPath, "-pam", "-o", filepath.Join(dir, "still.pam")).CombinedOutput(); err != nil {
		t.Fatalf("libwebp could not decode still output: %v: %s", err, output)
	}

	var animated bytes.Buffer
	animationEncoder, err := encoder.NewAnimation(&animated, 32, 24, Options{Quality: 88, Method: 0})
	if err != nil {
		t.Fatalf("NewAnimation failed: %v", err)
	}
	for i, durationMS := range []int{33, 34, 33} {
		if err := animationEncoder.AddFrame(frames[i], durationMS); err != nil {
			t.Fatalf("AddFrame %d failed: %v", i, err)
		}
	}
	if err := animationEncoder.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	animationPath := filepath.Join(dir, "animation.webp")
	if err := os.WriteFile(animationPath, animated.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := exec.Command(webpmuxPath, "-info", animationPath).CombinedOutput()
	if err != nil {
		t.Fatalf("libwebp could not inspect animation output: %v: %s", err, info)
	}
	var durations []string
	for _, line := range strings.Split(string(info), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 7 && fields[0][0] >= '0' && fields[0][0] <= '9' && strings.HasSuffix(fields[0], ":") {
			durations = append(durations, fields[6])
		}
	}
	if got, want := strings.Join(durations, ","), "33,34,33"; got != want {
		t.Errorf("webpmux durations = %q, want %q:\n%s", got, want, info)
	}
	for frame := 1; frame <= 3; frame++ {
		framePath := filepath.Join(dir, fmt.Sprintf("frame-%d.webp", frame))
		if output, err := exec.Command(webpmuxPath, "-get", "frame", fmt.Sprint(frame), animationPath, "-o", framePath).CombinedOutput(); err != nil {
			t.Fatalf("webpmux could not extract frame %d: %v: %s", frame, err, output)
		}
		if output, err := exec.Command(dwebpPath, framePath, "-pam", "-o", filepath.Join(dir, fmt.Sprintf("frame-%d.pam", frame))).CombinedOutput(); err != nil {
			t.Fatalf("libwebp could not decode frame %d: %v: %s", frame, err, output)
		}
	}
}

func TestConcurrentAnimationsCanUseDifferentMethodsAtSameQuality(t *testing.T) {
	encoder := NewInProcessEncoder()
	var fastOutput bytes.Buffer
	fast, err := encoder.NewAnimation(&fastOutput, 64, 64, Options{Quality: 88, Method: 0})
	if err != nil {
		t.Fatalf("create fast animation: %v", err)
	}
	defer fast.Close()

	var compactOutput bytes.Buffer
	compact, err := encoder.NewAnimation(&compactOutput, 64, 64, Options{Quality: 88, Method: 6})
	if err != nil {
		t.Fatalf("create compact animation concurrently: %v", err)
	}
	defer compact.Close()

	frame := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			frame.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: uint8((x ^ y) * 4), A: uint8(128 + (x+y)%128)})
		}
	}
	for i, animationEncoder := range []AnimationEncoder{fast, compact} {
		if err := animationEncoder.AddFrame(frame, 25); err != nil {
			t.Fatalf("add frame to encoder %d: %v", i, err)
		}
	}
	if err := fast.Close(); err != nil {
		t.Fatalf("close fast encoder: %v", err)
	}
	if err := compact.Close(); err != nil {
		t.Fatalf("close compact encoder: %v", err)
	}
	if bytes.Equal(fastOutput.Bytes(), compactOutput.Bytes()) {
		t.Fatal("method 0 and method 6 produced identical output; method setting was not applied")
	}
}

func TestNewAnimationRejectsNilWriter(t *testing.T) {
	encoder := NewInProcessEncoder()
	if _, err := encoder.NewAnimation(nil, 2, 2, Options{Quality: 88, Method: 0}); err == nil {
		t.Fatal("NewAnimation accepted a nil writer")
	}
}

func TestAnimationWorkerCountHonorsMemoryBudget(t *testing.T) {
	if got := animationWorkerCount(2048, 1537, 18); got != 3 {
		t.Fatalf("2048px animation workers = %d, want 3", got)
	}
	if got := animationWorkerCount(768, 577, 18); got != 8 {
		t.Fatalf("768px animation workers = %d, want 8", got)
	}
	if got := animationWorkerCount(2048, 1537, 2); got != 2 {
		t.Fatalf("two-core animation workers = %d, want 2", got)
	}
}
