package sc

import "testing"

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
