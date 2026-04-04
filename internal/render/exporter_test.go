package render

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sc2fla/internal/sc"
)

func mustLoadSWF(t *testing.T, path string) *sc.SWF {
	t.Helper()
	swf, err := sc.Load(path)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", path, err)
	}
	return swf
}

func findTarget(t *testing.T, exporter *Exporter, name string) Target {
	t.Helper()
	targets, skipped := exporter.prepareTargets()
	if len(skipped) != 0 {
		t.Fatalf("prepareTargets skipped assets unexpectedly: %+v", skipped[:minInt(len(skipped), 3)])
	}
	for _, target := range targets {
		if target.Name == name {
			return target
		}
	}
	t.Fatalf("target %q not found", name)
	return Target{}
}

func requireWebPTools(t *testing.T) {
	t.Helper()
	if _, err := lookupWebPTools(); err != nil {
		t.Skipf("webp tools unavailable: %v", err)
	}
}

func TestPrepareTargetDragonWrapper(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/chr_dragon.sc")
	exporter := NewExporter(swf)

	target := findTarget(t, exporter, "dragonx_fly1_3")
	if !target.IsWrapper {
		t.Fatalf("dragonx_fly1_3 should be treated as a wrapper export")
	}
	if target.ResolvedTimeline == target.ResourceID {
		t.Fatalf("resolved timeline should descend into the animated child")
	}
	if target.Duration <= 0 {
		t.Fatalf("wrapper duration should be positive")
	}
	if !slices.Contains(target.BindLabels, "boss_attack_pivot") {
		t.Fatalf("expected bind labels to include boss_attack_pivot, got %v", target.BindLabels)
	}
	if len(target.AncestorIDs) < 2 {
		t.Fatalf("expected ancestor path to the resolved timeline, got %v", target.AncestorIDs)
	}
}

func TestBuildingsSpellFactoryKeepsSingleTargetWithLabels(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/buildings.sc")
	exporter := NewExporter(swf)
	target := findTarget(t, exporter, "spell_factory_lvl9")
	want := []string{"idle", "production", "transition"}
	if !slices.Equal(target.FrameLabels, want) {
		t.Fatalf("spell_factory_lvl9 labels = %v, want %v", target.FrameLabels, want)
	}
}

func TestPrepareTargetsIncludePlayerHousePartSplits(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/buildings_cc.sc")
	exporter := NewExporter(swf)
	targets := mustTargets(t, exporter)

	foundPart := false
	foundBounds := false
	for _, target := range targets {
		switch target.Name {
		case "playerhouse_parts/deco_winter01":
			foundPart = true
			if target.SelectedBind != "deco_winter01" {
				t.Fatalf("selected bind = %q, want deco_winter01", target.SelectedBind)
			}
		case "playerhouse_parts/bounds":
			foundBounds = true
		}
	}
	if !foundPart {
		t.Fatal("expected split target for playerhouse_parts/deco_winter01")
	}
	if foundBounds {
		t.Fatal("did not expect a synthetic split target for playerhouse_parts/bounds")
	}
}

func TestPlayerHousePartSplitRendersSubtreeOnly(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/buildings_cc.sc")
	exporter := NewExporter(swf)

	baseTarget := findTarget(t, exporter, "playerhouse_parts")
	partTarget := findTarget(t, exporter, "playerhouse_parts/deco_winter01")

	baseFrames, _, _, _, err := exporter.renderTarget(baseTarget)
	if err != nil {
		t.Fatalf("base renderTarget failed: %v", err)
	}
	partFrames, _, _, _, err := exporter.renderTarget(partTarget)
	if err != nil {
		t.Fatalf("part renderTarget failed: %v", err)
	}
	if len(baseFrames) == 0 || len(partFrames) == 0 {
		t.Fatal("expected rendered frames for both targets")
	}

	baseBounds := baseFrames[0].Image.Bounds()
	partBounds := partFrames[0].Image.Bounds()
	if partBounds.Dx() >= baseBounds.Dx() {
		t.Fatalf("part width = %d, want less than base width %d", partBounds.Dx(), baseBounds.Dx())
	}
	if partBounds.Dy() >= baseBounds.Dy() {
		t.Fatalf("part height = %d, want less than base height %d", partBounds.Dy(), baseBounds.Dy())
	}
}

func TestWriteAnimatedWebPProducesRIFF(t *testing.T) {
	requireWebPTools(t)

	frames := []renderedFrame{
		{
			Image: func() *image.NRGBA {
				img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
				img.SetNRGBA(0, 0, color.NRGBA{})
				img.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 64, B: 32, A: 255})
				return img
			}(),
			DelayCS: 4,
		},
		{
			Image: func() *image.NRGBA {
				img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
				img.SetNRGBA(0, 0, color.NRGBA{R: 16, G: 128, B: 255, A: 255})
				img.SetNRGBA(1, 0, color.NRGBA{})
				return img
			}(),
			DelayCS: 5,
		},
	}

	var buf bytes.Buffer
	if err := writeAnimatedWebP(&buf, frames); err != nil {
		t.Fatalf("writeAnimatedWebP failed: %v", err)
	}

	data := buf.Bytes()
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Fatal("webp should start with RIFF")
	}
	if !bytes.Contains(data, []byte("WEBP")) {
		t.Fatal("webp should contain WEBP signature")
	}
}

func TestBuildImg2WebPArgsUsesFastLossyEncoding(t *testing.T) {
	args := buildImg2WebPArgs([]string{"a.png", "b.png"}, []renderedFrame{
		{DelayCS: 4},
		{DelayCS: 5},
	}, "out.webp")

	if !slices.Contains(args, "-lossy") || !slices.Contains(args, "-q") {
		t.Fatalf("expected lossy quality flags, args=%v", args)
	}
	if !slices.Contains(args, "-m") || !slices.Contains(args, "0") {
		t.Fatalf("expected fast method flag, args=%v", args)
	}
	if !slices.Contains(args, "40") || !slices.Contains(args, "50") {
		t.Fatalf("expected frame duration flags, args=%v", args)
	}
}

func TestExportDragonNamedOutputsAndManifest(t *testing.T) {
	requireWebPTools(t)

	swf := mustLoadSWF(t, "../../sc/chr_dragon.sc")
	exporter := NewExporter(swf)

	tempDir := t.TempDir()
	manifest, err := exporter.ExportAll(tempDir, 1)
	if err != nil {
		t.Fatalf("ExportAll failed: %v", err)
	}

	if got := len(manifest.Exports); got != 39 {
		t.Fatalf("manifest export count = %d, want 39", got)
	}
	if len(manifest.Skipped) != 0 {
		t.Fatalf("expected no skipped exports, got %d", len(manifest.Skipped))
	}

	files, err := os.ReadDir(filepath.Join(tempDir, "exports"))
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(files) != 39 {
		t.Fatalf("exported file count = %d, want 39", len(files))
	}
	for _, file := range files {
		name := file.Name()
		if filepath.Ext(name) != ".webp" {
			t.Fatalf("expected dragon outputs to be WEBPs, got %s", name)
		}
		if strings.HasPrefix(name, "movieclip_") || strings.HasPrefix(name, "shape_") || strings.HasPrefix(name, "resource_") {
			t.Fatalf("unexpected helper-like output name %s", name)
		}
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("manifest marshal failed: %v", err)
	}
	if len(manifestBytes) == 0 {
		t.Fatal("manifest json should not be empty")
	}

	found := false
	for _, entry := range manifest.Exports {
		if entry.ExportName == "dragonx_fly1_3" {
			found = true
			if !entry.IsWrapperExport {
				t.Fatal("dragonx_fly1_3 should be marked as a wrapper in the manifest")
			}
			if entry.OutputFile != "dragonx_fly1_3.webp" {
				t.Fatalf("unexpected output file %s", entry.OutputFile)
			}
		}
	}
	if !found {
		t.Fatal("manifest missing dragonx_fly1_3")
	}
}

func TestBattleBlimpDirectCompositeExport(t *testing.T) {
	requireWebPTools(t)

	swf := mustLoadSWF(t, "../../sc/chr_battle_blimp.sc")
	exporter := NewExporter(swf)
	target := findTarget(t, exporter, "Siege_Machine_Balloon4_hover2")
	if target.IsWrapper {
		t.Fatalf("Siege_Machine_Balloon4_hover2 should use its own root timeline")
	}
	if target.ResolvedTimeline != target.ResourceID {
		t.Fatalf("resolved timeline = %d, want root %d", target.ResolvedTimeline, target.ResourceID)
	}

	outDir := t.TempDir()
	entry, skipped, _, err := exporter.exportTarget(target, outDir, newNameAllocator(outDir))
	if err != nil {
		t.Fatalf("exportTarget failed: %v", err)
	}
	if skipped != nil {
		t.Fatalf("exportTarget unexpectedly skipped: %+v", skipped)
	}
	if filepath.Ext(entry.OutputFile) != ".webp" {
		t.Fatalf("expected a WEBP output, got %s", entry.OutputFile)
	}
}

func TestLoadingLabelsAppearInPreparedTargets(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/loading.sc")
	exporter := NewExporter(swf)
	targets, skipped := exporter.prepareTargets()
	if len(skipped) != 0 {
		t.Fatalf("prepareTargets skipped assets unexpectedly: %+v", skipped[:minInt(len(skipped), 3)])
	}

	hasBind := false
	hasFrame := false
	for _, target := range targets {
		if len(target.BindLabels) != 0 {
			hasBind = true
		}
		if len(target.FrameLabels) != 0 {
			hasFrame = true
		}
	}
	if !hasBind {
		t.Fatal("expected at least one prepared loading target to expose bind labels")
	}
	if !hasFrame {
		t.Fatal("expected at least one prepared loading target to expose frame labels")
	}
}

func TestUIWrapperChangePointsStayComposite(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/ui.sc")
	exporter := NewExporter(swf)
	target := findTarget(t, exporter, "wl_trophy_banner")
	if !target.IsWrapper {
		t.Fatal("wl_trophy_banner should be treated as a wrapper export")
	}
	if target.ResolvedTimeline == target.ResourceID {
		t.Fatal("wl_trophy_banner should resolve to an animated descendant")
	}

	changePoints := exporter.collectChangePoints(target, target.Duration)
	if len(changePoints) < 50 {
		t.Fatalf("expected many change points for wl_trophy_banner, got %d", len(changePoints))
	}
}

func TestDragonGoldenFirstFrameHash(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/chr_dragon.sc")
	exporter := NewExporter(swf)
	target := findTarget(t, exporter, "dragonx_fly1_3")
	frames, _, _, _, err := exporter.renderTarget(target)
	if err != nil {
		t.Fatalf("renderTarget failed: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("expected at least one rendered frame")
	}

	hash := sha1.Sum(frames[0].Image.Pix)
	const want = "2e09eaba617bd0272a3901943592d0ac05908970"
	if got := fmtHash(hash); got == want {
		return
	}
	t.Fatalf("golden first-frame hash = %s, want %s", fmtHash(hash), want)
}

func TestRenderScaleIncreasesOutputDimensions(t *testing.T) {
	swf := mustLoadSWF(t, "../../sc/chr_dragon.sc")
	baseExporter := NewExporter(swf)
	scaledExporter := NewExporterWithOptions(swf, ExportOptions{RenderScale: 2})
	baseTarget := findTarget(t, baseExporter, "dragonx_fly1_3")
	scaledTarget := findTarget(t, scaledExporter, "dragonx_fly1_3")

	baseFrames, _, _, _, err := baseExporter.renderTarget(baseTarget)
	if err != nil {
		t.Fatalf("base renderTarget failed: %v", err)
	}
	scaledFrames, _, _, _, err := scaledExporter.renderTarget(scaledTarget)
	if err != nil {
		t.Fatalf("scaled renderTarget failed: %v", err)
	}
	if len(baseFrames) == 0 || len(scaledFrames) == 0 {
		t.Fatal("expected rendered frames for both exporters")
	}

	baseBounds := baseFrames[0].Image.Bounds()
	scaledBounds := scaledFrames[0].Image.Bounds()
	if scaledBounds.Dx() != baseBounds.Dx()*2 {
		t.Fatalf("scaled width = %d, want %d", scaledBounds.Dx(), baseBounds.Dx()*2)
	}
	if scaledBounds.Dy() != baseBounds.Dy()*2 {
		t.Fatalf("scaled height = %d, want %d", scaledBounds.Dy(), baseBounds.Dy()*2)
	}
}

func BenchmarkParseDragon(b *testing.B) {
	for i := 0; i < b.N; i++ {
		swf, err := sc.Load("../../sc/chr_dragon.sc")
		if err != nil {
			b.Fatalf("Load failed: %v", err)
		}
		_ = swf
	}
}

func BenchmarkRenderDragonWrapper(b *testing.B) {
	swf, err := sc.Load("../../sc/chr_dragon.sc")
	if err != nil {
		b.Fatalf("Load failed: %v", err)
	}
	exporter := NewExporter(swf)
	var target Target
	for _, candidate := range mustTargets(b, exporter) {
		if candidate.Name == "dragonx_fly1_3" {
			target = candidate
			break
		}
	}
	if target.Name == "" {
		b.Fatal("dragonx_fly1_3 target not found")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, _, err := exporter.renderTarget(target); err != nil {
			b.Fatalf("renderTarget failed: %v", err)
		}
	}
}

func BenchmarkPrepareUITrophyBanner(b *testing.B) {
	swf, err := sc.Load("../../sc/ui.sc")
	if err != nil {
		b.Fatalf("Load failed: %v", err)
	}
	exporter := NewExporter(swf)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var target Target
		for _, candidate := range mustTargets(b, exporter) {
			if candidate.Name == "wl_trophy_banner" {
				target = candidate
				break
			}
		}
		if target.ResolvedTimeline == 0 {
			b.Fatal("resolved timeline should not be zero")
		}
	}
}

func mustTargets(tb testing.TB, exporter *Exporter) []Target {
	tb.Helper()
	targets, skipped := exporter.prepareTargets()
	if len(skipped) != 0 {
		tb.Fatalf("prepareTargets skipped assets unexpectedly: %+v", skipped[:minInt(len(skipped), 3)])
	}
	return targets
}

func fmtHash(hash [20]byte) string {
	return fmt.Sprintf("%x", hash[:])
}
