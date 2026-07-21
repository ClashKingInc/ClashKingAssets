//go:build darwin && cgo

package render

/*
#cgo LDFLAGS: -framework Foundation -framework Metal
#include <stdint.h>
#include <stdlib.h>

typedef struct sc_metal_compositor sc_metal_compositor;
typedef void sc_metal_texture;

sc_metal_compositor *sc_metal_compositor_create(int width, int height, char **error_message);
void sc_metal_compositor_destroy(sc_metal_compositor *compositor);
int sc_metal_compositor_begin(sc_metal_compositor *compositor, char **error_message);
int sc_metal_compositor_clear(sc_metal_compositor *compositor, uint8_t r, uint8_t g, uint8_t b, uint8_t a, char **error_message);
sc_metal_texture *sc_metal_compositor_upload(
    sc_metal_compositor *compositor,
    const uint8_t *pixels,
    int width,
    int height,
    int stride,
    char **error_message
);
void sc_metal_texture_release(sc_metal_texture *texture);
sc_metal_texture *sc_metal_compositor_surface_create(sc_metal_compositor *compositor, char **error_message);
int sc_metal_compositor_surface_clear(
    sc_metal_compositor *compositor,
    sc_metal_texture *surface,
    uint8_t r,
    uint8_t g,
    uint8_t b,
    uint8_t a,
    char **error_message
);
int sc_metal_compositor_mask_combine(
    sc_metal_compositor *compositor,
    sc_metal_texture *first,
    sc_metal_texture *second,
    sc_metal_texture *output,
    char **error_message
);
int sc_metal_compositor_draw(
    sc_metal_compositor *compositor,
    sc_metal_texture *destination,
    sc_metal_texture *texture,
    sc_metal_texture *alpha_mask,
    float inverse_a,
    float inverse_b,
    float inverse_c,
    float inverse_d,
    float inverse_tx,
    float inverse_ty,
    float red_add,
    float green_add,
    float blue_add,
    float alpha_mul,
    float red_mul,
    float green_mul,
    float blue_mul,
    uint32_t blend_mode,
    uint32_t allow_additive_coverage,
    uint32_t luminance_floor,
    uint32_t nrgba_over_fast_path,
    int left,
    int top,
    int right,
    int bottom,
    char **error_message
);
int sc_metal_compositor_readback(
    sc_metal_compositor *compositor,
    uint8_t *pixels,
    int stride,
    char **error_message
);
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"runtime"
	"unsafe"

	"sc2fla/internal/sc"
)

type metalSpriteKey struct {
	sprite *image.NRGBA
	pixels unsafe.Pointer
	rect   image.Rectangle
	stride int
	length int
}

type metalCachedSprite struct {
	texture unsafe.Pointer
	floor   int
}

type metalSurface struct {
	texture    unsafe.Pointer
	inUse      bool
	persistent bool
}

type metalCompositor struct {
	handle   *C.sc_metal_compositor
	width    int
	height   int
	textures map[metalSpriteKey]metalCachedSprite
	surfaces []*metalSurface
	masks    map[uint64]*metalSurface
	maskSeen map[uint64]struct{}
	active   bool
	closed   bool
}

func newMetalCompositor(width, height int) (*metalCompositor, error) {
	if width <= 0 || height <= 0 || width > math.MaxInt32 || height > math.MaxInt32 {
		return nil, fmt.Errorf("%w: invalid canvas dimensions %dx%d", errMetalCompositorUnsupported, width, height)
	}
	var errorMessage *C.char
	handle := C.sc_metal_compositor_create(C.int(width), C.int(height), &errorMessage)
	if handle == nil {
		return nil, metalCompositorError(errorMessage)
	}
	return &metalCompositor{
		handle:   handle,
		width:    width,
		height:   height,
		textures: make(map[metalSpriteKey]metalCachedSprite),
		masks:    make(map[uint64]*metalSurface),
		maskSeen: make(map[uint64]struct{}),
	}, nil
}

func (c *metalCompositor) beginFrame() error {
	if err := c.usable(); err != nil {
		return err
	}
	if c.active {
		return fmt.Errorf("%w: a frame is already active", errMetalCompositorUnsupported)
	}
	var errorMessage *C.char
	if C.sc_metal_compositor_begin(c.handle, &errorMessage) == 0 {
		return metalCompositorError(errorMessage)
	}
	c.active = true
	return nil
}

func (c *metalCompositor) clear(clearColor color.NRGBA) error {
	if err := c.requireActive(); err != nil {
		return err
	}
	var errorMessage *C.char
	if C.sc_metal_compositor_clear(
		c.handle,
		C.uint8_t(clearColor.R),
		C.uint8_t(clearColor.G),
		C.uint8_t(clearColor.B),
		C.uint8_t(clearColor.A),
		&errorMessage,
	) == 0 {
		return metalCompositorError(errorMessage)
	}
	return nil
}

func (c *metalCompositor) draw(sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, allowAdditiveCoverage bool) error {
	return c.drawMasked(sprite, matrix, colorTransform, blend, allowAdditiveCoverage, nil)
}

func (c *metalCompositor) drawMasked(sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, allowAdditiveCoverage bool, alphaMask *image.NRGBA) error {
	var maskTexture unsafe.Pointer
	if alphaMask != nil && !alphaMask.Bounds().Empty() {
		cached, err := c.cachedSprite(alphaMask)
		if err != nil {
			return err
		}
		maskTexture = cached.texture
	}
	return c.drawTextures(nil, sprite, matrix, colorTransform, blend, allowAdditiveCoverage, maskTexture)
}

func (c *metalCompositor) drawTo(destination *metalSurface, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, allowAdditiveCoverage bool, alphaMask *metalSurface) error {
	var maskTexture unsafe.Pointer
	if alphaMask != nil {
		maskTexture = alphaMask.texture
	}
	return c.drawTextures(destination, sprite, matrix, colorTransform, blend, allowAdditiveCoverage, maskTexture)
}

// borrowedSurface exposes an uploaded image as a read-only surface. The
// texture remains owned by the compositor's sprite cache and must not be
// returned to the writable surface pool.
func (c *metalCompositor) borrowedSurface(sprite *image.NRGBA) (*metalSurface, error) {
	cached, err := c.cachedSprite(sprite)
	if err != nil {
		return nil, err
	}
	return &metalSurface{texture: cached.texture}, nil
}

func (c *metalCompositor) drawTextures(destination *metalSurface, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, allowAdditiveCoverage bool, maskTexture unsafe.Pointer) error {
	if err := c.requireActive(); err != nil {
		return err
	}
	mode, err := parseMetalBlendMode(blend)
	if err != nil {
		return err
	}
	if sprite == nil || sprite.Bounds().Empty() {
		return nil
	}
	requiredPixels := (sprite.Bounds().Dy()-1)*sprite.Stride + sprite.Bounds().Dx()*4
	if len(sprite.Pix) < requiredPixels || sprite.Stride < sprite.Bounds().Dx()*4 {
		return fmt.Errorf("%w: invalid sprite storage", errMetalCompositorUnsupported)
	}
	inv, err := matrix.Inverse()
	if err != nil {
		return nil
	}
	left, top, right, bottom := metalDrawBounds(matrix, sprite.Bounds().Dx(), sprite.Bounds().Dy(), c.width, c.height)
	if right <= left || bottom <= top {
		return nil
	}
	cached, err := c.cachedSprite(sprite)
	if err != nil {
		return err
	}
	_, integerTranslationOK := integerTranslation(matrix)
	nrgbaOverFastPath := maskTexture == nil && blend == "" && isIdentityColorTransform(colorTransform) && integerTranslationOK
	var errorMessage *C.char
	var destinationTexture unsafe.Pointer
	if destination != nil {
		destinationTexture = destination.texture
	}
	if C.sc_metal_compositor_draw(
		c.handle,
		destinationTexture,
		cached.texture,
		maskTexture,
		C.float(inv.A), C.float(inv.B), C.float(inv.C), C.float(inv.D), C.float(inv.Tx), C.float(inv.Ty),
		C.float(colorTransform.RAdd), C.float(colorTransform.GAdd), C.float(colorTransform.BAdd),
		C.float(colorTransform.AMul), C.float(colorTransform.RMul), C.float(colorTransform.GMul), C.float(colorTransform.BMul),
		C.uint32_t(mode), C.uint32_t(boolUint32(allowAdditiveCoverage)), C.uint32_t(cached.floor), C.uint32_t(boolUint32(nrgbaOverFastPath)),
		C.int(left), C.int(top), C.int(right), C.int(bottom),
		&errorMessage,
	) == 0 {
		return metalCompositorError(errorMessage)
	}
	runtime.KeepAlive(sprite)
	return nil
}

func (c *metalCompositor) acquireSurface() (*metalSurface, error) {
	if err := c.requireActive(); err != nil {
		return nil, err
	}
	var surface *metalSurface
	for _, candidate := range c.surfaces {
		if !candidate.inUse && !candidate.persistent {
			surface = candidate
			break
		}
	}
	if surface == nil {
		var errorMessage *C.char
		texture := C.sc_metal_compositor_surface_create(c.handle, &errorMessage)
		if texture == nil {
			return nil, metalCompositorError(errorMessage)
		}
		surface = &metalSurface{texture: texture}
		c.surfaces = append(c.surfaces, surface)
	}
	surface.inUse = true
	if err := c.clearSurface(surface, color.NRGBA{}); err != nil {
		surface.inUse = false
		return nil, err
	}
	return surface, nil
}

func (c *metalCompositor) acquirePersistentMask(key uint64) (*metalSurface, bool, error) {
	if err := c.requireActive(); err != nil {
		return nil, false, err
	}
	if surface, ok := c.masks[key]; ok {
		return surface, true, nil
	}
	// Do not retain one-off animated masks as full-canvas textures. A key must
	// repeat before it earns a persistent GPU surface.
	if _, ok := c.maskSeen[key]; !ok {
		c.maskSeen[key] = struct{}{}
		surface, err := c.acquireSurface()
		return surface, false, err
	}
	if len(c.masks) >= 32 {
		surface, err := c.acquireSurface()
		return surface, false, err
	}
	var errorMessage *C.char
	texture := C.sc_metal_compositor_surface_create(c.handle, &errorMessage)
	if texture == nil {
		return nil, false, metalCompositorError(errorMessage)
	}
	surface := &metalSurface{texture: texture, inUse: true, persistent: true}
	c.surfaces = append(c.surfaces, surface)
	c.masks[key] = surface
	if err := c.clearSurface(surface, color.NRGBA{}); err != nil {
		delete(c.masks, key)
		c.surfaces = c.surfaces[:len(c.surfaces)-1]
		C.sc_metal_texture_release(texture)
		return nil, false, err
	}
	return surface, false, nil
}

func (c *metalCompositor) clearSurface(surface *metalSurface, clearColor color.NRGBA) error {
	if err := c.requireActive(); err != nil {
		return err
	}
	if surface == nil || surface.texture == nil {
		return fmt.Errorf("%w: invalid Metal surface", errMetalCompositorUnsupported)
	}
	var errorMessage *C.char
	if C.sc_metal_compositor_surface_clear(
		c.handle,
		surface.texture,
		C.uint8_t(clearColor.R), C.uint8_t(clearColor.G), C.uint8_t(clearColor.B), C.uint8_t(clearColor.A),
		&errorMessage,
	) == 0 {
		return metalCompositorError(errorMessage)
	}
	return nil
}

func (c *metalCompositor) combineMasks(first, second *metalSurface) (*metalSurface, error) {
	if first == nil {
		return second, nil
	}
	if second == nil {
		return first, nil
	}
	output, err := c.acquireSurface()
	if err != nil {
		return nil, err
	}
	var errorMessage *C.char
	if C.sc_metal_compositor_mask_combine(
		c.handle,
		first.texture,
		second.texture,
		output.texture,
		&errorMessage,
	) == 0 {
		c.releaseSurface(output)
		return nil, metalCompositorError(errorMessage)
	}
	return output, nil
}

func (c *metalCompositor) releaseSurface(surface *metalSurface) {
	if surface != nil && !surface.persistent {
		surface.inUse = false
	}
}

func (c *metalCompositor) readback(reuse *image.NRGBA) (*image.NRGBA, error) {
	if err := c.requireActive(); err != nil {
		return nil, err
	}
	if reuse == nil || reuse.Bounds() != image.Rect(0, 0, c.width, c.height) || reuse.Stride != c.width*4 {
		reuse = image.NewNRGBA(image.Rect(0, 0, c.width, c.height))
	}
	var errorMessage *C.char
	result := C.sc_metal_compositor_readback(
		c.handle,
		(*C.uint8_t)(unsafe.Pointer(&reuse.Pix[0])),
		C.int(reuse.Stride),
		&errorMessage,
	)
	c.active = false
	for _, surface := range c.surfaces {
		surface.inUse = false
	}
	if result == 0 {
		return nil, metalCompositorError(errorMessage)
	}
	runtime.KeepAlive(reuse)
	return reuse, nil
}

func (c *metalCompositor) cachedSpriteCount() int {
	if c == nil {
		return 0
	}
	return len(c.textures)
}

func (c *metalCompositor) close() error {
	if c == nil || c.closed {
		return nil
	}
	for key, cached := range c.textures {
		C.sc_metal_texture_release(cached.texture)
		delete(c.textures, key)
	}
	for _, surface := range c.surfaces {
		C.sc_metal_texture_release(surface.texture)
		surface.texture = nil
		surface.inUse = false
	}
	c.surfaces = nil
	c.masks = nil
	c.maskSeen = nil
	C.sc_metal_compositor_destroy(c.handle)
	c.handle = nil
	c.active = false
	c.closed = true
	return nil
}

func (c *metalCompositor) cachedMaskCount() int {
	if c == nil {
		return 0
	}
	return len(c.masks)
}

func (c *metalCompositor) cachedSprite(sprite *image.NRGBA) (metalCachedSprite, error) {
	// Exporter sprites are immutable. Retaining the image identity together
	// with its backing storage and layout prevents aliases or replaced Pix
	// buffers from reusing the wrong GPU texture without hashing every frame.
	key := metalSpriteKey{
		sprite: sprite,
		pixels: unsafe.Pointer(&sprite.Pix[0]),
		rect:   sprite.Rect,
		stride: sprite.Stride,
		length: len(sprite.Pix),
	}
	if cached, ok := c.textures[key]; ok {
		return cached, nil
	}
	width, height := sprite.Bounds().Dx(), sprite.Bounds().Dy()
	var errorMessage *C.char
	texture := C.sc_metal_compositor_upload(
		c.handle,
		(*C.uint8_t)(unsafe.Pointer(&sprite.Pix[0])),
		C.int(width),
		C.int(height),
		C.int(sprite.Stride),
		&errorMessage,
	)
	runtime.KeepAlive(sprite)
	if texture == nil {
		return metalCachedSprite{}, metalCompositorError(errorMessage)
	}
	cached := metalCachedSprite{
		texture: texture,
		floor:   borderLuminanceFloor(sprite),
	}
	c.textures[key] = cached
	return cached, nil
}

func (c *metalCompositor) usable() error {
	if c == nil || c.handle == nil || c.closed {
		return fmt.Errorf("%w: compositor is closed", errMetalCompositorUnavailable)
	}
	return nil
}

func (c *metalCompositor) requireActive() error {
	if err := c.usable(); err != nil {
		return err
	}
	if !c.active {
		return fmt.Errorf("%w: beginFrame must be called first", errMetalCompositorUnsupported)
	}
	return nil
}

func metalDrawBounds(matrix sc.Matrix, width, height, canvasWidth, canvasHeight int) (left, top, right, bottom int) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, corner := range [][2]float64{{0, 0}, {float64(width), 0}, {float64(width), float64(height)}, {0, float64(height)}} {
		x, y := matrix.Apply(corner[0], corner[1])
		minX = math.Min(minX, x)
		minY = math.Min(minY, y)
		maxX = math.Max(maxX, x)
		maxY = math.Max(maxY, y)
	}
	left = maxInt(int(math.Floor(minX)), 0)
	top = maxInt(int(math.Floor(minY)), 0)
	right = minInt(int(math.Ceil(maxX)), canvasWidth)
	bottom = minInt(int(math.Ceil(maxY)), canvasHeight)
	return
}

func metalCompositorError(message *C.char) error {
	if message == nil {
		return errMetalCompositorUnavailable
	}
	defer C.free(unsafe.Pointer(message))
	text := C.GoString(message)
	if text == "no Metal device is available" {
		return fmt.Errorf("%w: %s", errMetalCompositorUnavailable, text)
	}
	return errors.New(text)
}

func boolUint32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}
