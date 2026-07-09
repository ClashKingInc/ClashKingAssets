package render

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func writeStillWebP(w io.Writer, img image.Image) error {
	tempDir, err := os.MkdirTemp("", "sc-export-webp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	inputPath := filepath.Join(tempDir, "i.png")
	inputFile, err := os.Create(inputPath)
	if err != nil {
		return err
	}
	if err := png.Encode(inputFile, img); err != nil {
		inputFile.Close()
		return err
	}
	if err := inputFile.Close(); err != nil {
		return err
	}

	outputPath := filepath.Join(tempDir, "o.webp")
	cwebpPath, err := lookupCWebPTool()
	if err != nil {
		return err
	}
	if err := runCommand(cwebpPath, "-quiet", "-q", animatedWebPQuality(), "-m", animatedWebPMethod(), inputPath, "-o", outputPath); err != nil {
		return err
	}

	return copyWebPOutput(w, outputPath)
}

func writeAnimatedWebP(w io.Writer, frames []renderedFrame) error {
	if len(frames) == 0 {
		return fmt.Errorf("webp requires at least one frame")
	}

	tempDir, err := os.MkdirTemp("", "sc-export-webp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	pngEncoder := png.Encoder{CompressionLevel: png.BestSpeed}
	framePaths := make([]string, 0, len(frames))
	for i, frame := range frames {
		frameName := fmt.Sprintf("%x.png", i)
		pngPath := filepath.Join(tempDir, frameName)
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
		framePaths = append(framePaths, frameName)
	}

	outputName := "o.webp"
	img2webpPath, err := lookupWebPTools()
	if err != nil {
		return err
	}
	if err := runImg2WebPInDir(tempDir, img2webpPath, framePaths, frames, outputName); err != nil {
		return err
	}

	return copyWebPOutput(w, filepath.Join(tempDir, outputName))
}

func writeAnimatedWebPFromFiles(w io.Writer, tempDir string, framePaths []string, frames []renderedFrame) error {
	if len(framePaths) == 0 {
		return fmt.Errorf("webp requires at least one frame")
	}
	img2webpPath, err := lookupWebPTools()
	if err != nil {
		return err
	}
	outputName := "o.webp"
	if err := runImg2WebPInDir(tempDir, img2webpPath, framePaths, frames, outputName); err != nil {
		return err
	}
	return copyWebPOutput(w, filepath.Join(tempDir, outputName))
}

func copyWebPOutput(w io.Writer, outputPath string) error {
	outputFile, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	_, err = io.Copy(w, outputFile)
	return err
}

func lookupCWebPTool() (string, error) {
	cwebpPath, err := exec.LookPath("cwebp")
	if err != nil {
		return "", fmt.Errorf("cwebp not found in PATH")
	}
	return cwebpPath, nil
}

func lookupWebPTools() (string, error) {
	img2webpPath, err := exec.LookPath("img2webp")
	if err != nil {
		return "", fmt.Errorf("img2webp not found in PATH")
	}
	return img2webpPath, nil
}

func ensureWebPToolsAvailable() error {
	if _, err := lookupWebPTools(); err != nil {
		return err
	}
	_, err := lookupCWebPTool()
	return err
}

func animatedWebPQuality() string {
	return getEnvOrDefault("SC_ANIM_WEBP_QUALITY", "88")
}

func animatedWebPMethod() string {
	return getEnvOrDefault("SC_ANIM_WEBP_METHOD", "0")
}

func getEnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func buildImg2WebPArgs(framePaths []string, frames []renderedFrame, outputPath string) []string {
	if len(framePaths) == 0 {
		return []string{"-o", outputPath}
	}
	args := make([]string, 0, len(framePaths)*7+4)
	args = append(args, "-loop", "0", "-lossy", "-q", animatedWebPQuality(), "-m", animatedWebPMethod())
	for i, framePath := range framePaths {
		delayMS := 10
		if i < len(frames) && frames[i].DelayCS > 0 {
			delayMS = frames[i].DelayCS * 10
		}
		args = append(args, "-d", strconv.Itoa(delayMS), framePath)
	}
	args = append(args, "-o", outputPath)
	return args
}

func runImg2WebPInDir(dir, name string, framePaths []string, frames []renderedFrame, outputPath string) error {
	return runCommandInDir(dir, name, buildImg2WebPArgs(framePaths, frames, outputPath)...)
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, string(output))
	}
	return nil
}

func runCommandInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, string(output))
	}
	return nil
}
