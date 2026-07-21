package render

import (
	"errors"
	"fmt"
	"image"
	"io"

	webpencode "sc2fla/internal/encode/webp"
)

var currentWebPEncoder = webpencode.NewInProcessEncoder()

func writeStillWebP(w io.Writer, img image.Image, opts ExportOptions) error {
	return currentWebPEncoder.EncodeStill(w, img, webpOptions(opts))
}

func writeAnimatedWebP(w io.Writer, frames []renderedFrame, opts ExportOptions) error {
	return encodeAnimatedWebP(w, len(frames), opts, func(index int) (image.Image, int, error) {
		return frames[index].Image, frames[index].DelayMS, nil
	})
}

func encodeAnimatedWebP(w io.Writer, frameCount int, opts ExportOptions, loadFrame func(int) (image.Image, int, error)) error {
	if frameCount == 0 {
		return fmt.Errorf("WebP requires at least one frame")
	}

	first, firstDelayMS, err := loadFrame(0)
	if err != nil {
		return err
	}
	if first == nil {
		return fmt.Errorf("WebP frame 0 is nil")
	}
	bounds := first.Bounds()
	animation, err := currentWebPEncoder.NewAnimation(w, bounds.Dx(), bounds.Dy(), webpOptions(opts))
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = animation.Close()
		}
	}()

	if err := addWebPFrame(animation, first, firstDelayMS, bounds, 0); err != nil {
		return errors.Join(err, animation.Close())
	}
	for index := 1; index < frameCount; index++ {
		img, delayMS, err := loadFrame(index)
		if err != nil {
			return errors.Join(err, animation.Close())
		}
		if err := addWebPFrame(animation, img, delayMS, bounds, index); err != nil {
			return errors.Join(err, animation.Close())
		}
	}
	if err := animation.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	return nil
}

func addWebPFrame(animation webpencode.AnimationEncoder, img image.Image, delayMS int, bounds image.Rectangle, index int) error {
	if img == nil {
		return fmt.Errorf("WebP frame %d is nil", index)
	}
	if img.Bounds() != bounds {
		return fmt.Errorf("WebP frame %d bounds = %v, want %v", index, img.Bounds(), bounds)
	}
	if delayMS <= 0 {
		delayMS = 10
	}
	return animation.AddFrame(img, delayMS)
}

func webpOptions(opts ExportOptions) webpencode.Options {
	return webpencode.Options{
		Quality: opts.WebPQuality,
		Method:  opts.WebPMethod,
	}
}
