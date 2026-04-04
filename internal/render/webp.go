package render

import (
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

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
	argsPath := filepath.Join(tempDir, "args.txt")
	if err := os.WriteFile(argsPath, []byte(buildImg2WebPArgsFile(framePaths, frames, outputName)), 0o600); err != nil {
		return err
	}
	if err := runCommandInDir(tempDir, img2webpPath, filepath.Base(argsPath)); err != nil {
		return err
	}

	return copyAnimatedWebPOutput(w, filepath.Join(tempDir, outputName))
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
	argsPath := filepath.Join(tempDir, "args.txt")
	if err := os.WriteFile(argsPath, []byte(buildImg2WebPArgsFile(framePaths, frames, outputName)), 0o600); err != nil {
		return err
	}
	if err := runCommandInDir(tempDir, img2webpPath, filepath.Base(argsPath)); err != nil {
		return err
	}
	return copyAnimatedWebPOutput(w, filepath.Join(tempDir, outputName))
}

func copyAnimatedWebPOutput(w io.Writer, outputPath string) error {
	outputFile, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	_, err = io.Copy(w, outputFile)
	return err
}

func lookupWebPTools() (string, error) {
	img2webpPath, err := exec.LookPath("img2webp")
	if err != nil {
		return "", fmt.Errorf("img2webp not found in PATH")
	}
	return img2webpPath, nil
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

func buildImg2WebPArgsFile(framePaths []string, frames []renderedFrame, outputPath string) string {
	args := buildImg2WebPArgs(framePaths, frames, outputPath)
	return strings.Join(args, "\n")
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
