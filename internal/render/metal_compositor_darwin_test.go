//go:build darwin && cgo

package render

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"sc2fla/internal/sc"
)

func TestMetalCompositorClearAndReusableReadback(t *testing.T) {
	compositor := requireMetalCompositor(t, 9, 7)
	defer compositor.close()

	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	want := color.NRGBA{R: 17, G: 81, B: 203, A: 149}
	if err := compositor.clear(want); err != nil {
		t.Fatal(err)
	}
	first, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	for y := range first.Bounds().Dy() {
		for x := range first.Bounds().Dx() {
			if got := first.NRGBAAt(x, y); got != want {
				t.Fatalf("clear pixel (%d,%d) = %#v, want %#v", x, y, got, want)
			}
		}
	}

	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	second, err := compositor.readback(first)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("readback did not reuse the supplied NRGBA")
	}
}

func TestMetalCompositorAffineOverMatchesCPU(t *testing.T) {
	const width, height = 48, 40
	sprite := testMetalSprite(13, 11)
	matrix := sc.Matrix{A: 1.37, B: 0.23, C: -0.31, D: 0.91, Tx: 12.4, Ty: 7.2}
	transform := sc.ColorTransform{RAdd: 11, GAdd: -7, BAdd: 19, AMul: 0.73, RMul: 0.81, GMul: 1.08, BMul: 0.66}

	want := image.NewNRGBA(image.Rect(0, 0, width, height))
	if err := drawBitmap(want, sprite, matrix, transform, ""); err != nil {
		t.Fatal(err)
	}
	got := renderMetalTestFrame(t, width, height, nil, sprite, matrix, transform, "", false)
	assertNRGBANear(t, got, want, 0, 0)
}

func TestMetalCompositorIntegerTranslationMatchesCPUFastPath(t *testing.T) {
	const width, height = 24, 20
	bottom := testMetalSprite(17, 15)
	top := testMetalSprite(11, 9)
	want := image.NewNRGBA(image.Rect(0, 0, width, height))
	if err := drawBitmap(want, bottom, sc.Matrix{A: 1, D: 1, Tx: 2, Ty: 1}, sc.IdentityColor(), ""); err != nil {
		t.Fatal(err)
	}
	if err := drawBitmap(want, top, sc.Matrix{A: 1, D: 1, Tx: 7, Ty: 6}, sc.IdentityColor(), ""); err != nil {
		t.Fatal(err)
	}

	compositor := requireMetalCompositor(t, width, height)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	if err := compositor.draw(bottom, sc.Matrix{A: 1, D: 1, Tx: 2, Ty: 1}, sc.IdentityColor(), "", false); err != nil {
		t.Fatal(err)
	}
	if err := compositor.draw(top, sc.Matrix{A: 1, D: 1, Tx: 7, Ty: 6}, sc.IdentityColor(), "", false); err != nil {
		t.Fatal(err)
	}
	got, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNRGBANear(t, got, want, 0, 0)
}

func TestMetalCompositorBlendModesMatchCPU(t *testing.T) {
	const width, height = 32, 28
	background := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			background.SetNRGBA(x, y, color.NRGBA{R: uint8(30 + x*3), G: uint8(45 + y*4), B: uint8(100 + (x+y)%80), A: uint8(150 + (x+y)%100)})
		}
	}
	sprite := testMetalSprite(12, 10)
	matrix := sc.Matrix{A: 1.08, B: -0.13, C: 0.19, D: 1.16, Tx: 8.3, Ty: 7.7}

	for _, tc := range []struct {
		blend    string
		coverage bool
	}{
		{blend: ""},
		{blend: "add"},
		{blend: "add", coverage: true},
		{blend: "screen"},
		{blend: "multiply"},
	} {
		t.Run(fmt.Sprintf("%s/coverage=%t", tc.blend, tc.coverage), func(t *testing.T) {
			want := cloneNRGBA(background)
			if err := drawBitmapWithCoverage(want, sprite, matrix, sc.IdentityColor(), tc.blend, tc.coverage); err != nil {
				t.Fatal(err)
			}
			got := renderMetalTestFrame(t, width, height, background, sprite, matrix, sc.IdentityColor(), tc.blend, tc.coverage)
			assertNRGBANear(t, got, want, 0, 0)
		})
	}
}

func TestMetalCompositorCachesSpritesAcrossFrames(t *testing.T) {
	compositor := requireMetalCompositor(t, 24, 24)
	defer compositor.close()
	sprite := testMetalSprite(8, 8)
	for range 2 {
		if err := compositor.beginFrame(); err != nil {
			t.Fatal(err)
		}
		if err := compositor.clear(color.NRGBA{}); err != nil {
			t.Fatal(err)
		}
		if err := compositor.draw(sprite, sc.Matrix{A: 1, D: 1, Tx: 4, Ty: 5}, sc.IdentityColor(), "", false); err != nil {
			t.Fatal(err)
		}
		if _, err := compositor.readback(nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := compositor.cachedSpriteCount(); got != 1 {
		t.Fatalf("cached sprite count = %d, want 1", got)
	}
}

func TestMetalCompositorSubimagesUseDistinctCachedRegions(t *testing.T) {
	atlas := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for y := range 4 {
		for x := range 8 {
			pixel := color.NRGBA{R: 255, A: 255}
			if x >= 4 {
				pixel = color.NRGBA{B: 255, A: 255}
			}
			atlas.SetNRGBA(x, y, pixel)
		}
	}
	left := atlas.SubImage(image.Rect(0, 0, 4, 4)).(*image.NRGBA)
	right := atlas.SubImage(image.Rect(4, 0, 8, 4)).(*image.NRGBA)
	compositor := requireMetalCompositor(t, 8, 4)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	if err := compositor.draw(left, sc.IdentityMatrix(), sc.IdentityColor(), "", false); err != nil {
		t.Fatal(err)
	}
	if err := compositor.draw(right, sc.Matrix{A: 1, D: 1, Tx: 4}, sc.IdentityColor(), "", false); err != nil {
		t.Fatal(err)
	}
	result, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.NRGBAAt(1, 1); got != (color.NRGBA{R: 255, A: 255}) {
		t.Fatalf("left subimage pixel = %#v", got)
	}
	if got := result.NRGBAAt(6, 1); got != (color.NRGBA{B: 255, A: 255}) {
		t.Fatalf("right subimage pixel = %#v", got)
	}
	if got := compositor.cachedSpriteCount(); got != 2 {
		t.Fatalf("cached sprite count = %d, want 2", got)
	}
}

func TestMetalCompositorAdditiveCoverageMatchesCPUOnTransparency(t *testing.T) {
	sprite := image.NewNRGBA(image.Rect(0, 0, 5, 5))
	sprite.SetNRGBA(2, 2, color.NRGBA{R: 255, G: 120, B: 40, A: 230})
	matrix := sc.Matrix{A: 1, D: 1, Tx: 3, Ty: 3}
	for _, allow := range []bool{false, true} {
		want := image.NewNRGBA(image.Rect(0, 0, 11, 11))
		if err := drawBitmapWithCoverage(want, sprite, matrix, sc.IdentityColor(), "add", allow); err != nil {
			t.Fatal(err)
		}
		got := renderMetalTestFrame(t, 11, 11, nil, sprite, matrix, sc.IdentityColor(), "add", allow)
		assertNRGBANear(t, got, want, 0, 0)
	}
}

func TestMetalCompositorMaskedDrawMatchesCPU(t *testing.T) {
	const width, height = 37, 31
	sprite := testMetalSprite(17, 13)
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8((x*17 + y*29) % 256)})
		}
	}
	matrix := sc.Matrix{A: 1.17, B: -0.19, C: 0.23, D: 0.94, Tx: 8.4, Ty: 7.1}
	transform := sc.ColorTransform{RAdd: 7, GAdd: -5, BAdd: 11, AMul: 0.81, RMul: 0.92, GMul: 1.03, BMul: 0.74}

	want := image.NewNRGBA(image.Rect(0, 0, width, height))
	if err := drawBitmapMasked(want, sprite, matrix, transform, "", mask, false); err != nil {
		t.Fatal(err)
	}
	compositor := requireMetalCompositor(t, width, height)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawMasked(sprite, matrix, transform, "", false, mask); err != nil {
		t.Fatal(err)
	}
	got, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNRGBANear(t, got, want, 0, 0)
}

func TestMetalCompositorSurfaceMaskCombinationMatchesCPU(t *testing.T) {
	const width, height = 19, 17
	firstMask := image.NewNRGBA(image.Rect(0, 0, width, height))
	secondMask := image.NewNRGBA(image.Rect(0, 0, width, height))
	content := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			firstMask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8((x * 255) / (width - 1))})
			secondMask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8((y * 255) / (height - 1))})
			content.SetNRGBA(x, y, color.NRGBA{R: uint8(30 + x*7), G: uint8(40 + y*9), B: 190, A: 230})
		}
	}
	want := image.NewNRGBA(image.Rect(0, 0, width, height))
	if err := drawBitmapMasked(want, content, sc.IdentityMatrix(), sc.IdentityColor(), "", combineAlphaMasks(firstMask, secondMask), false); err != nil {
		t.Fatal(err)
	}

	compositor := requireMetalCompositor(t, width, height)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	firstSurface, err := compositor.acquireSurface()
	if err != nil {
		t.Fatal(err)
	}
	secondSurface, err := compositor.acquireSurface()
	if err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(firstSurface, firstMask, sc.IdentityMatrix(), sc.IdentityColor(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(secondSurface, secondMask, sc.IdentityMatrix(), sc.IdentityColor(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	combined, err := compositor.combineMasks(firstSurface, secondSurface)
	if err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(nil, content, sc.IdentityMatrix(), sc.IdentityColor(), "", false, combined); err != nil {
		t.Fatal(err)
	}
	got, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNRGBANear(t, got, want, 0, 0)
}

func TestMetalCompositorCombinesGPUResidentMasks(t *testing.T) {
	const width, height = 29, 23
	firstMask := image.NewNRGBA(image.Rect(0, 0, width, height))
	secondMask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			firstMask.SetNRGBA(x, y, color.NRGBA{R: 255, A: uint8((x*13 + y*7) % 256)})
			secondMask.SetNRGBA(x, y, color.NRGBA{G: 255, A: uint8((x*5 + y*19 + 31) % 256)})
		}
	}
	sprite := testMetalSprite(15, 11)
	matrix := sc.Matrix{A: 1.09, B: 0.11, C: -0.17, D: 0.97, Tx: 7.3, Ty: 5.6}

	wantMask := combineAlphaMasks(firstMask, secondMask)
	want := image.NewNRGBA(image.Rect(0, 0, width, height))
	if err := drawBitmapMasked(want, sprite, matrix, sc.IdentityColor(), "", wantMask, false); err != nil {
		t.Fatal(err)
	}

	compositor := requireMetalCompositor(t, width, height)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	first, err := compositor.acquireSurface()
	if err != nil {
		t.Fatal(err)
	}
	second, err := compositor.acquireSurface()
	if err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(first, firstMask, sc.IdentityMatrix(), sc.IdentityColor(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(second, secondMask, sc.IdentityMatrix(), sc.IdentityColor(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	combined, err := compositor.combineMasks(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := compositor.drawTo(nil, sprite, matrix, sc.IdentityColor(), "", false, combined); err != nil {
		t.Fatal(err)
	}
	compositor.releaseSurface(combined)
	compositor.releaseSurface(second)
	compositor.releaseSurface(first)
	got, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNRGBANear(t, got, want, 0, 0)
	if gotSurfaces := len(compositor.surfaces); gotSurfaces != 3 {
		t.Fatalf("surface pool size = %d, want 3", gotSurfaces)
	}
}

func TestMetalExporterNestedMasksMatchCPU(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	texture.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	texture.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 191})
	texture.SetNRGBA(2, 0, color.NRGBA{G: 255, A: 255})
	texture.SetNRGBA(3, 0, color.NRGBA{B: 255, A: 255})
	shape := func(id uint16, textureX, outputX float64) *sc.Shape {
		return &sc.Shape{ID: id, Bitmaps: []sc.ShapeBitmap{{
			TextureIndex: 0,
			UVCoords:     []sc.Point{{X: textureX, Y: 0}},
			XYCoords:     []sc.Point{{X: outputX, Y: 0}},
		}}}
	}
	inner := &sc.MovieClip{ID: 10, FrameRate: 1, MatrixBank: 0,
		Binds:  []sc.Bind{{ID: 11}, {ID: 12}, {ID: 13}, {ID: 14}, {ID: 15}},
		Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4}}}},
	}
	outer := &sc.MovieClip{ID: 1, FrameRate: 1, MatrixBank: 0,
		Binds:  []sc.Bind{{ID: 2}, {ID: 3}, {ID: 4}, {ID: 10}, {ID: 5}, {ID: 6}},
		Frames: []sc.MovieClipFrame{{Elements: []sc.FrameElement{{Bind: 0}, {Bind: 1}, {Bind: 2}, {Bind: 3}, {Bind: 4}, {Bind: 5, Matrix: 1}}}},
	}
	swf := &sc.SWF{
		Textures: []*sc.Texture{{Image: texture}},
		Resources: map[uint16]sc.Resource{
			1: outer, 2: &sc.MovieClipModifier{ID: 2, Modifier: 38}, 3: shape(3, 0, 0),
			4: &sc.MovieClipModifier{ID: 4, Modifier: 39}, 5: &sc.MovieClipModifier{ID: 5, Modifier: 40},
			6:  shape(6, 3, 0),
			10: inner, 11: &sc.MovieClipModifier{ID: 11, Modifier: 38}, 12: shape(12, 1, 0),
			13: &sc.MovieClipModifier{ID: 13, Modifier: 39}, 14: shape(14, 2, 0), 15: &sc.MovieClipModifier{ID: 15, Modifier: 40},
		},
		MatrixBanks: []*sc.MatrixBank{{Matrices: []sc.Matrix{{A: 1, D: 1}, {A: 1, D: 1, Tx: 1}}}},
	}
	exporter := NewExporter(swf)
	target := Target{Name: "nested-mask", ResourceID: 1, Resource: outer}
	cache := map[bitmapCacheKey]*bitmapRenderable{}
	bounds, err := exporter.collectBounds(target, 0, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	want, err := exporter.renderSceneAtInto(target, 0, bounds, cache, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compositor := requireMetalCompositor(t, want.Bounds().Dx(), want.Bounds().Dy())
	defer compositor.close()
	got, err := exporter.renderSceneMetalAtInto(target, 0, bounds, cache, nil, compositor, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNRGBANear(t, got, want, 0, 0)
	postMaskSiblingFound := false
	for y := range got.Bounds().Dy() {
		for x := range got.Bounds().Dx() {
			pixel := got.NRGBAAt(x, y)
			postMaskSiblingFound = postMaskSiblingFound || (pixel.B == 255 && pixel.A != 0)
		}
	}
	if !postMaskSiblingFound {
		t.Fatalf("post-mask sibling was not rendered; bounds=%v pixels=%v", got.Bounds(), got.Pix)
	}
	for pass := 0; pass < 2; pass++ {
		got, err = exporter.renderSceneMetalAtInto(target, 0, bounds, cache, nil, compositor, got)
		if err != nil {
			t.Fatal(err)
		}
		assertNRGBANear(t, got, want, 0, 0)
	}
	if got := compositor.cachedMaskCount(); got != 2 {
		t.Fatalf("persistent mask count = %d, want 2", got)
	}
}

func TestMetalCompositorReportsUnsupportedConditions(t *testing.T) {
	compositor := requireMetalCompositor(t, 16, 16)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	sprite := testMetalSprite(4, 4)
	if err := compositor.draw(sprite, sc.IdentityMatrix(), sc.IdentityColor(), "overlay", false); !errors.Is(err, errMetalCompositorUnsupported) {
		t.Fatalf("unsupported blend error = %v", err)
	}
}

func BenchmarkMetalCompositorAffineScene(b *testing.B) {
	const width, height = 1024, 1024
	sprite := testMetalSprite(192, 192)
	floor := borderLuminanceFloor(sprite)
	type draw struct {
		matrix   sc.Matrix
		blend    string
		coverage bool
	}
	draws := make([]draw, 0, 48)
	blends := []string{"", "add", "screen", "multiply"}
	for i := range 48 {
		angle := float64(i%12) * math.Pi / 31
		scale := 0.72 + float64(i%7)*0.09
		draws = append(draws, draw{
			matrix: sc.Matrix{
				A:  math.Cos(angle) * scale,
				B:  math.Sin(angle) * scale,
				C:  -math.Sin(angle) * scale,
				D:  math.Cos(angle) * scale,
				Tx: float64(70 + (i%8)*122),
				Ty: float64(85 + (i/8)*151),
			},
			blend:    blends[i%len(blends)],
			coverage: i%3 == 0,
		})
	}

	b.Run("CPU", func(b *testing.B) {
		canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
		b.ReportAllocs()
		for range b.N {
			clear(canvas.Pix)
			for _, item := range draws {
				if err := drawBitmapMaskedWithFloor(canvas, sprite, item.matrix, sc.IdentityColor(), item.blend, nil, item.coverage, floor); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("Metal", func(b *testing.B) {
		compositor := requireMetalCompositor(b, width, height)
		defer compositor.close()
		var output *image.NRGBA
		// Compile the pipeline and upload the immutable sprite outside timing.
		if err := compositor.beginFrame(); err != nil {
			b.Fatal(err)
		}
		if err := compositor.clear(color.NRGBA{}); err != nil {
			b.Fatal(err)
		}
		if err := compositor.draw(sprite, draws[0].matrix, sc.IdentityColor(), draws[0].blend, draws[0].coverage); err != nil {
			b.Fatal(err)
		}
		var err error
		output, err = compositor.readback(output)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if err := compositor.beginFrame(); err != nil {
				b.Fatal(err)
			}
			if err := compositor.clear(color.NRGBA{}); err != nil {
				b.Fatal(err)
			}
			for _, item := range draws {
				if err := compositor.draw(sprite, item.matrix, sc.IdentityColor(), item.blend, item.coverage); err != nil {
					b.Fatal(err)
				}
			}
			output, err = compositor.readback(output)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func renderMetalTestFrame(t *testing.T, width, height int, background, sprite *image.NRGBA, matrix sc.Matrix, transform sc.ColorTransform, blend string, coverage bool) *image.NRGBA {
	t.Helper()
	compositor := requireMetalCompositor(t, width, height)
	defer compositor.close()
	if err := compositor.beginFrame(); err != nil {
		t.Fatal(err)
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		t.Fatal(err)
	}
	if background != nil {
		if err := compositor.draw(background, sc.IdentityMatrix(), sc.IdentityColor(), "", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := compositor.draw(sprite, matrix, transform, blend, coverage); err != nil {
		t.Fatal(err)
	}
	result, err := compositor.readback(nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireMetalCompositor(t testing.TB, width, height int) *metalCompositor {
	t.Helper()
	compositor, err := newMetalCompositor(width, height)
	if err == nil {
		return compositor
	}
	if errors.Is(err, errMetalCompositorUnavailable) || strings.Contains(err.Error(), "no Metal device") {
		t.Skip("Metal device is unavailable in the process sandbox")
	}
	t.Fatalf("newMetalCompositor() error = %v", err)
	return nil
}

func testMetalSprite(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			dx := float64(x) - float64(width-1)/2
			dy := float64(y) - float64(height-1)/2
			distance := math.Sqrt(dx*dx + dy*dy)
			alpha := maxInt(0, 255-int(distance*31))
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*37 + y*11 + 29) % 256),
				G: uint8((x*17 + y*43 + 71) % 256),
				B: uint8((x*53 + y*7 + 113) % 256),
				A: uint8(alpha),
			})
		}
	}
	return img
}

func assertNRGBANear(t *testing.T, got, want *image.NRGBA, tolerance, allowedOutliers int) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
	outliers := 0
	maximumDifference := 0
	var first string
	for y := got.Bounds().Min.Y; y < got.Bounds().Max.Y; y++ {
		for x := got.Bounds().Min.X; x < got.Bounds().Max.X; x++ {
			actual := got.NRGBAAt(x, y)
			expected := want.NRGBAAt(x, y)
			difference := maxInt(absByteDifference(actual.R, expected.R), maxInt(absByteDifference(actual.G, expected.G), maxInt(absByteDifference(actual.B, expected.B), absByteDifference(actual.A, expected.A))))
			maximumDifference = maxInt(maximumDifference, difference)
			if difference > tolerance {
				outliers++
				if first == "" {
					first = fmt.Sprintf("(%d,%d): got=%#v want=%#v difference=%d", x, y, actual, expected, difference)
				}
			}
		}
	}
	if outliers > allowedOutliers {
		t.Fatalf("images differ: outliers=%d allowed=%d max_difference=%d first=%s", outliers, allowedOutliers, maximumDifference, first)
	}
}

func absByteDifference(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
