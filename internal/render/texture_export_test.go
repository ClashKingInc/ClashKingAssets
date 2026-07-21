package render

import (
	"os"
	"path/filepath"
	"testing"

	"image"
	"image/color"
)

func TestExportTextureFilePreferWebP(t *testing.T) {
	original := decodeTextureFile
	decodeTextureFile = func(path string) (*image.NRGBA, error) {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
		img.SetNRGBA(1, 1, color.NRGBA{G: 255, A: 255})
		return img, nil
	}
	defer func() { decodeTextureFile = original }()

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "icon_test.sctx")
	if err := os.WriteFile(inputPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	outBase := filepath.Join(tempDir, "exports", "icon_test")

	if err := exportTextureFile(inputPath, outBase, ExportOptions{PreferWebP: true}); err != nil {
		t.Fatalf("exportTextureFile failed: %v", err)
	}
	if _, err := os.Stat(outBase + ".webp"); err != nil {
		t.Fatalf("expected webp output, stat failed: %v", err)
	}
	if _, err := os.Stat(outBase + ".png"); !os.IsNotExist(err) {
		t.Fatalf("expected no png output, got err=%v", err)
	}
}
