// Package hevc provides a streaming native macOS HEVC movie encoder.
package hevc

import (
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrUnavailable identifies hosts where the native encoder cannot be used.
var ErrUnavailable = errors.New("hardware HEVC encoder unavailable")

// Options controls HEVC encoding.
type Options struct {
	// Quality controls video quality from 0 to 100.
	Quality int
	// RequireHardware prevents VideoToolbox from falling back to software.
	RequireHardware bool
}

// Result describes a completed movie.
type Result struct {
	Frames                    int
	DurationMS                int64
	HardwareAccelerated       bool
	HardwareAccelerationKnown bool
}

// Encoder accepts full-canvas NRGBA frames in display order. The destination
// is atomically replaced only after Close successfully finalizes the movie.
type Encoder struct {
	mu       sync.Mutex
	path     string
	tempPath string
	width    int
	height   int
	opts     Options
	native   nativeEncoder
	frames   int
	duration int64
	done     bool
	result   Result
	err      error
}

// New starts a streaming HEVC QuickTime encoder.
func New(path string, width, height int, opts Options) (*Encoder, error) {
	if err := validate(path, width, height, opts); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("prepare HEVC output: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.mov")
	if err != nil {
		return nil, fmt.Errorf("prepare HEVC output: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("prepare HEVC output: %w", err)
	}
	// AVAssetWriter refuses to open an existing output URL.
	if err := os.Remove(tempPath); err != nil {
		return nil, fmt.Errorf("prepare HEVC output: %w", err)
	}
	native, err := newNativeEncoder(tempPath, width, height, opts)
	if err != nil {
		_ = os.Remove(tempPath)
		return nil, err
	}
	return &Encoder{path: path, tempPath: tempPath, width: width, height: height, opts: opts, native: native}, nil
}

// AddFrame appends one frame with an exact positive millisecond duration.
func (e *Encoder) AddFrame(frame *image.NRGBA, durationMS int) error {
	if e == nil {
		return fmt.Errorf("HEVC encoder is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return fmt.Errorf("HEVC encoder is closed")
	}
	if frame == nil {
		return fmt.Errorf("HEVC frame is nil")
	}
	if w, h := frame.Bounds().Dx(), frame.Bounds().Dy(); w != e.width || h != e.height {
		return fmt.Errorf("HEVC frame is %dx%d, want %dx%d", w, h, e.width, e.height)
	}
	if frame.Stride < e.width*4 || len(frame.Pix) < (e.height-1)*frame.Stride+e.width*4 {
		return fmt.Errorf("HEVC frame has invalid NRGBA storage")
	}
	if durationMS <= 0 {
		return fmt.Errorf("HEVC frame duration must be positive, got %dms", durationMS)
	}
	if int64(durationMS) > math.MaxInt64-e.duration {
		return fmt.Errorf("HEVC movie duration overflows milliseconds")
	}
	if err := e.native.addFrame(frame, e.duration); err != nil {
		return fmt.Errorf("append HEVC frame %d: %w", e.frames, err)
	}
	e.frames++
	e.duration += int64(durationMS)
	return nil
}

// Close finalizes and atomically publishes the movie. Repeated calls return
// the result of the first call.
func (e *Encoder) Close() (Result, error) {
	if e == nil {
		return Result{}, fmt.Errorf("HEVC encoder is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return e.result, e.err
	}
	e.done = true
	if e.frames == 0 {
		e.native.abort()
		_ = os.Remove(e.tempPath)
		e.err = fmt.Errorf("finish HEVC movie: no frames")
		return Result{}, e.err
	}
	if err := e.native.finish(e.duration); err != nil {
		e.native.abort()
		_ = os.Remove(e.tempPath)
		e.err = fmt.Errorf("finish HEVC movie: %w", err)
		return Result{}, e.err
	}
	if err := os.Chmod(e.tempPath, 0o644); err != nil {
		_ = os.Remove(e.tempPath)
		e.err = fmt.Errorf("publish HEVC movie: %w", err)
		return Result{}, e.err
	}
	if err := os.Rename(e.tempPath, e.path); err != nil {
		_ = os.Remove(e.tempPath)
		e.err = fmt.Errorf("publish HEVC movie: %w", err)
		return Result{}, e.err
	}
	e.result = Result{
		Frames:                    e.frames,
		DurationMS:                e.duration,
		HardwareAccelerated:       e.opts.RequireHardware,
		HardwareAccelerationKnown: e.opts.RequireHardware,
	}
	return e.result, nil
}

// Abort cancels encoding and removes the unpublished temporary movie.
func (e *Encoder) Abort() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return
	}
	e.done = true
	e.native.abort()
	_ = os.Remove(e.tempPath)
	e.err = fmt.Errorf("HEVC encoding aborted")
}

func validate(path string, width, height int, opts Options) error {
	if path == "" {
		return fmt.Errorf("HEVC output path is empty")
	}
	if !strings.EqualFold(filepath.Ext(path), ".mov") {
		return fmt.Errorf("HEVC output path must end in .mov: %q", path)
	}
	if width <= 0 || height <= 0 || width > math.MaxInt32 || height > math.MaxInt32 {
		return fmt.Errorf("invalid HEVC canvas %dx%d", width, height)
	}
	if width%2 != 0 || height%2 != 0 {
		return fmt.Errorf("HEVC canvas must have even dimensions, got %dx%d", width, height)
	}
	if opts.Quality < 0 || opts.Quality > 100 {
		return fmt.Errorf("HEVC quality must be between 0 and 100, got %d", opts.Quality)
	}
	return nil
}

type nativeEncoder interface {
	addFrame(*image.NRGBA, int64) error
	finish(int64) error
	abort()
}
