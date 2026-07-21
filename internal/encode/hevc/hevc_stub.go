//go:build !darwin || !cgo

package hevc

import (
	"fmt"
	"image"
)

func newNativeEncoder(_ string, _, _ int, _ Options) (nativeEncoder, error) {
	return nil, fmt.Errorf("%w: requires macOS with cgo", ErrUnavailable)
}

type unavailableNativeEncoder struct{}

func (unavailableNativeEncoder) addFrame(*image.NRGBA, int64) error { return ErrUnavailable }
func (unavailableNativeEncoder) finish(int64) error                 { return ErrUnavailable }
func (unavailableNativeEncoder) abort()                             {}
