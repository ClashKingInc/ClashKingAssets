package sc

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

var shapeBitmapTags = map[uint8]bool{
	4:  true,
	17: true,
	22: true,
}

func loadShape(reader *Reader, tag uint8, textures []*Texture) (*Shape, error) {
	id, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	bitmapCount, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	if tag == 18 {
		if _, err := reader.ReadU16(); err != nil {
			return nil, err
		}
	}

	shape := &Shape{ID: id, Bitmaps: make([]ShapeBitmap, 0, bitmapCount)}
	for {
		bitmapTag, err := reader.ReadU8()
		if err != nil {
			return nil, err
		}
		bitmapTagLength, err := reader.ReadI32()
		if err != nil {
			return nil, err
		}
		bitmapEnd, err := reader.SectionEnd(int(bitmapTagLength))
		if err != nil {
			return nil, fmt.Errorf("shape %d bitmap tag %d length %d: %w", id, bitmapTag, bitmapTagLength, err)
		}

		if bitmapTag == 0 {
			return shape, nil
		}
		if !shapeBitmapTags[bitmapTag] {
			if err := reader.Seek(bitmapEnd); err != nil {
				return nil, err
			}
			continue
		}

		bitmap, err := loadShapeBitmap(reader, bitmapTag, textures)
		if err != nil {
			return nil, err
		}
		shape.Bitmaps = append(shape.Bitmaps, bitmap)
		if reader.Pos() > bitmapEnd {
			return nil, fmt.Errorf("shape %d bitmap tag %d consumed past its declared end: pos=%d end=%d", id, bitmapTag, reader.Pos(), bitmapEnd)
		}
		if reader.Pos() < bitmapEnd {
			if err := reader.Seek(bitmapEnd); err != nil {
				return nil, err
			}
		}
	}
}

func loadShapeBitmap(reader *Reader, tag uint8, textures []*Texture) (ShapeBitmap, error) {
	textureIndex, err := reader.ReadU8()
	if err != nil {
		return ShapeBitmap{}, err
	}
	maxRects := tag == 4
	pointsCount := 4
	if !maxRects {
		pc, err := reader.ReadU8()
		if err != nil {
			return ShapeBitmap{}, err
		}
		pointsCount = int(pc)
	}
	bitmap := ShapeBitmap{
		TextureIndex: int(textureIndex),
		MaxRects:     maxRects,
		XYCoords:     make([]Point, 0, pointsCount),
		UVCoords:     make([]Point, 0, pointsCount),
	}

	for i := 0; i < pointsCount; i++ {
		x, err := reader.ReadTwip()
		if err != nil {
			return ShapeBitmap{}, err
		}
		y, err := reader.ReadTwip()
		if err != nil {
			return ShapeBitmap{}, err
		}
		bitmap.XYCoords = append(bitmap.XYCoords, Point{X: x, Y: y})
	}

	texWidth, texHeight := 0.0, 0.0
	if int(textureIndex) < len(textures) && textures[textureIndex] != nil {
		texWidth = float64(textures[textureIndex].Width)
		texHeight = float64(textures[textureIndex].Height)
	}
	for i := 0; i < pointsCount; i++ {
		u16, err := reader.ReadU16()
		if err != nil {
			return ShapeBitmap{}, err
		}
		v16, err := reader.ReadU16()
		if err != nil {
			return ShapeBitmap{}, err
		}
		u := float64(u16)
		v := float64(v16)
		if tag == 22 {
			u = float64(u16) / 65535.0 * texWidth
			v = float64(v16) / 65535.0 * texHeight
		}
		bitmap.UVCoords = append(bitmap.UVCoords, Point{
			X: math.Ceil(u),
			Y: math.Ceil(v),
		})
	}

	return bitmap, nil
}

func (b ShapeBitmap) UVBounds() (left, top, right, bottom int) {
	if len(b.UVCoords) == 0 {
		return 0, 0, 1, 1
	}
	minX := b.UVCoords[0].X
	maxX := b.UVCoords[0].X
	minY := b.UVCoords[0].Y
	maxY := b.UVCoords[0].Y
	for _, p := range b.UVCoords[1:] {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}

	left = int(minX)
	top = int(minY)
	right = int(maxX)
	bottom = int(maxY)
	if right-left == 0 {
		right++
	}
	if bottom-top == 0 {
		bottom++
	}
	return
}

func (b ShapeBitmap) SpriteImage(textures []*Texture) (*image.NRGBA, error) {
	if b.TextureIndex < 0 || b.TextureIndex >= len(textures) || textures[b.TextureIndex] == nil {
		return nil, fmt.Errorf("texture %d is not available", b.TextureIndex)
	}
	texture := textures[b.TextureIndex]
	if texture.Image == nil {
		if texture.LoadError != "" {
			return nil, fmt.Errorf("texture %d failed to decode: %s", b.TextureIndex, texture.LoadError)
		}
		return nil, fmt.Errorf("texture %d image is not loaded", b.TextureIndex)
	}
	src := texture.Image
	if len(b.SolidTriangles) != 0 {
		return b.solidSpriteImage(src), nil
	}
	if len(b.UVCoords) == 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1)), nil
	}

	w, h := b.uvSize()
	if w == 1 && h == 1 {
		x := int(b.UVCoords[0].X)
		y := int(b.UVCoords[0].Y)
		if image.Pt(x, y).In(src.Rect) {
			c := src.NRGBAAt(x, y)
			sprite := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			sprite.SetNRGBA(0, 0, c)
			return sprite, nil
		}
		return image.NewNRGBA(image.Rect(0, 0, 1, 1)), nil
	}

	left, top, right, bottom := b.UVBounds()
	sprite := image.NewNRGBA(image.Rect(0, 0, right-left, bottom-top))
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			if !pointInPolygon(float64(x)+0.5, float64(y)+0.5, b.UVCoords) && !pointNearPolygonEdge(float64(x)+0.5, float64(y)+0.5, b.UVCoords, 0.75) {
				continue
			}
			if !image.Pt(x, y).In(src.Rect) {
				continue
			}
			sprite.SetNRGBA(x-left, y-top, src.NRGBAAt(x, y))
		}
	}
	return sprite, nil
}

func (b ShapeBitmap) solidSpriteImage(texture *image.NRGBA) *image.NRGBA {
	minX, minY, maxX, maxY, ok := pointBounds(b.SolidTriangles)
	if !ok {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	left, top := int(math.Floor(minX)), int(math.Floor(minY))
	right, bottom := int(math.Ceil(maxX)), int(math.Ceil(maxY))
	if right <= left {
		right = left + 1
	}
	if bottom <= top {
		bottom = top + 1
	}
	sprite := image.NewNRGBA(image.Rect(0, 0, right-left, bottom-top))
	sample := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if len(b.UVCoords) != 0 {
		x, y := int(b.UVCoords[0].X), int(b.UVCoords[0].Y)
		if image.Pt(x, y).In(texture.Bounds()) {
			sample = texture.NRGBAAt(x, y)
		}
	}
	samples := [...]Point{{X: 0.25, Y: 0.25}, {X: 0.75, Y: 0.25}, {X: 0.25, Y: 0.75}, {X: 0.75, Y: 0.75}}
	for index := 0; index+2 < len(b.SolidTriangles); index += 3 {
		triangle := b.SolidTriangles[index : index+3]
		triMinX, triMinY, triMaxX, triMaxY, _ := pointBounds(triangle)
		startX := maxIntShape(0, int(math.Floor(triMinX))-left)
		startY := maxIntShape(0, int(math.Floor(triMinY))-top)
		endX := minIntShape(sprite.Bounds().Dx(), int(math.Ceil(triMaxX))-left)
		endY := minIntShape(sprite.Bounds().Dy(), int(math.Ceil(triMaxY))-top)
		for y := startY; y < endY; y++ {
			for x := startX; x < endX; x++ {
				coverage := 0
				for _, offset := range samples {
					if pointInTriangle(float64(left+x)+offset.X, float64(top+y)+offset.Y, triangle) {
						coverage++
					}
				}
				if coverage == 0 {
					continue
				}
				alpha := uint8((int(sample.A)*coverage + len(samples)/2) / len(samples))
				if alpha <= sprite.NRGBAAt(x, y).A {
					continue
				}
				sprite.SetNRGBA(x, y, color.NRGBA{R: sample.R, G: sample.G, B: sample.B, A: alpha})
			}
		}
	}
	return sprite
}

func pointInTriangle(x, y float64, triangle []Point) bool {
	if len(triangle) != 3 {
		return false
	}
	sign := func(a, b Point) float64 {
		return (x-b.X)*(a.Y-b.Y) - (a.X-b.X)*(y-b.Y)
	}
	d1 := sign(triangle[0], triangle[1])
	d2 := sign(triangle[1], triangle[2])
	d3 := sign(triangle[2], triangle[0])
	hasNegative := d1 < -1e-8 || d2 < -1e-8 || d3 < -1e-8
	hasPositive := d1 > 1e-8 || d2 > 1e-8 || d3 > 1e-8
	return !(hasNegative && hasPositive)
}

func maxIntShape(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntShape(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (b ShapeBitmap) LocalTransform() (Matrix, error) {
	if len(b.SolidTriangles) != 0 {
		minX, minY, _, _, ok := pointBounds(b.SolidTriangles)
		if !ok {
			return IdentityMatrix(), nil
		}
		return Matrix{A: 1, D: 1, Tx: math.Floor(minX), Ty: math.Floor(minY)}, nil
	}
	left, top, right, bottom := b.UVBounds()
	local := make([]Point, len(b.UVCoords))
	for i, p := range b.UVCoords {
		local[i] = Point{X: p.X - float64(left), Y: p.Y - float64(top)}
	}
	transform, err := solveAffine(local, b.XYCoords)
	if err == nil {
		return transform, nil
	}
	if fallback, ok := fallbackAffine(local, b.XYCoords); ok {
		return fallback, nil
	}
	return IdentityMatrix(), b.wrapLocalTransformError(local, left, top, right, bottom, err)
}

func (b ShapeBitmap) uvSize() (int, int) {
	left, top, right, bottom := b.UVBounds()
	return right - left, bottom - top
}

func pointInPolygon(x, y float64, poly []Point) bool {
	inside := false
	for i, pi := range poly {
		pj := poly[(i+len(poly)-1)%len(poly)]
		intersect := ((pi.Y > y) != (pj.Y > y)) &&
			(x < (pj.X-pi.X)*(y-pi.Y)/(pj.Y-pi.Y+1e-12)+pi.X)
		if intersect {
			inside = !inside
		}
	}
	return inside
}

func pointOnPolygonEdge(x, y float64, poly []Point) bool {
	for i, a := range poly {
		b := poly[(i+1)%len(poly)]
		if pointOnSegment(x, y, a, b) {
			return true
		}
	}
	return false
}

func pointNearPolygonEdge(x, y float64, poly []Point, tolerance float64) bool {
	for i, a := range poly {
		b := poly[(i+1)%len(poly)]
		if pointSegmentDistance(x, y, a, b) <= tolerance {
			return true
		}
	}
	return false
}

func pointSegmentDistance(x, y float64, a, b Point) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	lengthSq := dx*dx + dy*dy
	if lengthSq == 0 {
		return math.Hypot(x-a.X, y-a.Y)
	}
	t := ((x-a.X)*dx + (y-a.Y)*dy) / lengthSq
	t = math.Max(0, math.Min(1, t))
	closestX := a.X + t*dx
	closestY := a.Y + t*dy
	return math.Hypot(x-closestX, y-closestY)
}

func pointOnSegment(x, y float64, a, b Point) bool {
	cross := (x-a.X)*(b.Y-a.Y) - (y-a.Y)*(b.X-a.X)
	if math.Abs(cross) > 1e-6 {
		return false
	}
	dot := (x-a.X)*(b.X-a.X) + (y-a.Y)*(b.Y-a.Y)
	if dot < 0 {
		return false
	}
	lengthSq := (b.X-a.X)*(b.X-a.X) + (b.Y-a.Y)*(b.Y-a.Y)
	return dot <= lengthSq+1e-6
}

func solveAffine(src, dst []Point) (Matrix, error) {
	if len(src) != len(dst) || len(src) == 0 {
		return IdentityMatrix(), fmt.Errorf("invalid affine point set")
	}
	if len(src) == 1 {
		return Matrix{A: 1, D: 1, Tx: dst[0].X - src[0].X, Ty: dst[0].Y - src[0].Y}, nil
	}

	var m [3][3]float64
	var bx [3]float64
	var by [3]float64
	for i := range src {
		u, v := src[i].X, src[i].Y
		x, y := dst[i].X, dst[i].Y
		row := [3]float64{u, v, 1}
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				m[r][c] += row[r] * row[c]
			}
			bx[r] += row[r] * x
			by[r] += row[r] * y
		}
	}

	sx, err := solve3x3(m, bx)
	if err != nil {
		return IdentityMatrix(), err
	}
	sy, err := solve3x3(m, by)
	if err != nil {
		return IdentityMatrix(), err
	}

	return Matrix{
		A:  sx[0],
		C:  sx[1],
		Tx: sx[2],
		B:  sy[0],
		D:  sy[1],
		Ty: sy[2],
	}, nil
}

func fallbackAffine(src, dst []Point) (Matrix, bool) {
	if len(src) != len(dst) || len(src) == 0 {
		return Matrix{}, false
	}

	srcMinX, srcMinY, srcMaxX, srcMaxY, ok := pointBounds(src)
	if !ok {
		return Matrix{}, false
	}
	dstMinX, dstMinY, dstMaxX, dstMaxY, ok := pointBounds(dst)
	if !ok {
		return Matrix{}, false
	}

	matrix := IdentityMatrix()
	srcWidth := srcMaxX - srcMinX
	dstWidth := dstMaxX - dstMinX
	if math.Abs(srcWidth) > 1e-6 {
		matrix.A = dstWidth / srcWidth
		matrix.Tx = dstMinX - matrix.A*srcMinX
	} else {
		matrix.Tx = dstMinX - srcMinX
	}

	srcHeight := srcMaxY - srcMinY
	dstHeight := dstMaxY - dstMinY
	if math.Abs(srcHeight) > 1e-6 {
		matrix.D = dstHeight / srcHeight
		matrix.Ty = dstMinY - matrix.D*srcMinY
	} else {
		matrix.Ty = dstMinY - srcMinY
	}

	return matrix, true
}

func solve3x3(m [3][3]float64, b [3]float64) ([3]float64, error) {
	aug := [3][4]float64{}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			aug[i][j] = m[i][j]
		}
		aug[i][3] = b[i]
	}

	for col := 0; col < 3; col++ {
		pivot := col
		for row := col + 1; row < 3; row++ {
			if math.Abs(aug[row][col]) > math.Abs(aug[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(aug[pivot][col]) < 1e-10 {
			return [3]float64{}, fmt.Errorf("singular affine system")
		}
		if pivot != col {
			aug[col], aug[pivot] = aug[pivot], aug[col]
		}
		factor := aug[col][col]
		for j := col; j < 4; j++ {
			aug[col][j] /= factor
		}
		for row := 0; row < 3; row++ {
			if row == col {
				continue
			}
			f := aug[row][col]
			for j := col; j < 4; j++ {
				aug[row][j] -= f * aug[col][j]
			}
		}
	}

	return [3]float64{aug[0][3], aug[1][3], aug[2][3]}, nil
}

func pointBounds(points []Point) (minX, minY, maxX, maxY float64, ok bool) {
	if len(points) == 0 {
		return 0, 0, 0, 0, false
	}
	minX, maxX = points[0].X, points[0].X
	minY, maxY = points[0].Y, points[0].Y
	for _, point := range points[1:] {
		if point.X < minX {
			minX = point.X
		}
		if point.X > maxX {
			maxX = point.X
		}
		if point.Y < minY {
			minY = point.Y
		}
		if point.Y > maxY {
			maxY = point.Y
		}
	}
	return minX, minY, maxX, maxY, true
}

func (b ShapeBitmap) wrapLocalTransformError(local []Point, left, top, right, bottom int, err error) error {
	return fmt.Errorf(
		"%w local=%s world=%s uv_bounds=[%d,%d,%d,%d] max_rects=%t texture_index=%d",
		err,
		formatPoints(local),
		formatPoints(b.XYCoords),
		left,
		top,
		right,
		bottom,
		b.MaxRects,
		b.TextureIndex,
	)
}

func formatPoints(points []Point) string {
	if len(points) == 0 {
		return "[]"
	}
	out := "["
	for i, point := range points {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("(%.3f,%.3f)", point.X, point.Y)
	}
	out += "]"
	return out
}

func toNRGBA(img image.Image) *image.NRGBA {
	if out, ok := img.(*image.NRGBA); ok {
		return out
	}
	b := img.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func colorToNRGBA(c color.Color) color.NRGBA {
	r, g, b, a := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}
