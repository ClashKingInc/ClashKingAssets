package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkWebPStillInProcess(b *testing.B) {
	img := webPBenchmarkStillImage(1024, 1024)
	opts := normalizeExportOptions(ExportOptions{})
	var encoded bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded.Reset()
		if err := writeStillWebP(&encoded, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encoded.Len()), "output-bytes")
	b.ReportMetric(0, "temp-bytes/op")
}

func BenchmarkWebPStillExternalReference(b *testing.B) {
	cwebpPath, err := exec.LookPath("cwebp")
	if err != nil {
		b.Skip("cwebp reference tool is unavailable")
	}
	img := webPBenchmarkStillImage(1024, 1024)
	var outputBytes int64
	var tempBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		inputPath := filepath.Join(dir, "input.png")
		input, err := os.Create(inputPath)
		if err != nil {
			b.Fatal(err)
		}
		if err := png.Encode(input, img); err != nil {
			input.Close()
			b.Fatal(err)
		}
		if err := input.Close(); err != nil {
			b.Fatal(err)
		}
		info, err := os.Stat(inputPath)
		if err != nil {
			b.Fatal(err)
		}
		tempBytes += info.Size()

		outputPath := filepath.Join(dir, "output.webp")
		if output, err := exec.Command(cwebpPath, "-quiet", "-q", "88", "-m", "0", inputPath, "-o", outputPath).CombinedOutput(); err != nil {
			b.Fatalf("cwebp failed: %v: %s", err, output)
		}
		info, err = os.Stat(outputPath)
		if err != nil {
			b.Fatal(err)
		}
		outputBytes = info.Size()
	}
	b.ReportMetric(float64(outputBytes), "output-bytes")
	b.ReportMetric(float64(tempBytes)/float64(b.N), "temp-bytes/op")
}

func BenchmarkWebPStillRealAssetInProcess(b *testing.B) {
	img := loadWebPBenchmarkAsset(b)
	opts := normalizeExportOptions(ExportOptions{})
	var encoded bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded.Reset()
		if err := writeStillWebP(&encoded, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encoded.Len()), "output-bytes")
	b.ReportMetric(0, "temp-bytes/op")
}

func BenchmarkWebPStillRealAssetExternalReference(b *testing.B) {
	cwebpPath, err := exec.LookPath("cwebp")
	if err != nil {
		b.Skip("cwebp reference tool is unavailable")
	}
	img := loadWebPBenchmarkAsset(b)
	var outputBytes int64
	var tempBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		inputPath := filepath.Join(dir, "input.png")
		input, err := os.Create(inputPath)
		if err != nil {
			b.Fatal(err)
		}
		if err := png.Encode(input, img); err != nil {
			input.Close()
			b.Fatal(err)
		}
		if err := input.Close(); err != nil {
			b.Fatal(err)
		}
		info, err := os.Stat(inputPath)
		if err != nil {
			b.Fatal(err)
		}
		tempBytes += info.Size()

		outputPath := filepath.Join(dir, "output.webp")
		if output, err := exec.Command(cwebpPath, "-quiet", "-q", "88", "-m", "0", inputPath, "-o", outputPath).CombinedOutput(); err != nil {
			b.Fatalf("cwebp failed: %v: %s", err, output)
		}
		info, err = os.Stat(outputPath)
		if err != nil {
			b.Fatal(err)
		}
		outputBytes = info.Size()
	}
	b.ReportMetric(float64(outputBytes), "output-bytes")
	b.ReportMetric(float64(tempBytes)/float64(b.N), "temp-bytes/op")
}

func BenchmarkWebPAnimationInProcess(b *testing.B) {
	frames := webPBenchmarkFrames(16, 512, 512)
	opts := normalizeExportOptions(ExportOptions{})
	var encoded bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded.Reset()
		if err := writeAnimatedWebP(&encoded, frames, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encoded.Len()), "output-bytes")
	b.ReportMetric(0, "temp-bytes/op")
}

func BenchmarkWebPAnimationExternalReference(b *testing.B) {
	img2webpPath, err := exec.LookPath("img2webp")
	if err != nil {
		b.Skip("img2webp reference tool is unavailable")
	}
	frames := webPBenchmarkFrames(16, 512, 512)
	var outputBytes int64
	var tempBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		dir := b.TempDir()
		args := []string{"-loop", "0", "-lossy", "-q", "88", "-m", "0"}
		for frameIndex, frame := range frames {
			name := fmt.Sprintf("%x.png", frameIndex)
			path := filepath.Join(dir, name)
			file, err := os.Create(path)
			if err != nil {
				b.Fatal(err)
			}
			encoder := png.Encoder{CompressionLevel: png.BestSpeed}
			if err := encoder.Encode(file, frame.Image); err != nil {
				file.Close()
				b.Fatal(err)
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				b.Fatal(err)
			}
			tempBytes += info.Size()
			args = append(args, "-d", strconv.Itoa(frame.DelayMS), name)
		}
		outputPath := filepath.Join(dir, "output.webp")
		args = append(args, "-o", outputPath)
		cmd := exec.Command(img2webpPath, args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("img2webp failed: %v: %s", err, output)
		}
		info, err := os.Stat(outputPath)
		if err != nil {
			b.Fatal(err)
		}
		outputBytes = info.Size()
	}
	b.ReportMetric(float64(outputBytes), "output-bytes")
	b.ReportMetric(float64(tempBytes)/float64(b.N), "temp-bytes/op")
}

func webPBenchmarkStillImage(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if (x/48+y/48)%5 == 0 {
				alpha = uint8((x + y) & 0xff)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*3 + y) & 0xff),
				G: uint8((x + y*2) & 0xff),
				B: uint8((x ^ y) & 0xff),
				A: alpha,
			})
		}
	}
	return img
}

func webPBenchmarkFrames(count, width, height int) []renderedFrame {
	frames := make([]renderedFrame, count)
	for frameIndex := range frames {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		minX := 24 + frameIndex*13
		minY := 96 + frameIndex*7
		for y := minY; y < minY+160; y++ {
			for x := minX; x < minX+160; x++ {
				distance := (x-minX-80)*(x-minX-80) + (y-minY-80)*(y-minY-80)
				if distance > 80*80 {
					continue
				}
				img.SetNRGBA(x, y, color.NRGBA{
					R: uint8(80 + frameIndex*7),
					G: uint8((x + y) & 0xff),
					B: 220,
					A: uint8(160 + distance%96),
				})
			}
		}
		frames[frameIndex] = renderedFrame{Image: img, DelayMS: 33 + frameIndex%2}
	}
	return frames
}

func loadWebPBenchmarkAsset(b *testing.B) image.Image {
	b.Helper()
	file, err := os.Open("../../assets/capital-base/troop-pics/Icon_CC_Mega_Troop_Mega_Sparky.png")
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		b.Fatal(err)
	}
	return img
}
