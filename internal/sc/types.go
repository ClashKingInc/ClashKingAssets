package sc

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

type Resource interface {
	ResourceType() string
}

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

func (r Rect) Empty() bool {
	return r.MaxX <= r.MinX || r.MaxY <= r.MinY
}

func (r *Rect) ExpandTo(x, y float64) {
	if r.Empty() {
		r.MinX, r.MaxX = x, x
		r.MinY, r.MaxY = y, y
		return
	}
	if x < r.MinX {
		r.MinX = x
	}
	if x > r.MaxX {
		r.MaxX = x
	}
	if y < r.MinY {
		r.MinY = y
	}
	if y > r.MaxY {
		r.MaxY = y
	}
}

type Matrix struct {
	A  float64
	B  float64
	C  float64
	D  float64
	Tx float64
	Ty float64
}

func IdentityMatrix() Matrix {
	return Matrix{A: 1, D: 1}
}

func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.Tx, m.B*x + m.D*y + m.Ty
}

func (m Matrix) Multiply(other Matrix) Matrix {
	return Matrix{
		A:  m.A*other.A + m.C*other.B,
		B:  m.B*other.A + m.D*other.B,
		C:  m.A*other.C + m.C*other.D,
		D:  m.B*other.C + m.D*other.D,
		Tx: m.A*other.Tx + m.C*other.Ty + m.Tx,
		Ty: m.B*other.Tx + m.D*other.Ty + m.Ty,
	}
}

func (m Matrix) Inverse() (Matrix, error) {
	det := m.A*m.D - m.B*m.C
	if math.Abs(det) < 1e-10 {
		return Matrix{}, fmt.Errorf("singular matrix")
	}
	return Matrix{
		A:  m.D / det,
		B:  -m.B / det,
		C:  -m.C / det,
		D:  m.A / det,
		Tx: (m.C*m.Ty - m.D*m.Tx) / det,
		Ty: (m.B*m.Tx - m.A*m.Ty) / det,
	}, nil
}

type ColorTransform struct {
	RAdd float64
	GAdd float64
	BAdd float64
	AMul float64
	RMul float64
	GMul float64
	BMul float64
}

func IdentityColor() ColorTransform {
	return ColorTransform{
		AMul: 1,
		RMul: 1,
		GMul: 1,
		BMul: 1,
	}
}

func (c ColorTransform) Combine(next ColorTransform) ColorTransform {
	return ColorTransform{
		RAdd: next.RAdd*c.RMul + c.RAdd,
		GAdd: next.GAdd*c.GMul + c.GAdd,
		BAdd: next.BAdd*c.BMul + c.BAdd,
		AMul: c.AMul * next.AMul,
		RMul: c.RMul * next.RMul,
		GMul: c.GMul * next.GMul,
		BMul: c.BMul * next.BMul,
	}
}

func (c ColorTransform) Apply(px color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: clampByte(float64(px.R)*c.RMul + c.RAdd),
		G: clampByte(float64(px.G)*c.GMul + c.GAdd),
		B: clampByte(float64(px.B)*c.BMul + c.BAdd),
		A: clampByte(float64(px.A) * c.AMul),
	}
}

func clampByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(math.Round(v))
	}
}

type MatrixBank struct {
	Index                int
	Matrices             []Matrix
	ColorTransforms      []ColorTransform
	MatricesCount        int
	ColorTransformsCount int
}

type Shape struct {
	ID      uint16
	Bitmaps []ShapeBitmap
}

func (s *Shape) ResourceType() string { return "shape" }

type ShapeBitmap struct {
	TextureIndex int
	UVCoords     []Point
	XYCoords     []Point
	MaxRects     bool
}

type MovieClipModifier struct {
	ID       uint16
	Modifier uint8
}

func (m *MovieClipModifier) ResourceType() string { return "movieclip_modifier" }

type Bind struct {
	ID    uint16
	Blend string
	Name  string
}

type FrameElement struct {
	Bind   uint16
	Matrix uint16
	Color  uint16
}

type MovieClipFrame struct {
	Elements []FrameElement
	Name     string
}

type MovieClip struct {
	ID          uint16
	FrameRate   int
	Binds       []Bind
	Frames      []MovieClipFrame
	NineSlice   []float64
	MatrixBank  int
	UnknownFlag bool
}

func (m *MovieClip) ResourceType() string { return "movieclip" }

type TextField struct {
	ID           uint16
	FontName     string
	Text         string
	FontSize     int
	FontAlign    uint8
	Bold         bool
	Italic       bool
	Multiline    bool
	IsDynamic    bool
	Outline      bool
	FontColor    uint32
	OutlineColor uint32
	Top          int16
	Bottom       int16
	Left         int16
	Right        int16
	C1           int16
	C2           int16
}

func (t *TextField) ResourceType() string { return "text_field" }

type Texture struct {
	Channels            int
	PixelFormat         string
	PixelInternalFormat string
	PixelType           string
	MagFilter           string
	MinFilter           string
	Linear              bool
	Downscaling         bool
	Width               int
	Height              int
	Image               *image.NRGBA
	LoadError           string
}

type SWF struct {
	Filename              string
	UseUncommonTexture    bool
	UseLowResTexture      bool
	HasExternalTexture    bool
	TexturesCount         int
	MovieClipModifiers    int
	ShapesCount           int
	TextFieldsCount       int
	MovieClipsCount       int
	Textures              []*Texture
	MatrixBanks           []*MatrixBank
	Resources             map[uint16]Resource
	Exports               map[uint16][]string
	HighResTexturePostfix string
	LowResTexturePostfix  string
}
