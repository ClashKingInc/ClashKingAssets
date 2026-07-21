// Package webp provides the extractor's in-process WebP encoder seam.
package webp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"
	"runtime"
	"sync"

	webpcodec "github.com/deepteams/webp"
	"github.com/deepteams/webp/animation"
	"github.com/deepteams/webp/mux"
)

// Options controls lossy WebP encoding.
type Options struct {
	Quality int
	Method  int
}

// Encoder is the format-encoder seam used by the renderer.
type Encoder interface {
	EncodeStill(io.Writer, image.Image, Options) error
	NewAnimation(io.Writer, int, int, Options) (AnimationEncoder, error)
}

// AnimationEncoder accepts full-canvas frames in display order.
type AnimationEncoder interface {
	AddFrame(image.Image, int) error
	Close() error
	Abort()
}

// InProcessEncoder encodes WebP without temporary files or installed tools.
type InProcessEncoder struct{}

// NewInProcessEncoder creates a self-contained pure-Go WebP encoder.
func NewInProcessEncoder() Encoder {
	return InProcessEncoder{}
}

func (InProcessEncoder) EncodeStill(w io.Writer, img image.Image, opts Options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	if err := webpcodec.Encode(w, img, codecOptions(opts)); err != nil {
		return fmt.Errorf("encode still WebP: %w", err)
	}
	return nil
}

func (InProcessEncoder) NewAnimation(w io.Writer, width, height int, opts Options) (AnimationEncoder, error) {
	if w == nil {
		return nil, fmt.Errorf("WebP animation writer is nil")
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 || width > 16383 || height > 16383 {
		return nil, fmt.Errorf("invalid WebP animation canvas %dx%d", width, height)
	}
	muxer := mux.NewMuxer()
	muxer.SetCanvasSize(width, height)
	muxer.SetLoopCount(0)
	workers := animationWorkerCount(width, height, runtime.GOMAXPROCS(0))
	encoder := &inProcessAnimationEncoder{
		writer: w,
		muxer:  muxer,
		opts:   opts,
		width:  width,
		height: height,
		jobs:   make(chan *animationFrame, workers),
	}
	encoder.workers.Add(workers)
	for range workers {
		go encoder.encodeFrames()
	}
	return encoder, nil
}

func animationWorkerCount(width, height, maxProcs int) int {
	const (
		maximumWorkers      = 8
		encoderMemoryBudget = 1 << 30
		// The pure-Go lossy encoder peaks around 24 source-frame bytes per
		// pixel while its prediction and transform workspaces are live.
		estimatedBytesPerPixel = 4 * 24
	)
	estimatedWorkerBytes := max(int64(1), int64(width)*int64(height)*estimatedBytesPerPixel)
	memoryLimitedWorkers := max(1, int(int64(encoderMemoryBudget)/estimatedWorkerBytes))
	return min(maximumWorkers, max(1, maxProcs), memoryLimitedWorkers)
}

type animationFrame struct {
	bounds     image.Rectangle
	durationMS int
	image      *image.NRGBA
	bitstream  []byte
	err        error
}

type inProcessAnimationEncoder struct {
	writer   io.Writer
	muxer    *mux.Muxer
	opts     Options
	width    int
	height   int
	previous *image.NRGBA
	frames   []*animationFrame
	jobs     chan *animationFrame
	workers  sync.WaitGroup
	closed   bool
	once     sync.Once
	err      error
}

func (e *inProcessAnimationEncoder) AddFrame(img image.Image, durationMS int) error {
	if e.closed {
		return fmt.Errorf("WebP animation encoder is closed")
	}
	if durationMS <= 0 {
		return fmt.Errorf("WebP frame duration must be positive, got %dms", durationMS)
	}
	canvas, err := animationCanvas(img, e.width, e.height)
	if err != nil {
		return err
	}
	changed := canvas.Bounds()
	if e.previous != nil {
		changed = changedAnimationBounds(e.previous, canvas)
		if changed.Empty() {
			e.frames[len(e.frames)-1].durationMS += durationMS
			return nil
		}
		changed = paddedAnimationBounds(changed, canvas.Bounds(), 8)
	}
	frame := &animationFrame{
		bounds:     changed,
		durationMS: durationMS,
		image:      cropNRGBA(canvas, changed),
	}
	e.frames = append(e.frames, frame)
	e.jobs <- frame
	if e.previous == nil {
		e.previous = cloneNRGBA(canvas)
	} else {
		copyNRGBARect(e.previous, canvas, changed)
	}
	return nil
}

func (e *inProcessAnimationEncoder) Close() error {
	e.once.Do(func() {
		e.closed = true
		close(e.jobs)
		e.workers.Wait()
		if len(e.frames) == 0 {
			e.err = fmt.Errorf("no WebP animation frames")
			return
		}
		for index, frame := range e.frames {
			if frame.err != nil {
				e.err = fmt.Errorf("encode WebP animation frame %d: %w", index, frame.err)
				return
			}
			if err := e.muxer.AddFrame(frame.bitstream, &mux.FrameOptions{
				Duration:    frame.durationMS,
				OffsetX:     frame.bounds.Min.X,
				OffsetY:     frame.bounds.Min.Y,
				BlendMode:   mux.BlendNone,
				DisposeMode: mux.DisposeNone,
			}); err != nil {
				e.err = fmt.Errorf("add WebP animation frame %d: %w", index, err)
				return
			}
		}
		e.err = e.muxer.Assemble(e.writer)
	})
	if e.err != nil {
		return fmt.Errorf("finish WebP animation: %w", e.err)
	}
	return nil
}

func (e *inProcessAnimationEncoder) Abort() {
	e.once.Do(func() {
		e.closed = true
		close(e.jobs)
		e.workers.Wait()
		e.err = fmt.Errorf("WebP animation encoding aborted")
	})
}

func (e *inProcessAnimationEncoder) encodeFrames() {
	defer e.workers.Done()
	for frame := range e.jobs {
		frame.bitstream, frame.err = encodeRawFrame(frame.image, false, e.opts.Quality, e.opts.Method)
		frame.image = nil
	}
}

func animationCanvas(img image.Image, width, height int) (*image.NRGBA, error) {
	if img == nil {
		return nil, fmt.Errorf("WebP animation frame is nil")
	}
	if direct, ok := img.(*image.NRGBA); ok && direct.Bounds() == image.Rect(0, 0, width, height) {
		return direct, nil
	}
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		return nil, fmt.Errorf("WebP animation frame is %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), width, height)
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), img, img.Bounds().Min, draw.Src)
	return canvas, nil
}

func changedAnimationBounds(previous, current *image.NRGBA) image.Rectangle {
	width, height := current.Bounds().Dx(), current.Bounds().Dy()
	minX, minY := width, height
	maxX, maxY := 0, 0
	for y := 0; y < height; y++ {
		previousRow := previous.Pix[y*previous.Stride : y*previous.Stride+width*4]
		currentRow := current.Pix[y*current.Stride : y*current.Stride+width*4]
		for x := 0; x < width; x++ {
			offset := x * 4
			if previousRow[offset] == currentRow[offset] &&
				previousRow[offset+1] == currentRow[offset+1] &&
				previousRow[offset+2] == currentRow[offset+2] &&
				previousRow[offset+3] == currentRow[offset+3] {
				continue
			}
			minX = min(minX, x)
			minY = min(minY, y)
			maxX = max(maxX, x+1)
			maxY = max(maxY, y+1)
		}
	}
	if minX == width {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func paddedAnimationBounds(changed, canvas image.Rectangle, padding int) image.Rectangle {
	changed.Min.X = max(canvas.Min.X, changed.Min.X-padding)
	changed.Min.Y = max(canvas.Min.Y, changed.Min.Y-padding)
	changed.Max.X = min(canvas.Max.X, changed.Max.X+padding)
	changed.Max.Y = min(canvas.Max.Y, changed.Max.Y+padding)
	// Animated WebP stores offsets in half-pixel units, so both offsets must
	// be even. Expanding left/up preserves every changed pixel.
	changed.Min.X &^= 1
	changed.Min.Y &^= 1
	return changed
}

func cropNRGBA(source *image.NRGBA, bounds image.Rectangle) *image.NRGBA {
	cropped := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		sourceStart := (bounds.Min.Y+y)*source.Stride + bounds.Min.X*4
		copy(cropped.Pix[y*cropped.Stride:y*cropped.Stride+bounds.Dx()*4], source.Pix[sourceStart:sourceStart+bounds.Dx()*4])
	}
	return cropped
}

func cloneNRGBA(source *image.NRGBA) *image.NRGBA {
	clone := image.NewNRGBA(source.Bounds())
	copy(clone.Pix, source.Pix)
	return clone
}

func copyNRGBARect(destination, source *image.NRGBA, bounds image.Rectangle) {
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		start := bounds.Min.X * 4
		end := bounds.Max.X * 4
		copy(destination.Pix[y*destination.Stride+start:y*destination.Stride+end], source.Pix[y*source.Stride+start:y*source.Stride+end])
	}
}

func validateOptions(opts Options) error {
	if opts.Quality < 0 || opts.Quality > 100 {
		return fmt.Errorf("WebP quality must be between 0 and 100, got %d", opts.Quality)
	}
	if opts.Method < 0 || opts.Method > 6 {
		return fmt.Errorf("WebP method must be between 0 and 6, got %d", opts.Method)
	}
	return nil
}

func codecOptions(opts Options) *webpcodec.EncoderOptions {
	config := webpcodec.DefaultOptions()
	config.Quality = float32(opts.Quality)
	config.Method = opts.Method
	return config
}

func encodeRawFrame(img image.Image, lossless bool, quality, method int) ([]byte, error) {
	var encoded bytes.Buffer
	config := codecOptions(Options{Quality: quality, Method: method})
	config.Lossless = lossless
	if err := webpcodec.Encode(&encoded, img, config); err != nil {
		return nil, err
	}

	parsed, err := animation.DecodeBytes(encoded.Bytes())
	if err != nil {
		return nil, fmt.Errorf("extract encoded WebP frame: %w", err)
	}
	if len(parsed.Frames) != 1 {
		return nil, fmt.Errorf("encoded WebP frame count = %d, want 1", len(parsed.Frames))
	}
	frame := parsed.Frames[0]
	if len(frame.AlphaData) == 0 {
		return frame.BitstreamData, nil
	}

	// The muxer accepts ALPH-header + alpha payload + VP8 bitstream for lossy
	// frames with transparency.
	raw := make([]byte, 8+len(frame.AlphaData)+(len(frame.AlphaData)&1)+len(frame.BitstreamData))
	binary.LittleEndian.PutUint32(raw[0:4], mux.FourCCALPH)
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(frame.AlphaData)))
	copy(raw[8:], frame.AlphaData)
	copy(raw[8+len(frame.AlphaData)+(len(frame.AlphaData)&1):], frame.BitstreamData)
	return raw, nil
}
