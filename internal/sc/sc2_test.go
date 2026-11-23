package sc

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestAffineShapeBitmapFromStripUnifiesQuad(t *testing.T) {
	tex := &Texture{
		Width:  64,
		Height: 64,
		Image:  image.NewNRGBA(image.Rect(0, 0, 64, 64)),
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			alpha := uint8(255 - x*4)
			tex.Image.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: alpha})
		}
	}

	toU16 := func(px, dim float64) uint16 {
		return uint16(math.Floor(px / dim * 65535.0))
	}
	// 4-vertex triangle strip representing a single axis-aligned gradient rectangle.
	points := []shapeVertex{
		{X: -150, Y: -16, U: toU16(32, 64), V: toU16(8, 64)},
		{X: -150, Y: 16, U: toU16(8, 64), V: toU16(8, 64)},
		{X: 150, Y: -16, U: toU16(32, 64), V: toU16(56, 64)},
		{X: 150, Y: 16, U: toU16(8, 64), V: toU16(56, 64)},
	}

	bm, ok := affineShapeBitmapFromStrip(points, 0, []*Texture{tex})
	if !ok {
		t.Fatal("expected affine triangle strip to unify into a single ShapeBitmap")
	}
	if len(bm.UVTriangles) != 6 {
		t.Fatalf("expected 2 triangles (6 UV vertices), got %d", len(bm.UVTriangles))
	}

	sprite, err := bm.SpriteImage([]*Texture{tex})
	if err != nil {
		t.Fatalf("SpriteImage failed: %v", err)
	}
	left, top, right, bottom := bm.UVBounds()
	if sprite.Bounds().Dx() != right-left || sprite.Bounds().Dy() != bottom-top {
		t.Fatalf("unexpected sprite size %v, want %dx%d", sprite.Bounds(), right-left, bottom-top)
	}
	// Every pixel inside the unified gradient rectangle should match the source texture
	// with no double-blended diagonal seam.
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			got := sprite.NRGBAAt(x-left, y-top)
			want := tex.Image.NRGBAAt(x, y)
			if got != want {
				t.Fatalf("pixel (%d,%d) = %+v, want %+v", x, y, got, want)
			}
		}
	}

	transform, err := bm.LocalTransform()
	if err != nil {
		t.Fatalf("LocalTransform failed: %v", err)
	}
	for i, uv := range bm.UVCoords {
		gx, gy := transform.Apply(uv.X-float64(left), uv.Y-float64(top))
		if math.Hypot(gx-bm.XYCoords[i].X, gy-bm.XYCoords[i].Y) > 1e-4 {
			t.Fatalf("vertex %d mapped to (%.4f,%.4f), want (%.4f,%.4f)", i, gx, gy, bm.XYCoords[i].X, bm.XYCoords[i].Y)
		}
	}
}

func TestAffineShapeBitmapFromStripRejectsWarpedStrip(t *testing.T) {
	tex := &Texture{Width: 64, Height: 64}
	toU16 := func(px, dim float64) uint16 {
		return uint16(math.Floor(px / dim * 65535.0))
	}
	// Fourth vertex breaks affine linearity by 40px.
	points := []shapeVertex{
		{X: 0, Y: 0, U: toU16(0, 64), V: toU16(0, 64)},
		{X: 0, Y: 100, U: toU16(0, 64), V: toU16(32, 64)},
		{X: 100, Y: 0, U: toU16(32, 64), V: toU16(0, 64)},
		{X: 140, Y: 140, U: toU16(32, 64), V: toU16(32, 64)},
	}
	if _, ok := affineShapeBitmapFromStrip(points, 0, []*Texture{tex}); ok {
		t.Fatal("expected warped non-affine strip to fall back to per-triangle triangulation")
	}
}
