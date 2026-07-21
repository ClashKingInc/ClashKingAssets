package render

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	hevcencode "sc2fla/internal/encode/hevc"
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

func TestCollectChangePointsPreservesEveryFrameAtCommonRates(t *testing.T) {
	for _, fps := range []int{24, 30, 60} {
		t.Run(fmt.Sprintf("%dfps", fps), func(t *testing.T) {
			clip := &sc.MovieClip{ID: 1, FrameRate: fps, Frames: make([]sc.MovieClipFrame, fps)}
			exporter := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{1: clip}})
			target := Target{ResourceID: 1, Resource: clip, Duration: 1}

			points := exporter.collectChangePoints(target, target.Duration)
			if len(points) != fps {
				t.Fatalf("change points = %d, want %d", len(points), fps)
			}
			for frame, point := range points {
				if got := clipFrameIndexAt(clip, point); got != frame {
					t.Fatalf("change point %d (%0.9fs) selects frame %d", frame, point, got)
				}
			}
		})
	}
}

func TestCollapsedFrameDurationsAddUpToExactTimeline(t *testing.T) {
	for _, fps := range []int{24, 30, 60} {
		t.Run(fmt.Sprintf("%dfps", fps), func(t *testing.T) {
			clip := &sc.MovieClip{ID: 1, FrameRate: fps, Frames: make([]sc.MovieClipFrame, fps)}
			exporter := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{1: clip}})
			target := Target{ResourceID: 1, Resource: clip, Duration: 1}

			steps, err := exporter.collapseVisualStates(target, exporter.collectChangePoints(target, target.Duration), target.Duration)
			if err != nil {
				t.Fatalf("collapseVisualStates failed: %v", err)
			}
			var totalMS int
			for _, step := range steps {
				totalMS += step.DelayMS
			}
			if totalMS != 1000 {
				t.Fatalf("duration = %dms, want exactly 1000ms", totalMS)
			}
		})
	}
}

func TestCollapseVisualStatesSkipsDistinctTimelineFramesWithIdenticalDrawing(t *testing.T) {
	clip := &sc.MovieClip{
		ID: 1, FrameRate: 2, MatrixBank: 0,
		Binds: []sc.Bind{{ID: 2}},
		Frames: []sc.MovieClipFrame{
			{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}},
			{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}},
		},
	}
	exporter := NewExporter(&sc.SWF{
		Resources:   map[uint16]sc.Resource{1: clip, 2: &sc.Shape{ID: 2, Bitmaps: []sc.ShapeBitmap{{}}}},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}}}},
	})
	target := Target{ResourceID: 1, Resource: clip, Duration: 1}
	steps, err := exporter.collapseVisualStates(target, exporter.collectChangePoints(target, target.Duration), target.Duration)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].DelayMS != 1000 {
		t.Fatalf("collapsed steps = %+v, want one unchanged 1000ms visual state", steps)
	}
}

func TestCollapseSceneVisualStatesIncludesBaseAnimation(t *testing.T) {
	foregroundClip := &sc.MovieClip{ID: 1, FrameRate: 2, Frames: []sc.MovieClipFrame{{}, {}}}
	foreground := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{1: foregroundClip}})
	foregroundTarget := Target{ResourceID: 1, Resource: foregroundClip, Duration: 1}

	baseClip := &sc.MovieClip{
		ID: 10, FrameRate: 2, MatrixBank: 0,
		Binds: []sc.Bind{{ID: 11}, {ID: 12}},
		Frames: []sc.MovieClipFrame{
			{Elements: []sc.FrameElement{{Bind: 0}}},
			{Elements: []sc.FrameElement{{Bind: 1}}},
		},
	}
	base := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{
		10: baseClip,
		11: &sc.Shape{ID: 11, Bitmaps: []sc.ShapeBitmap{{}}},
		12: &sc.Shape{ID: 12, Bitmaps: []sc.ShapeBitmap{{}}},
	}})
	scenery := &sceneryRenderContext{exporter: base, target: Target{ResourceID: 10, Resource: baseClip, Duration: 1}}
	steps, err := foreground.collapseSceneVisualStates(foregroundTarget, []float64{0, 0.5}, 1, scenery)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("combined scene steps = %+v, want both base visual states", steps)
	}
}

func TestVisualStateSignatureHandlesEmptyMovieClip(t *testing.T) {
	clip := &sc.MovieClip{ID: 1, FrameRate: 30}
	exporter := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{1: clip}})
	target := Target{ResourceID: 1, Resource: clip}

	if _, err := exporter.visualStateSignature(target, 0); err != nil {
		t.Fatalf("visualStateSignature failed: %v", err)
	}
}

func TestAnimatedRenderMatchesFullDisplayList(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	texture.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	texture.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	texture.SetNRGBA(2, 0, color.NRGBA{B: 255, A: 255})
	shape := func(id uint16, textureX, outputX float64) *sc.Shape {
		return &sc.Shape{ID: id, Bitmaps: []sc.ShapeBitmap{{
			TextureIndex: 0,
			UVCoords:     []sc.Point{{X: textureX, Y: 0}},
			XYCoords:     []sc.Point{{X: outputX, Y: 0}},
		}}}
	}
	staticClip := &sc.MovieClip{ID: 2, FrameRate: 1, MatrixBank: 0, Binds: []sc.Bind{{ID: 4}}, Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0}}}}}
	animatedClip := &sc.MovieClip{ID: 3, FrameRate: 2, MatrixBank: 0, Binds: []sc.Bind{{ID: 5}, {ID: 6}}, Frames: []sc.MovieClipFrame{
		{Elements: []sc.FrameElement{{Bind: 0}}},
		{Elements: []sc.FrameElement{{Bind: 1}}},
	}}
	root := &sc.MovieClip{ID: 1, FrameRate: 2, MatrixBank: 0, Binds: []sc.Bind{{ID: 2}, {ID: 3}}, Frames: []sc.MovieClipFrame{
		{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}}},
		{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}}},
	}}
	swf := &sc.SWF{
		Textures:    []*sc.Texture{{Image: texture}},
		Resources:   map[uint16]sc.Resource{1: root, 2: staticClip, 3: animatedClip, 4: shape(4, 0, 0), 5: shape(5, 1, 1), 6: shape(6, 2, 2)},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}}}},
	}
	exporter := NewExporter(swf)
	target := Target{Name: "scene", ResourceID: 1, Resource: root, Duration: 1}

	frames, _, _, _, err := exporter.renderTarget(target)
	if err != nil {
		t.Fatalf("renderTarget failed: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("renderTarget frames = %d, want 2", len(frames))
	}
	cache := map[bitmapCacheKey]*bitmapRenderable{}
	bounds, err := exporter.collectBounds(target, target.Duration, []float64{0, 0.5}, cache)
	if err != nil {
		t.Fatalf("collectBounds failed: %v", err)
	}
	for i, at := range []float64{0, 0.5} {
		want, err := exporter.renderAt(target, at, bounds, cache)
		if err != nil {
			t.Fatalf("renderAt(%v) failed: %v", at, err)
		}
		if gotHash, wantHash := sha1.Sum(frames[i].Image.Pix), sha1.Sum(want.Pix); gotHash != wantHash {
			t.Fatalf("frame %d differs from the full ordered display list: got=%s want=%s", i, fmtHash(gotHash), fmtHash(wantHash))
		}
	}
}

func TestSceneryUsesSharedWorldBoundsAtRenderScale(t *testing.T) {
	makeShapeSWF := func(id uint16, x float64, px color.NRGBA) (*sc.SWF, Target) {
		texture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		texture.SetNRGBA(0, 0, px)
		shape := &sc.Shape{ID: id, Bitmaps: []sc.ShapeBitmap{{
			TextureIndex: 0,
			UVCoords:     []sc.Point{{X: 0, Y: 0}},
			XYCoords:     []sc.Point{{X: x, Y: 0}},
		}}}
		swf := &sc.SWF{Textures: []*sc.Texture{{Image: texture}}, Resources: map[uint16]sc.Resource{id: shape}}
		return swf, Target{Name: fmt.Sprintf("shape-%d", id), ResourceID: id, Resource: shape}
	}
	foregroundSWF, foreground := makeShapeSWF(1, 4, color.NRGBA{G: 255, A: 255})
	baseSWF, base := makeShapeSWF(2, 0, color.NRGBA{R: 255, A: 255})
	exporter := NewExporterWithOptions(foregroundSWF, ExportOptions{RenderScale: 2})
	scenery := &sceneryRenderContext{
		exporter:    NewExporterWithOptions(baseSWF, ExportOptions{RenderScale: 2}),
		target:      base,
		spriteCache: map[bitmapCacheKey]*bitmapRenderable{},
	}
	cache := map[bitmapCacheKey]*bitmapRenderable{}

	bounds, err := exporter.collectSceneBounds(foreground, 0, []float64{0}, cache, scenery)
	if err != nil {
		t.Fatalf("collectSceneBounds failed: %v", err)
	}
	frame, err := exporter.renderSceneAtInto(foreground, 0, bounds, cache, scenery, nil)
	if err != nil {
		t.Fatalf("renderSceneAtInto failed: %v", err)
	}
	if got, want := frame.Bounds().Dx(), bounds.Dx()*2; got != want {
		t.Fatalf("scaled scene width = %d, want %d", got, want)
	}
	red, green := false, false
	for y := 0; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			px := frame.NRGBAAt(x, y)
			red = red || px.R > px.G
			green = green || px.G > px.R
		}
	}
	if !green {
		t.Fatalf("shared scene missing foreground: red(base)=%v green(foreground)=%v", red, green)
	}
	if scenery.baseMatrix.Tx != 4 {
		t.Fatalf("base alignment translation = %v, want centers aligned by 4px", scenery.baseMatrix.Tx)
	}
}

func TestSceneDurationUsesSharedLoopPeriod(t *testing.T) {
	target := Target{Duration: 2}
	scenery := &sceneryRenderContext{target: Target{Duration: 3}}

	got, err := sceneDuration(target, scenery)
	if err != nil {
		t.Fatalf("sceneDuration failed: %v", err)
	}
	if got != 6 {
		t.Fatalf("scene duration = %v, want the 6-second shared loop", got)
	}
}

func TestSceneDurationAllowsFullTwoMinuteSceneryLoop(t *testing.T) {
	target := Target{Duration: 120}
	scenery := &sceneryRenderContext{target: Target{Duration: 40}}

	got, err := sceneDuration(target, scenery)
	if err != nil {
		t.Fatalf("sceneDuration failed: %v", err)
	}
	if got != 120 {
		t.Fatalf("scene duration = %v, want 120 seconds", got)
	}
}

func TestSceneryOutputScaleCapsLongestEdgeByDefault(t *testing.T) {
	exporter := NewExporter(&sc.SWF{})
	target := Target{Name: "Player_Background"}
	bounds := image.Rect(0, 0, 5000, 4000)

	scale := exporter.outputScale(target, bounds)
	if got := int(math.Ceil(float64(bounds.Dx()) * scale)); got != 2048 {
		t.Fatalf("scaled longest edge = %d, want 2048", got)
	}
}

func TestLargestEnclosedTransparentCentroidIgnoresOutsideTransparency(t *testing.T) {
	frame := image.NewNRGBA(image.Rect(0, 0, 7, 7))
	for y := 1; y < 6; y++ {
		for x := 1; x < 6; x++ {
			if x == 1 || x == 5 || y == 1 || y == 5 {
				frame.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}

	x, y, ok := largestEnclosedTransparentCentroid(frame)
	if !ok || x != 3 || y != 3 {
		t.Fatalf("hole centroid = (%v,%v,%v), want (3,3,true)", x, y, ok)
	}
}

func TestSceneryAllowedMaskKeepsInteriorHoleAndClipsExterior(t *testing.T) {
	foreground := image.NewNRGBA(image.Rect(0, 0, 7, 7))
	for y := 1; y < 6; y++ {
		for x := 1; x < 6; x++ {
			if x == 1 || x == 5 || y == 1 || y == 5 {
				foreground.SetNRGBA(x, y, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
			}
		}
	}

	mask := sceneryAllowedMask(foreground)
	if got := mask.NRGBAAt(0, 0).A; got != 0 {
		t.Fatalf("exterior mask alpha = %d, want 0", got)
	}
	if got := mask.NRGBAAt(3, 3).A; got != 255 {
		t.Fatalf("interior opening mask alpha = %d, want 255", got)
	}
	if got := mask.NRGBAAt(1, 1).A; got != 255 {
		t.Fatalf("foreground edge mask alpha = %d, want 255 for clean antialiasing", got)
	}
}

func TestSceneryAllowedMaskAppliesOnlyToBaseLayer(t *testing.T) {
	quad := func(textureIndex int) sc.ShapeBitmap {
		return sc.ShapeBitmap{
			TextureIndex: textureIndex,
			UVCoords:     []sc.Point{{X: 0, Y: 0}, {X: 7, Y: 0}, {X: 7, Y: 7}, {X: 0, Y: 7}},
			XYCoords:     []sc.Point{{X: 0, Y: 0}, {X: 7, Y: 0}, {X: 7, Y: 7}, {X: 0, Y: 7}},
		}
	}
	foregroundImage := image.NewNRGBA(image.Rect(0, 0, 7, 7))
	for y := 1; y < 6; y++ {
		for x := 1; x < 6; x++ {
			if x == 1 || x == 5 || y == 1 || y == 5 {
				foregroundImage.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
			}
		}
	}
	foregroundImage.SetNRGBA(0, 0, color.NRGBA{B: 255, A: 255})
	foregroundShape := &sc.Shape{ID: 1, Bitmaps: []sc.ShapeBitmap{quad(0)}}
	foregroundSWF := &sc.SWF{
		Textures:  []*sc.Texture{{Image: foregroundImage}},
		Resources: map[uint16]sc.Resource{1: foregroundShape},
	}
	foregroundExporter := NewExporter(foregroundSWF)
	foregroundTarget := Target{Name: "Player_Background", ResourceID: 1, Resource: foregroundShape}

	baseImage := image.NewNRGBA(image.Rect(0, 0, 7, 7))
	stddraw.Draw(baseImage, baseImage.Bounds(), &image.Uniform{C: color.NRGBA{R: 255, A: 255}}, image.Point{}, stddraw.Src)
	baseShape := &sc.Shape{ID: 2, Bitmaps: []sc.ShapeBitmap{quad(0)}}
	baseExporter := NewExporter(&sc.SWF{
		Textures:  []*sc.Texture{{Image: baseImage}},
		Resources: map[uint16]sc.Resource{2: baseShape},
	})
	scenery := &sceneryRenderContext{
		exporter:    baseExporter,
		target:      Target{Name: "Player_village_bg", ResourceID: 2, Resource: baseShape},
		spriteCache: map[bitmapCacheKey]*bitmapRenderable{},
		baseMatrix:  sc.IdentityMatrix(),
		allowedMask: sceneryAllowedMask(foregroundImage),
	}

	frame, err := foregroundExporter.renderSceneAtInto(
		foregroundTarget,
		0,
		image.Rect(0, 0, 7, 7),
		map[bitmapCacheKey]*bitmapRenderable{},
		scenery,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.NRGBAAt(3, 3); got.R != 255 || got.A != 255 {
		t.Fatalf("base inside village opening = %#v, want opaque red", got)
	}
	if got := frame.NRGBAAt(0, 1); got.A != 0 {
		t.Fatalf("base in exterior transparency = %#v, want transparent", got)
	}
	if got := frame.NRGBAAt(0, 0); got.B != 255 || got.A != 255 {
		t.Fatalf("foreground outside allowed area = %#v, want unrestricted opaque blue", got)
	}
}

func TestSceneryViewportUsesAlignedBaseAndFourByThreeCamera(t *testing.T) {
	foreground := image.Rect(-1000, -500, 4000, 3500)
	base := image.Rect(200, 100, 3200, 2400)

	viewport := sceneryViewportBounds(foreground, base)

	if !viewport.In(foreground) {
		t.Fatalf("viewport %v is outside foreground %v", viewport, foreground)
	}
	if viewport.Intersect(base) != base {
		t.Fatalf("viewport %v does not contain aligned base %v", viewport, base)
	}
	if got := float64(viewport.Dx()) / float64(viewport.Dy()); math.Abs(got-4.0/3.0) > 0.001 {
		t.Fatalf("viewport aspect ratio = %0.4f, want 4:3", got)
	}
	if viewport.Dx() <= base.Dx() || viewport.Dx() >= foreground.Dx() {
		t.Fatalf("viewport width = %d, want a foreground margin without staging bounds", viewport.Dx())
	}
}

func TestNamedTextFieldBoundsUsesAuthoredCameraTransform(t *testing.T) {
	field := &sc.TextField{ID: 3, Left: -2, Top: -484, Right: 4330, Bottom: 2840}
	child := &sc.MovieClip{
		ID:         2,
		MatrixBank: 0,
		Binds:      []sc.Bind{{ID: 3, Name: "camera_bounds"}},
		Frames:     []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0, Matrix: 0}}}},
	}
	root := &sc.MovieClip{
		ID:         1,
		MatrixBank: 0,
		Binds:      []sc.Bind{{ID: 2}},
		Frames:     []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0, Matrix: 1}}}},
	}
	swf := &sc.SWF{
		Resources: map[uint16]sc.Resource{1: root, 2: child, 3: field},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{
			{A: 1, D: 1, Tx: -274, Ty: 182.35},
			{A: 1, D: 1, Tx: 10, Ty: 20},
		}}},
	}
	exporter := NewExporter(swf)
	target := Target{Name: "Player_Background", ResourceID: 1, Resource: root}

	got, ok := exporter.namedTextFieldBounds(target, "camera_bounds")
	if !ok {
		t.Fatal("camera bounds not found")
	}
	want := image.Rect(-266, -282, 4066, 3043)
	if got != want {
		t.Fatalf("camera bounds = %v, want %v", got, want)
	}
}

func TestLookupSceneryBaseSWFAllowsMissingOptionalMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sc", "background_player.sc")
	base, err := lookupSceneryBaseSWF(source, "Player_Background")
	if err != nil {
		t.Fatalf("lookupSceneryBaseSWF returned an error for missing metadata: %v", err)
	}
	if base != "" {
		t.Fatalf("lookupSceneryBaseSWF returned %q, want no mapped base", base)
	}
}

func TestLookupSceneryBaseSWFRejectsMalformedMetadata(t *testing.T) {
	root := t.TempDir()
	logicDir := filepath.Join(root, "logic")
	if err := os.MkdirAll(logicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logicDir, "village_backgrounds.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "sc", "background_player.sc")
	if _, err := lookupSceneryBaseSWF(source, "Player_Background"); err == nil {
		t.Fatal("lookupSceneryBaseSWF accepted malformed metadata")
	}
}

func TestAnimatedRenderStopsWhenExportIsCancelled(t *testing.T) {
	clip := &sc.MovieClip{ID: 1, FrameRate: 2, Frames: []sc.MovieClipFrame{{}, {}}}
	exporter := NewExporter(&sc.SWF{Resources: map[uint16]sc.Resource{1: clip}})
	cancel := make(chan struct{})
	close(cancel)
	exporter.cancel = cancel
	target := Target{Name: "cancelled", ResourceID: 1, Resource: clip, Duration: 1}

	_, _, _, _, _, err := exporter.renderAnimatedTargetToWebP(target, io.Discard)
	if !errors.Is(err, errExportCancelled) {
		t.Fatalf("render error = %v, want export cancelled", err)
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

func TestPrepareRequestedTargetsDoesNotResolveUnrequestedResources(t *testing.T) {
	exporter := NewExporter(&sc.SWF{
		Resources: map[uint16]sc.Resource{
			1: &sc.Shape{ID: 1},
			2: &sc.TextField{ID: 2},
		},
		Exports: map[uint16][]string{
			1: {"wanted"},
			2: {"unrequested-unsupported"},
		},
	})

	targets, skipped := exporter.prepareRequestedTargets([]string{"wanted"})
	if len(targets) != 1 || targets[0].Name != "wanted" {
		t.Fatalf("prepared targets = %+v, want only wanted", targets)
	}
	if len(skipped) != 0 {
		t.Fatalf("unrequested resource was still prepared and skipped: %+v", skipped)
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

func TestNameAllocatorRejectsMappedPathsThatOnlyDifferByExtension(t *testing.T) {
	outDir := t.TempDir()
	allocator := newNameAllocator(outDir, map[string]string{
		"foo": filepath.Join(outDir, "shared", "asset.png"),
		"bar": filepath.Join(outDir, "shared", "asset.webp"),
	})

	if _, _, err := allocator.Next("foo", 1); err != nil {
		t.Fatalf("allocator.Next for foo failed: %v", err)
	}
	if _, _, err := allocator.Next("bar", 2); err == nil {
		t.Fatal("expected extension-only mapped path conflict")
	}
}

func TestNameAllocatorRejectsSecondMappedClaimWithSameExportName(t *testing.T) {
	outDir := t.TempDir()
	mapped := filepath.Join(outDir, "asset.webp")
	allocator := newNameAllocator(outDir, map[string]string{"duplicate": mapped})
	if _, _, err := allocator.Next("duplicate", 1); err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	if _, _, err := allocator.Next("duplicate", 2); err == nil {
		t.Fatal("expected the second resource with the same mapped export name to be rejected")
	}
}

func TestWriteOutputAtomicallyPreservesExistingFileOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.webp")
	if err := os.WriteFile(path, []byte("previous"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	wantErr := fmt.Errorf("encode failed")
	err := writeOutputAtomically(path, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial replacement"))
		return wantErr
	})
	if err == nil || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("writeOutputAtomically error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved output: %v", err)
	}
	if string(got) != "previous" {
		t.Fatalf("preserved output = %q, want previous", got)
	}
}

func TestRemoveOutputAlternatesKeepsOnlyRequestedFormat(t *testing.T) {
	outputBase := filepath.Join(t.TempDir(), "asset")
	for _, extension := range []string{".png", ".webp", ".mov"} {
		if err := os.WriteFile(outputBase+extension, []byte(extension), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", extension, err)
		}
	}

	if err := removeOutputAlternates(outputBase, ".mov"); err != nil {
		t.Fatalf("removeOutputAlternates failed: %v", err)
	}
	if _, err := os.Stat(outputBase + ".mov"); err != nil {
		t.Fatalf("kept MOV is missing: %v", err)
	}
	for _, extension := range []string{".png", ".webp"} {
		if _, err := os.Stat(outputBase + extension); !os.IsNotExist(err) {
			t.Fatalf("stale %s stat error = %v, want not-exist", extension, err)
		}
	}
}

func TestShouldFallbackFromHEVCOnlyForAutoUnavailable(t *testing.T) {
	unavailable := fmt.Errorf("native encoder: %w", hevcencode.ErrUnavailable)
	tests := []struct {
		name   string
		format string
		err    error
		want   bool
	}{
		{name: "auto unavailable", format: "auto", err: unavailable, want: true},
		{name: "explicit HEVC unavailable", format: "hevc", err: unavailable, want: false},
		{name: "auto encoder bug", format: "auto", err: errors.New("writer failed"), want: false},
		{name: "explicit WebP", format: "webp", err: unavailable, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldFallbackFromHEVC(test.format, test.err); got != test.want {
				t.Fatalf("shouldFallbackFromHEVC(%q, %v) = %v, want %v", test.format, test.err, got, test.want)
			}
		})
	}
}

func TestSingleFramePreferWebPUsesWebPOutput(t *testing.T) {
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
	frames := []renderedFrame{
		{
			Image: func() *image.NRGBA {
				img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
				img.SetNRGBA(0, 0, color.NRGBA{})
				img.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 64, B: 32, A: 255})
				return img
			}(),
			DelayMS: 40,
		},
		{
			Image: func() *image.NRGBA {
				img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
				img.SetNRGBA(0, 0, color.NRGBA{R: 16, G: 128, B: 255, A: 255})
				img.SetNRGBA(1, 0, color.NRGBA{})
				return img
			}(),
			DelayMS: 50,
		},
	}

	var buf bytes.Buffer
	if err := writeAnimatedWebP(&buf, frames, normalizeExportOptions(ExportOptions{})); err != nil {
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
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{})
	img.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 64, B: 32, A: 255})

	var buf bytes.Buffer
	if err := writeStillWebP(&buf, img, normalizeExportOptions(ExportOptions{})); err != nil {
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

func TestExportDragonNamedOutputsAndManifest(t *testing.T) {
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

func TestStaticBackdropAppliesRenderScaleToCanvas(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	texture.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	shape := &sc.Shape{ID: 1, Bitmaps: []sc.ShapeBitmap{{
		TextureIndex: 0,
		UVCoords:     []sc.Point{{X: 0, Y: 0}},
		XYCoords:     []sc.Point{{X: 0, Y: 0}},
	}}}
	swf := &sc.SWF{Textures: []*sc.Texture{{Image: texture}}, Resources: map[uint16]sc.Resource{1: shape}}
	exporter := NewExporterWithOptions(swf, ExportOptions{RenderScale: 2})
	target := Target{Name: "static", ResourceID: 1, Resource: shape}
	cache := map[bitmapCacheKey]*bitmapRenderable{}
	bounds, err := exporter.collectBounds(target, 0, nil, cache)
	if err != nil {
		t.Fatal(err)
	}

	frame, err := exporter.renderStaticBackdrop(target, bounds, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := frame.Bounds().Dx(), bounds.Dx()*2; got != want {
		t.Fatalf("static backdrop width = %d, want %d", got, want)
	}
	if got, want := frame.Bounds().Dy(), bounds.Dy()*2; got != want {
		t.Fatalf("static backdrop height = %d, want %d", got, want)
	}
}

func TestComposeAddDoesNotCreateBlackAlpha(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))

	composeAdd(canvas, 0, 0, color.NRGBA{R: 24, G: 12, B: 4, A: 200})

	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("additive black pixel = %#v, want transparent", got)
	}
}

func TestComposeAddDoesNotCreateCoverageByDefault(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	composeAdd(canvas, 0, 0, color.NRGBA{R: 255, G: 100, A: 255})
	if got := canvas.NRGBAAt(0, 0); got != (color.NRGBA{}) {
		t.Fatalf("additive pixel on transparency = %#v, want transparent", got)
	}
}

func TestComposeAddCanCreateVisibleCoverage(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	composeAddWithCoverage(canvas, 0, 0, color.NRGBA{R: 255, G: 100, A: 255}, true)
	if got := canvas.NRGBAAt(0, 0); got.A == 0 || got.R == 0 {
		t.Fatalf("additive coverage = %#v, want visible color", got)
	}
}

func TestSampleNRGBABilinearInterpolatesPremultipliedColor(t *testing.T) {
	sprite := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	sprite.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	sprite.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})

	got := sampleNRGBABilinear(sprite, 0.5, 0)
	if got.R < 126 || got.R > 129 || got.B < 126 || got.B > 129 || got.A != 255 {
		t.Fatalf("bilinear midpoint = %#v, want equal opaque red and blue", got)
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

func TestDrawOpaqueOverPreservesPartialAlphaComposition(t *testing.T) {
	bottom := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	bottom.SetNRGBA(0, 0, color.NRGBA{B: 255, A: 255})
	top := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	top.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})

	drawOpaqueOver(bottom, top)

	got := bottom.NRGBAAt(0, 0)
	if got.A != 255 || got.R < 127 || got.R > 129 || got.B < 126 || got.B > 128 {
		t.Fatalf("partially transparent overlay = %#v, want red composited over opaque blue", got)
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

func TestRenderAppliesMovieClipAlphaMask(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	texture.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	texture.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	texture.SetNRGBA(2, 0, color.NRGBA{G: 255, A: 255})
	maskShape := &sc.Shape{ID: 5, Bitmaps: []sc.ShapeBitmap{{
		TextureIndex: 0,
		UVCoords:     []sc.Point{{X: 0, Y: 0}},
		XYCoords:     []sc.Point{{X: 0, Y: 0}},
	}}}
	contentShape := &sc.Shape{ID: 6, Bitmaps: []sc.ShapeBitmap{{
		TextureIndex: 0,
		UVCoords:     []sc.Point{{X: 1, Y: 0}, {X: 3, Y: 0}, {X: 3, Y: 1}, {X: 1, Y: 1}},
		XYCoords:     []sc.Point{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 1}, {X: 0, Y: 1}},
	}}}
	clip := &sc.MovieClip{
		ID:         1,
		FrameRate:  1,
		MatrixBank: 0,
		Binds: []sc.Bind{
			{ID: 2}, {ID: 5}, {ID: 3}, {ID: 6}, {ID: 4},
		},
		Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{
			{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4},
		}}},
	}
	swf := &sc.SWF{
		Textures: []*sc.Texture{{Image: texture}},
		Resources: map[uint16]sc.Resource{
			1: clip,
			2: &sc.MovieClipModifier{ID: 2, Modifier: 38},
			3: &sc.MovieClipModifier{ID: 3, Modifier: 39},
			4: &sc.MovieClipModifier{ID: 4, Modifier: 40},
			5: maskShape,
			6: contentShape,
		},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}}}},
	}
	exporter := NewExporter(swf)
	target := Target{Name: "masked", ResourceID: 1, Resource: clip}
	cache := map[bitmapCacheKey]*bitmapRenderable{}
	bounds, err := exporter.collectBounds(target, 0, nil, cache)
	if err != nil {
		t.Fatalf("collectBounds failed: %v", err)
	}
	frame, err := exporter.renderAt(target, 0, bounds, cache)
	if err != nil {
		t.Fatalf("renderAt failed: %v", err)
	}
	visible := 0
	for y := 0; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			if frame.NRGBAAt(x, y).A != 0 {
				visible++
			}
		}
	}
	if visible != 1 {
		t.Fatalf("visible pixels = %d, want the two-pixel content clipped to the one-pixel mask", visible)
	}
}

func TestNestedMaskAppliesParentAlphaOnce(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	texture.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	texture.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	texture.SetNRGBA(2, 0, color.NRGBA{G: 255, A: 255})
	shape := func(id uint16, textureX float64) *sc.Shape {
		return &sc.Shape{ID: id, Bitmaps: []sc.ShapeBitmap{{
			TextureIndex: 0,
			UVCoords:     []sc.Point{{X: textureX, Y: 0}},
			XYCoords:     []sc.Point{{X: 0, Y: 0}},
		}}}
	}
	inner := &sc.MovieClip{ID: 10, FrameRate: 1, MatrixBank: 0,
		Binds:  []sc.Bind{{ID: 11}, {ID: 12}, {ID: 13}, {ID: 14}, {ID: 15}},
		Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4}}}},
	}
	outer := &sc.MovieClip{ID: 1, FrameRate: 1, MatrixBank: 0,
		Binds:  []sc.Bind{{ID: 2}, {ID: 3}, {ID: 4}, {ID: 10}, {ID: 5}},
		Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4}}}},
	}
	swf := &sc.SWF{
		Textures: []*sc.Texture{{Image: texture}},
		Resources: map[uint16]sc.Resource{
			1: outer, 2: &sc.MovieClipModifier{ID: 2, Modifier: 38}, 3: shape(3, 0),
			4: &sc.MovieClipModifier{ID: 4, Modifier: 39}, 5: &sc.MovieClipModifier{ID: 5, Modifier: 40},
			10: inner, 11: &sc.MovieClipModifier{ID: 11, Modifier: 38}, 12: shape(12, 1),
			13: &sc.MovieClipModifier{ID: 13, Modifier: 39}, 14: shape(14, 2), 15: &sc.MovieClipModifier{ID: 15, Modifier: 40},
		},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}}}},
	}
	exporter := NewExporter(swf)
	target := Target{Name: "nested-mask", ResourceID: 1, Resource: outer}
	cache := map[bitmapCacheKey]*bitmapRenderable{}
	bounds, err := exporter.collectBounds(target, 0, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exporter.renderAt(target, 0, bounds, cache)
	if err != nil {
		t.Fatal(err)
	}
	maximumAlpha := uint8(0)
	for index := 3; index < len(frame.Pix); index += 4 {
		alpha := frame.Pix[index]
		if alpha > maximumAlpha {
			maximumAlpha = alpha
		}
	}
	if maximumAlpha < 127 || maximumAlpha > 129 {
		t.Fatalf("nested mask alpha = %d, want parent alpha applied once (128)", maximumAlpha)
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
