//go:build !darwin || !cgo

package render

import (
	"image"
	"image/color"

	"sc2fla/internal/sc"
)

type metalCompositor struct{}

type metalSurface struct{}

func newMetalCompositor(_, _ int) (*metalCompositor, error) {
	return nil, errMetalCompositorUnavailable
}

func (c *metalCompositor) beginFrame() error { return errMetalCompositorUnavailable }

func (c *metalCompositor) clear(_ color.NRGBA) error { return errMetalCompositorUnavailable }

func (c *metalCompositor) draw(_ *image.NRGBA, _ sc.Matrix, _ sc.ColorTransform, _ string, _ bool) error {
	return errMetalCompositorUnavailable
}

func (c *metalCompositor) drawMasked(_ *image.NRGBA, _ sc.Matrix, _ sc.ColorTransform, _ string, _ bool, _ *image.NRGBA) error {
	return errMetalCompositorUnavailable
}

func (c *metalCompositor) drawTo(_ *metalSurface, _ *image.NRGBA, _ sc.Matrix, _ sc.ColorTransform, _ string, _ bool, _ *metalSurface) error {
	return errMetalCompositorUnavailable
}

func (c *metalCompositor) borrowedSurface(_ *image.NRGBA) (*metalSurface, error) {
	return nil, errMetalCompositorUnavailable
}

func (c *metalCompositor) acquireSurface() (*metalSurface, error) {
	return nil, errMetalCompositorUnavailable
}

func (c *metalCompositor) acquirePersistentMask(_ uint64) (*metalSurface, bool, error) {
	return nil, false, errMetalCompositorUnavailable
}

func (c *metalCompositor) clearSurface(_ *metalSurface, _ color.NRGBA) error {
	return errMetalCompositorUnavailable
}

func (c *metalCompositor) combineMasks(_, _ *metalSurface) (*metalSurface, error) {
	return nil, errMetalCompositorUnavailable
}

func (c *metalCompositor) releaseSurface(_ *metalSurface) {}

func (c *metalCompositor) readback(_ *image.NRGBA) (*image.NRGBA, error) {
	return nil, errMetalCompositorUnavailable
}

func (c *metalCompositor) cachedSpriteCount() int { return 0 }

func (c *metalCompositor) cachedMaskCount() int { return 0 }

func (c *metalCompositor) close() error { return nil }
