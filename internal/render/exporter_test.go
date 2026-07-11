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
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("fixture not present: %s", path)
		}
		t.Fatalf("Stat(%s) failed: %v", path, err)
	}
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
	if len(target.FrameSegments) != 3 {
		t.Fatalf("spell_factory_lvl9 segment count = %d, want 3", len(target.FrameSegments))
	}
	for _, segment := range target.FrameSegments {
		if segment.StartFrame >= segment.EndFrame {
			t.Fatalf("invalid segment %+v", segment)
		}
	}
}

func TestVisualStateSignatureIncludesTransforms(t *testing.T) {
	exporter := NewExporter(&sc.SWF{
		Resources: map[uint16]sc.Resource{
			1: &sc.MovieClip{
				ID:         1,
				FrameRate:  1,
				MatrixBank: 0,
				Binds:      []sc.Bind{{ID: 2}},
				Frames: []sc.MovieClipFrame{
					{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}},
					{Elements: []sc.FrameElement{{Bind: 0, Matrix: 1}}},
				},
			},
			2: &sc.Shape{ID: 2, Bitmaps: []sc.ShapeBitmap{{}, {}}},
		},
		MatrixBanks: []*sc.MatrixBank{{
			Matrices: []sc.Matrix{
				{A: 1, D: 1, Tx: 0, Ty: 0},
				{A: 1, D: 1, Tx: 10, Ty: 0},
			},
		}},
	})
	target := Target{ResourceID: 1, Resource: exporter.swf.Resources[1]}

	sig0, err := exporter.visualStateSignature(target, 0)
	if err != nil {
		t.Fatalf("visualStateSignature at t=0 failed: %v", err)
	}
	sig1, err := exporter.visualStateSignature(target, 1)
	if err != nil {
		t.Fatalf("visualStateSignature at t=1 failed: %v", err)
	}
	if sig0 == sig1 {
		t.Fatal("expected different visual signatures for different child transforms")
	}
}

func TestCollectAnimatedResourceSetIncludesSingleFrameWrappers(t *testing.T) {
	exporter := NewExporter(&sc.SWF{
		Resources: map[uint16]sc.Resource{
			1: &sc.MovieClip{
				ID:         1,
				FrameRate:  1,
				MatrixBank: 0,
				Binds:      []sc.Bind{{ID: 2}},
				Frames:     []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}}},
			},
			2: &sc.MovieClip{
				ID:         2,
				FrameRate:  2,
				MatrixBank: 0,
				Frames: []sc.MovieClipFrame{
					{},
					{},
				},
			},
		},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}}}},
	})

	animated := exporter.collectAnimatedResourceSet(1)
	if !animated[1] {
		t.Fatal("expected single-frame wrapper with animated child to be treated as animated")
	}
	if !animated[2] {
		t.Fatal("expected animated child clip to be treated as animated")
	}
}

func TestFilterRequestedTargetsIncludesExactMatchesOnly(t *testing.T) {
	targets := []Target{{Name: "foo", ResourceID: 1}, {Name: "bar", ResourceID: 2}, {Name: "bar", ResourceID: 3}}
	skipped := []SkippedEntry{{ExportName: "baz", ResourceID: 4, Reason: "unsupported"}}

	filteredTargets, filteredSkipped, err := filterRequestedTargets(targets, skipped, []string{"bar", "baz"}, "test.sc")
	if err != nil {
		t.Fatalf("filterRequestedTargets failed: %v", err)
	}
	if len(filteredTargets) != 2 {
		t.Fatalf("filtered target count = %d, want 2", len(filteredTargets))
	}
	for _, target := range filteredTargets {
		if target.Name != "bar" {
			t.Fatalf("unexpected filtered target %q", target.Name)
		}
	}
	if len(filteredSkipped) != 1 || filteredSkipped[0].ExportName != "baz" {
		t.Fatalf("filtered skipped = %+v, want baz", filteredSkipped)
	}
}

func TestFilterRequestedTargetsReturnsMissingNames(t *testing.T) {
	_, _, err := filterRequestedTargets([]Target{{Name: "foo", ResourceID: 1}}, nil, []string{"missing"}, "test.sc")
	if err == nil {
		t.Fatal("expected missing asset error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error %q should mention missing asset", err)
	}
}

func TestFilterRequestedTargetsSelectsFrameLabelOnAnimatedDescendant(t *testing.T) {
	exporter := NewExporter(&sc.SWF{
		Resources: map[uint16]sc.Resource{
			1: &sc.MovieClip{
				ID:         1,
				FrameRate:  1,
				MatrixBank: 0,
				Binds:      []sc.Bind{{ID: 2}},
				Frames:     []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}}},
			},
			2: &sc.MovieClip{
				ID:         2,
				FrameRate:  1,
				MatrixBank: 0,
				Binds:      []sc.Bind{{ID: 3}},
				Frames: []sc.MovieClipFrame{
					{Name: "wl_tier_1", Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}},
					{Name: "wl_tier_2", Elements: []sc.FrameElement{{Bind: 0, Matrix: 1}}},
					{Name: "wl_tier_3", Elements: []sc.FrameElement{{Bind: 0, Matrix: 2}}},
				},
			},
			3: &sc.Shape{ID: 3, Bitmaps: []sc.ShapeBitmap{{}}},
		},
		MatrixBanks: []*sc.MatrixBank{{
			Matrices: []sc.Matrix{
				{A: 1, D: 1, Tx: 0, Ty: 0},
				{A: 1, D: 1, Tx: 10, Ty: 0},
				{A: 1, D: 1, Tx: 20, Ty: 0},
			},
		}},
	})

	target, err := exporter.prepareTarget("badge", 1, exporter.swf.Resources[1])
	if err != nil {
		t.Fatalf("prepareTarget failed: %v", err)
	}
	if !target.IsWrapper {
		t.Fatal("expected wrapper target")
	}
	if !slices.Equal(target.FrameLabels, []string{"wl_tier_1", "wl_tier_2", "wl_tier_3"}) {
		t.Fatalf("frame labels = %v", target.FrameLabels)
	}

	filtered, _, err := filterRequestedTargets([]Target{target}, nil, []string{"badge@wl_tier_2"}, "test.sc")
	if err != nil {
		t.Fatalf("filterRequestedTargets failed: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered count = %d, want 1", len(filtered))
	}

	selected := filtered[0]
	if selected.Name != "badge@wl_tier_2" {
		t.Fatalf("selected name = %q", selected.Name)
	}
	if selected.Duration != 0 {
		t.Fatalf("selected duration = %v, want 0", selected.Duration)
	}
	if selected.SelectedFrame == nil {
		t.Fatal("expected selected frame metadata")
	}
	if selected.SelectedFrame.ResourceID != 2 || selected.SelectedFrame.FrameIndex != 1 {
		t.Fatalf("selected frame = %+v, want resource 2 frame 1", *selected.SelectedFrame)
	}

	baseSig, err := exporter.visualStateSignature(target, 1)
	if err != nil {
		t.Fatalf("visualStateSignature base failed: %v", err)
	}
	selectedSig, err := exporter.visualStateSignature(selected, 0)
	if err != nil {
		t.Fatalf("visualStateSignature selected failed: %v", err)
	}
	if selectedSig != baseSig {
		t.Fatalf("selected frame target should match the requested labeled frame: selected=%d base=%d", selectedSig, baseSig)
	}
}

func TestNameAllocatorUsesMappedOutputPath(t *testing.T) {
	outDir := t.TempDir()
	mapped := filepath.Join(outDir, "nested", "unit_barbarian_big.png")
	allocator := newNameAllocator(outDir, map[string]string{"unit_barbarian_big": mapped})

	outputBase, outputFile, err := allocator.Next("unit_barbarian_big", 9)
	if err != nil {
		t.Fatalf("allocator.Next failed: %v", err)
	}
	if outputBase != strings.TrimSuffix(mapped, filepath.Ext(mapped)) {
		t.Fatalf("outputBase = %q, want %q", outputBase, strings.TrimSuffix(mapped, filepath.Ext(mapped)))
	}
	if outputFile != strings.TrimSuffix(mapped, filepath.Ext(mapped)) {
		t.Fatalf("outputFile = %q, want %q", outputFile, strings.TrimSuffix(mapped, filepath.Ext(mapped)))
	}
	if _, err := os.Stat(filepath.Dir(mapped)); err != nil {
		t.Fatalf("expected mapped output dir to exist: %v", err)
	}
}

func TestNameAllocatorRejectsSharedMappedPath(t *testing.T) {
	outDir := t.TempDir()
	mapped := filepath.Join(outDir, "shared", "asset.png")
	allocator := newNameAllocator(outDir, map[string]string{"foo": mapped, "bar": mapped})

	if _, _, err := allocator.Next("foo", 1); err != nil {
		t.Fatalf("allocator.Next for foo failed: %v", err)
	}
	if _, _, err := allocator.Next("bar", 2); err == nil {
		t.Fatal("expected shared mapped path conflict")
	}
}

func TestSingleFramePreferWebPUsesWebPOutput(t *testing.T) {
	requireWebPTools(t)

	swf := mustLoadSWF(t, "../../sc/info_barbarian.sc")
	exporter := NewExporterWithOptions(swf, ExportOptions{PreferWebP: true})
	target := findTarget(t, exporter, "unit_barbarian_big")

	outDir := t.TempDir()
	entry, skipped, _, err := exporter.exportTarget(target, outDir, newNameAllocator(outDir, nil))
	if err != nil {
		t.Fatalf("exportTarget failed: %v", err)
	}
	if skipped != nil {
		t.Fatalf("expected export, got skipped %+v", skipped)
	}
	if entry == nil {
		t.Fatal("expected manifest entry")
	}
	if filepath.Ext(entry.OutputFile) != ".webp" {
		t.Fatalf("output file = %q, want .webp", entry.OutputFile)
	}
	if _, err := os.Stat(filepath.Join(outDir, entry.OutputFile)); err != nil {
		t.Fatalf("expected webp output on disk: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, entry.OutputFile))
	if err != nil {
		t.Fatalf("read webp output failed: %v", err)
	}
	if bytes.Contains(data, []byte("ANIM")) {
		t.Fatal("single-frame webp should not contain animation metadata")
	}
}

func TestFirstFrameOnlyBypassesAnimatedExport(t *testing.T) {
	requireWebPTools(t)

	swf := mustLoadSWF(t, "../../sc/chr_dragon.sc")
	exporter := NewExporterWithOptions(swf, ExportOptions{PreferWebP: true, FirstFrameOnly: true})
	target := findTarget(t, exporter, "dragonx_fly1_3")
	if target.Duration <= 0 {
		t.Fatal("test target should be animated")
	}

	outDir := t.TempDir()
	entry, skipped, _, err := exporter.exportTarget(target, outDir, newNameAllocator(outDir, nil))
	if err != nil {
		t.Fatalf("exportTarget failed: %v", err)
	}
	if skipped != nil {
		t.Fatalf("expected export, got skipped %+v", skipped)
	}
	if entry.FrameCount != 1 {
		t.Fatalf("frame count = %d, want 1", entry.FrameCount)
	}
	if entry.DurationMS != 0 {
		t.Fatalf("duration = %d, want 0", entry.DurationMS)
	}
	data, err := os.ReadFile(filepath.Join(outDir, entry.OutputFile))
	if err != nil {
		t.Fatalf("read webp output failed: %v", err)
	}
	if bytes.Contains(data, []byte("ANIM")) {
		t.Fatal("first-frame webp should not contain animation metadata")
	}
}

func TestLastFrameOnlyUsesEndOfTimeline(t *testing.T) {
	if got := stillRenderTime(nil, 5, ExportOptions{LastFrameOnly: true}); got <= 4.999 || got >= 5 {
		t.Fatalf("last frame render time = %f, want just before duration", got)
	}
	if got := stillRenderTime(nil, 5, ExportOptions{FirstFrameOnly: true}); got != 0 {
		t.Fatalf("first frame render time = %f, want 0", got)
	}
}

func TestSpecificFrameUsesOneBasedFrameIndex(t *testing.T) {
	clip := &sc.MovieClip{
		FrameRate: 10,
		Frames:    make([]sc.MovieClipFrame, 8),
	}
	if got := stillRenderTime(clip, 2, ExportOptions{FrameIndex: 5}); got != 0.4 {
		t.Fatalf("frame 5 render time = %f, want 0.4", got)
	}
	if got := stillRenderTime(clip, 0.8, ExportOptions{FrameIndex: 99}); got <= 0.799 || got >= 0.8 {
		t.Fatalf("out-of-range frame render time = %f, want just before duration", got)
	}
}

func TestWeaponizedBuilderHutBindSplitsAreEnabled(t *testing.T) {
	if !shouldSplitNamedBinds("worker_building_armed_lvl7") {
		t.Fatal("expected weaponized builder hut exports to support bind splits")
	}
	if shouldSplitNamedBinds("worker_building") {
		t.Fatal("did not expect plain builder hut export to split binds")
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

func TestWriteStillWebPDoesNotWriteAnimationChunk(t *testing.T) {
	requireWebPTools(t)

	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{})
	img.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 64, B: 32, A: 255})

	var buf bytes.Buffer
	if err := writeStillWebP(&buf, img); err != nil {
		t.Fatalf("writeStillWebP failed: %v", err)
	}

	data := buf.Bytes()
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Fatal("webp should start with RIFF")
	}
	if !bytes.Contains(data, []byte("WEBP")) {
		t.Fatal("webp should contain WEBP signature")
	}
	if bytes.Contains(data, []byte("ANIM")) {
		t.Fatal("still webp should not contain animation metadata")
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

	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	files = slices.DeleteFunc(files, func(entry os.DirEntry) bool {
		return filepath.Ext(entry.Name()) != ".webp"
	})
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
		if entry.ExportName == "spell_factory_lvl1" && len(entry.FrameSegments) == 0 {
			t.Fatal("spell_factory_lvl1 should expose frame segments in manifest")
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
	entry, skipped, _, err := exporter.exportTarget(target, outDir, newNameAllocator(outDir, nil))
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
	targets, _ := exporter.prepareTargets()
	var target Target
	for _, candidate := range targets {
		if candidate.Name == "wl_trophy_banner" {
			target = candidate
			break
		}
	}
	if target.Name == "" {
		t.Fatal("wl_trophy_banner target not found")
	}
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

func TestPrepareTargetsSkipsNamedUISurfacesOnlyForUISources(t *testing.T) {
	uiSWF := &sc.SWF{
		Filename: "ui.sc",
		Resources: map[uint16]sc.Resource{
			1: &sc.Shape{ID: 1},
			2: &sc.Shape{ID: 2},
		},
		Exports: map[uint16][]string{
			1: {"league_promoted_screen"},
			2: {"troop_card"},
		},
	}
	uiExporter := NewExporter(uiSWF)
	uiTargets, uiSkipped := uiExporter.prepareTargets()
	if len(uiTargets) != 1 || uiTargets[0].Name != "troop_card" {
		t.Fatalf("ui targets = %v, want only troop_card", targetNames(uiTargets))
	}
	if len(uiSkipped) != 1 || uiSkipped[0].ExportName != "league_promoted_screen" {
		t.Fatalf("ui skipped = %+v, want league_promoted_screen skipped", uiSkipped)
	}

	otherSWF := &sc.SWF{
		Filename: "buildings.sc",
		Resources: map[uint16]sc.Resource{
			1: &sc.Shape{ID: 1},
		},
		Exports: map[uint16][]string{
			1: {"league_promoted_screen"},
		},
	}
	otherExporter := NewExporter(otherSWF)
	otherTargets, otherSkipped := otherExporter.prepareTargets()
	if len(otherSkipped) != 0 {
		t.Fatalf("non-ui source unexpectedly skipped: %+v", otherSkipped)
	}
	if len(otherTargets) != 1 || otherTargets[0].Name != "league_promoted_screen" {
		t.Fatalf("non-ui targets = %v, want league_promoted_screen retained", targetNames(otherTargets))
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
	const want = "7032aab2b264e3b8c375189f916f9cdd24f8bf00"
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

func TestStaticOnlyContainersKeepWrappersBeforeAnimatedTimeline(t *testing.T) {
	exporter := &Exporter{}
	target := Target{AncestorIDs: []uint16{10, 20, 30}, ResolvedTimeline: 30}

	containers := exporter.staticOnlyContainers(target)

	if len(containers) != 1 || !containers[20] {
		t.Fatalf("static containers = %v, want only wrapper 20", containers)
	}
}

func TestComposeAddDoesNotCreateBlackAlpha(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))

	composeAdd(canvas, 0, 0, color.NRGBA{R: 24, G: 12, B: 4, A: 200})

	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("additive black pixel = %#v, want transparent", got)
	}
}

func TestComposeAddDoesNotCreateCoverage(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	composeAdd(canvas, 0, 0, color.NRGBA{R: 255, G: 100, A: 255})
	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("additive pixel on transparency = %#v, want transparent", got)
	}
}

func TestInheritedBlendUsesNearestOverride(t *testing.T) {
	if got := inheritedBlend("add", ""); got != "add" {
		t.Fatalf("inherited blend = %q, want add", got)
	}
	if got := inheritedBlend("add", "multiply"); got != "multiply" {
		t.Fatalf("overridden blend = %q, want multiply", got)
	}
}

func TestPreferFrameLabelUsesLabelAndPreservesFallback(t *testing.T) {
	targets := []Target{
		{
			Name:     "animated_deco",
			Duration: 2,
			FrameLabelLookup: map[string]FrameLabelTarget{
				"store_idle": {Label: "store_idle", ResourceID: 2, FrameIndex: 8},
				"idle_start": {Label: "idle_start", ResourceID: 2, FrameIndex: 4},
			},
		},
		{Name: "unlabeled_deco", Duration: 2},
	}

	selected := preferFrameLabel(targets, "store_idle, idle_end, idle_start")

	if selected[0].SelectedFrame == nil || selected[0].SelectedFrame.FrameIndex != 8 || selected[0].Duration != 0 {
		t.Fatalf("idle target was not selected: %+v", selected[0])
	}
	if selected[1].SelectedFrame != nil || selected[1].Duration != 2 {
		t.Fatalf("unlabeled target did not preserve fallback: %+v", selected[1])
	}
}

func TestComposeScreenTreatsBlackAsTransparent(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	composeScreen(canvas, 0, 0, color.NRGBA{A: 255})
	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("screen black pixel = %#v, want transparent", got)
	}
}

func TestComposeMultiplyDoesNotCreateCoverage(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	composeMultiply(canvas, 0, 0, color.NRGBA{R: 20, G: 40, B: 80, A: 255})
	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("multiply pixel on transparency = %#v, want transparent", got)
	}
}

func TestUnmaskedFrameElementsOmitsMaskGroups(t *testing.T) {
	clip := &sc.MovieClip{Binds: []sc.Bind{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}, {ID: 6}}}
	exporter := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{
		1: &sc.Shape{ID: 1},
		2: &sc.MovieClipModifier{ID: 2, Modifier: 38},
		3: &sc.Shape{ID: 3},
		4: &sc.MovieClipModifier{ID: 4, Modifier: 39},
		5: &sc.Shape{ID: 5},
		6: &sc.MovieClipModifier{ID: 6, Modifier: 40},
	}})
	elements := []sc.FrameElement{{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4}, {Bind: 5}, {Bind: 0}}
	visible := exporter.unmaskedFrameElements(clip, elements)
	if len(visible) != 2 || visible[0].Bind != 0 || visible[1].Bind != 0 {
		t.Fatalf("visible elements = %+v, want only unmasked shapes", visible)
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

func targetNames(targets []Target) []string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Name)
	}
	return names
}

func fmtHash(hash [20]byte) string {
	return fmt.Sprintf("%x", hash[:])
}
