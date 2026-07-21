package main

import (
	"io"
	"testing"
)

func TestParseCLIExportSubcommandAcceptsFlagsAfterInput(t *testing.T) {
	config, err := parseCLI([]string{
		"export",
		"input.sc",
		"--out", "output",
		"--prefer-webp",
		"--webp-quality", "0",
		"--webp-method", "6",
		"--disable-gpu",
		"--scenery-format", "webp",
		"--hevc-quality", "90",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI failed: %v", err)
	}
	if config.command != commandExport || config.input != "input.sc" || config.outDir != "output" {
		t.Fatalf("unexpected export config: %+v", config)
	}
	if !config.opts.PreferWebP || config.opts.WebPQuality != 0 || !config.opts.WebPQualitySet {
		t.Fatalf("explicit WebP quality was lost: %+v", config.opts)
	}
	if config.opts.WebPMethod != 6 {
		t.Fatalf("WebP method = %d, want 6", config.opts.WebPMethod)
	}
	if !config.opts.DisableGPU {
		t.Fatal("--disable-gpu was lost")
	}
	if config.opts.SceneryFormat != "webp" || config.opts.HEVCQuality != 90 {
		t.Fatalf("scenery encoding options were lost: %+v", config.opts)
	}
}

func TestParseCLILegacyExportAlsoAcceptsFlagsAfterInput(t *testing.T) {
	config, err := parseCLI([]string{"input.sc", "--out", "output"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI failed: %v", err)
	}
	if config.command != commandExport || config.input != "input.sc" || config.outDir != "output" {
		t.Fatalf("unexpected legacy export config: %+v", config)
	}
}

func TestParseCLIRootSubcommandsHaveExplicitModes(t *testing.T) {
	texture, err := parseCLI([]string{"texture-root", "textures", "--delete-source"}, io.Discard)
	if err != nil {
		t.Fatalf("texture-root parse failed: %v", err)
	}
	if texture.command != commandTextureRoot || texture.input != "textures" || !texture.deleteSource {
		t.Fatalf("unexpected texture-root config: %+v", texture)
	}

	sc, err := parseCLI([]string{"sc-root", "sc", "--workers", "8", "--file-concurrency", "4"}, io.Discard)
	if err != nil {
		t.Fatalf("sc-root parse failed: %v", err)
	}
	if sc.command != commandSCRoot || sc.input != "sc" || sc.workers != 8 || sc.opts.FileConcurrency != 4 {
		t.Fatalf("unexpected sc-root config: %+v", sc)
	}
}

func TestParseCLISC3DViewer(t *testing.T) {
	config, err := parseCLI([]string{"sc3d-viewer", "--fingerprint", "abc123", "--addr", "127.0.0.1:9000"}, io.Discard)
	if err != nil {
		t.Fatalf("sc3d-viewer parse failed: %v", err)
	}
	if config.command != commandSC3DViewer || config.fingerprint != "abc123" || config.viewerAddr != "127.0.0.1:9000" {
		t.Fatalf("unexpected sc3d-viewer config: %+v", config)
	}
	if _, err := parseCLI([]string{"sc3d-viewer", "input.glb"}, io.Discard); err == nil {
		t.Fatal("expected sc3d-viewer input path to be rejected")
	}
}

func TestParseCLIRejectsInvalidWebPOptions(t *testing.T) {
	if _, err := parseCLI([]string{"export", "input.sc", "--webp-quality", "101"}, io.Discard); err == nil {
		t.Fatal("expected out-of-range WebP quality error")
	}
	if _, err := parseCLI([]string{"export", "input.sc", "--webp-method", "7"}, io.Discard); err == nil {
		t.Fatal("expected out-of-range WebP method error")
	}
}

func TestParseCLIRejectsInvalidSceneryEncodingOptions(t *testing.T) {
	if _, err := parseCLI([]string{"export", "input.sc", "--scenery-format", "gif"}, io.Discard); err == nil {
		t.Fatal("expected invalid scenery format error")
	}
	if _, err := parseCLI([]string{"export", "input.sc", "--hevc-quality", "101"}, io.Discard); err == nil {
		t.Fatal("expected out-of-range HEVC quality error")
	}
}

func TestParseCLIRejectsOutputDirectoryForSCRoot(t *testing.T) {
	if _, err := parseCLI([]string{"sc-root", "raw", "--out", "elsewhere"}, io.Discard); err == nil {
		t.Fatal("expected --out to be rejected for sc-root")
	}
}
