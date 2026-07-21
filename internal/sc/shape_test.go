package sc

import (
	"image"
	"image/color"
	"testing"
)

func TestPointNearPolygonEdgeCoversAdjacentTrianglePixels(t *testing.T) {
	triangle := []Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}}

	if !pointNearPolygonEdge(5, 5.5, triangle, 0.75) {
		t.Fatal("pixel center beside shared diagonal was not covered")
	}
	if pointNearPolygonEdge(3, 7, triangle, 0.75) {
		t.Fatal("pixel far outside triangle was covered")
	}
}

func TestDegenerateShapeTriangleRejectsStripConnectors(t *testing.T) {
	connector := []shapeVertex{
		{X: 0, Y: 0, U: 0, V: 0},
		{X: 1, Y: 1, U: 100, V: 100},
		{X: 1, Y: 1, U: 100, V: 100},
	}
	if !degenerateShapeTriangle(connector) {
		t.Fatal("repeated strip connector vertex was not rejected")
	}

	triangle := []shapeVertex{
		{X: 0, Y: 0, U: 0, V: 0},
		{X: 1, Y: 0, U: 100, V: 0},
		{X: 0, Y: 1, U: 0, V: 100},
	}
	if degenerateShapeTriangle(triangle) {
		t.Fatal("visible triangle was rejected")
	}
}

func TestDegenerateShapeTriangleAcceptsSolidColorUVs(t *testing.T) {
	solidColor := []shapeVertex{
		{X: 0, Y: 0, U: 100, V: 200},
		{X: 10, Y: 0, U: 100, V: 200},
		{X: 0, Y: 10, U: 100, V: 200},
	}
	if degenerateShapeTriangle(solidColor) {
		t.Fatal("solid-color triangle with valid XY geometry was rejected because all UVs sample one texel")
	}
}

func TestSolidColorShapeRasterizesXYGeometryFromOneTextureSample(t *testing.T) {
	texture := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	texture.SetNRGBA(2, 1, color.NRGBA{R: 255, A: 255})
	bitmap := ShapeBitmap{
		TextureIndex:   0,
		UVCoords:       []Point{{X: 2, Y: 1}},
		SolidTriangles: []Point{{X: 10, Y: 20}, {X: 14, Y: 20}, {X: 10, Y: 24}},
	}
	sprite, err := bitmap.SpriteImage([]*Texture{{Image: texture}})
	if err != nil {
		t.Fatal(err)
	}
	if sprite.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("solid sprite bounds = %v, want 4x4 triangle bounds", sprite.Bounds())
	}
	if inside := sprite.NRGBAAt(0, 0); inside.R != 255 || inside.A == 0 {
		t.Fatalf("inside solid triangle pixel = %#v, want sampled red", inside)
	}
	if outside := sprite.NRGBAAt(3, 3); outside.A != 0 {
		t.Fatalf("outside solid triangle pixel = %#v, want transparent", outside)
	}
	transform, err := bitmap.LocalTransform()
	if err != nil {
		t.Fatal(err)
	}
	if transform.Tx != 10 || transform.Ty != 20 {
		t.Fatalf("solid triangle transform = %+v, want translation to XY bounds", transform)
	}
}
