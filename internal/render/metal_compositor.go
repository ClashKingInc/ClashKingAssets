package render

import "errors"

var (
	errMetalCompositorUnavailable = errors.New("metal compositor unavailable")
	errMetalCompositorUnsupported = errors.New("metal compositor unsupported")
)

type metalBlendMode uint32

const (
	metalBlendOver metalBlendMode = iota
	metalBlendAdd
	metalBlendScreen
	metalBlendMultiply
)

func parseMetalBlendMode(blend string) (metalBlendMode, error) {
	switch blend {
	case "", "over":
		return metalBlendOver, nil
	case "add":
		return metalBlendAdd, nil
	case "screen":
		return metalBlendScreen, nil
	case "multiply":
		return metalBlendMultiply, nil
	default:
		return 0, errors.Join(errMetalCompositorUnsupported, errors.New("blend mode "+blend))
	}
}
