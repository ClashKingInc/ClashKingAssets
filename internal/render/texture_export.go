package render

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sc2fla/internal/sc"
)

var decodeTextureFile = sc.DecodeTextureFile

func isDirectTextureInput(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sctx", ".ktx", ".zktx":
		return true
	default:
		return false
	}
}

func exportTextureFile(inputPath, outPath string, opts ExportOptions) error {
	start := time.Now()
	img, err := decodeTextureFile(inputPath)
	if err != nil {
		return err
	}

	opts = normalizeExportOptions(opts)
	if opts.RenderScale > 1 {
		img = scaleNRGBA(img, opts.RenderScale)
	}

	assetDir := outPath
	if assetDir == "" {
		assetDir = strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_assets"
	}
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return err
	}

	outputPath := filepath.Join(assetDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".png")
	if reason := tinyOutputReason(img.Bounds(), opts.SkipTinyOutputThreshold); reason != "" {
		if err := removeIfExists(outputPath); err != nil {
			return err
		}
		fmt.Printf("Skipped %s\n", inputPath)
		fmt.Printf("  Type:    texture\n")
		fmt.Printf("  Reason:  %s\n", reason)
		fmt.Printf("  Total:   %s\n", time.Since(start).Round(time.Millisecond))
		return nil
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		return err
	}

	fmt.Printf("Finished %s\n", inputPath)
	fmt.Printf("  Type:    texture\n")
	fmt.Printf("  Output:  %s\n", outputPath)
	fmt.Printf("  Size:    %.2f MB\n", float64(info.Size())/(1024*1024))
	fmt.Printf("  Total:   %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}

func scaleNRGBA(src *image.NRGBA, factor int) *image.NRGBA {
	if factor <= 1 {
		return src
	}
	bounds := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx()*factor, bounds.Dy()*factor))
	for y := 0; y < dst.Bounds().Dy(); y++ {
		sy := y / factor
		for x := 0; x < dst.Bounds().Dx(); x++ {
			sx := x / factor
			dst.SetNRGBA(x, y, src.NRGBAAt(sx+bounds.Min.X, sy+bounds.Min.Y))
		}
	}
	return dst
}
