package render

import (
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func writeAnimatedWebP(w io.Writer, frames []renderedFrame) error {
	if len(frames) == 0 {
		return fmt.Errorf("webp requires at least one frame")
	}

	img2webpPath, webpmuxPath, err := lookupWebPTools()
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "sc-export-webp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	pngEncoder := png.Encoder{CompressionLevel: png.BestSpeed}
	framePaths := make([]string, 0, len(frames))
	for i, frame := range frames {
		pngPath := filepath.Join(tempDir, fmt.Sprintf("frame_%04d.png", i))
		pngFile, err := os.Create(pngPath)
		if err != nil {
			return err
		}
		if err := pngEncoder.Encode(pngFile, frame.Image); err != nil {
			pngFile.Close()
			return err
		}
		if err := pngFile.Close(); err != nil {
			return err
		}
		framePaths = append(framePaths, pngPath)
	}

	rawPath := filepath.Join(tempDir, "animation_raw.webp")
	if err := runCommand(img2webpPath, buildImg2WebPArgs(framePaths, frames, rawPath)...); err != nil {
		return err
	}

	timedPath := filepath.Join(tempDir, "animation_timed.webp")
	if err := runCommand(webpmuxPath, buildWebPMuxDurationArgs(frames, rawPath, timedPath)...); err != nil {
		return err
	}

	outputPath := filepath.Join(tempDir, "animation.webp")
	if err := runCommand(webpmuxPath, buildWebPMuxColorArgs(timedPath, outputPath)...); err != nil {
		return err
	}

	outputFile, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	_, err = io.Copy(w, outputFile)
	return err
}

func lookupWebPTools() (string, string, error) {
	img2webpPath, err := exec.LookPath("img2webp")
	if err != nil {
		return "", "", fmt.Errorf("img2webp not found in PATH")
	}
	webpmuxPath, err := exec.LookPath("webpmux")
	if err != nil {
		return "", "", fmt.Errorf("webpmux not found in PATH")
	}
	return img2webpPath, webpmuxPath, nil
}

func buildImg2WebPArgs(framePaths []string, frames []renderedFrame, outputPath string) []string {
	if len(framePaths) == 0 {
		return []string{"-o", outputPath}
	}
	args := make([]string, 0, len(framePaths)*5+4)
	args = append(args, "-loop", "0", "-lossless", "-m", "0", "-exact", framePaths[0])
	for i := 1; i < len(framePaths); i++ {
		delayMS := 10
		if i < len(frames) && frames[i].DelayCS > 0 {
			delayMS = frames[i].DelayCS * 10
		}
		args = append(args, "-d", fmt.Sprintf("%d", delayMS), "-lossless", "-m", "0", "-exact", framePaths[i])
	}
	args = append(args, "-o", outputPath)
	return args
}

func buildWebPMuxDurationArgs(frames []renderedFrame, inputPath, outputPath string) []string {
	args := make([]string, 0, len(frames)*2+3)
	for i := range frames {
		delayMS := 10
		if frames[i].DelayCS > 0 {
			delayMS = frames[i].DelayCS * 10
		}
		frameIndex := i + 1
		args = append(args, "-duration", fmt.Sprintf("%d,%d,%d", delayMS, frameIndex, frameIndex))
	}
	args = append(args, inputPath, "-o", outputPath)
	return args
}

func buildWebPMuxColorArgs(inputPath, outputPath string) []string {
	args := []string{"-set", "bgcolor", "0,0,0,0", inputPath, "-o", outputPath}
	return args
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, string(output))
	}
	return nil
}
