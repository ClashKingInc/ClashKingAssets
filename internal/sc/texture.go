package sc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var (
	pixelFormats = []string{
		"GL_RGBA", "GL_RGBA", "GL_RGBA", "GL_RGBA", "GL_RGB", "GL_RGBA",
		"GL_LUMINANCE_ALPHA", "GL_RGBA", "GL_RGBA", "GL_RGBA", "GL_LUMINANCE",
	}
	pixelInternalFormats = []string{
		"GL_RGBA8", "GL_RGBA8", "GL_RGBA4", "GL_RGB5_A1", "GL_RGB565",
		"GL_RGBA8", "GL_LUMINANCE8_ALPHA8", "GL_RGBA8", "GL_RGBA8",
		"GL_RGBA4", "GL_LUMINANCE8",
	}
	pixelTypes = []string{
		"GL_UNSIGNED_BYTE", "GL_UNSIGNED_BYTE", "GL_UNSIGNED_SHORT_4_4_4_4",
		"GL_UNSIGNED_SHORT_5_5_5_1", "GL_UNSIGNED_SHORT_5_6_5",
		"GL_UNSIGNED_BYTE", "GL_UNSIGNED_BYTE", "GL_UNSIGNED_BYTE",
		"GL_UNSIGNED_BYTE", "GL_UNSIGNED_SHORT_4_4_4_4", "GL_UNSIGNED_BYTE",
	}
)

func loadTexture(reader *Reader, tag uint8, hasExternalTexture bool, swfPath string) (*Texture, error) {
	tex := &Texture{
		Channels:    4,
		MagFilter:   "GL_LINEAR",
		MinFilter:   "GL_NEAREST",
		Linear:      tag != 27 && tag != 28 && tag != 29,
		Downscaling: tag == 1 || tag == 16 || tag == 28 || tag == 29,
	}

	var (
		ktxSize             uint32
		externalTexturePath string
		err                 error
	)
	if tag == 45 {
		ktxSize, err = reader.ReadU32()
		if err != nil {
			return nil, err
		}
	}
	if tag == 47 {
		externalTexturePath, err = reader.ReadASCII()
		if err != nil {
			return nil, err
		}
	}

	pixelTypeIndex, err := reader.ReadU8()
	if err != nil {
		return nil, err
	}
	if int(pixelTypeIndex) >= len(pixelFormats) {
		return nil, fmt.Errorf("unsupported texture pixel type index %d", pixelTypeIndex)
	}
	tex.PixelFormat = pixelFormats[pixelTypeIndex]
	tex.PixelInternalFormat = pixelInternalFormats[pixelTypeIndex]
	tex.PixelType = pixelTypes[pixelTypeIndex]

	switch tag {
	case 16, 19, 29:
		tex.MinFilter = "GL_LINEAR_MIPMAP_NEAREST"
	case 34:
		tex.MagFilter = "GL_NEAREST"
		tex.MinFilter = "GL_NEAREST"
	}

	width, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	height, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	tex.Width = int(width)
	tex.Height = int(height)

	switch {
	case ktxSize > 0:
		payload, err := reader.Read(int(ktxSize))
		if err != nil {
			return nil, err
		}
		img, err := decodeKTXBytes(payload)
		if err != nil {
			tex.LoadError = err.Error()
			return tex, nil
		}
		tex.Image = img
		return tex, nil
	case externalTexturePath != "":
		img, err := decodeExternalTexture(swfPath, externalTexturePath)
		if err != nil {
			tex.LoadError = err.Error()
			return tex, nil
		}
		tex.Image = img
		return tex, nil
	case !hasExternalTexture:
		img, err := decodeRawTexture(reader, tex)
		if err != nil {
			tex.LoadError = err.Error()
			return tex, nil
		}
		tex.Image = img
		return tex, nil
	default:
		return tex, nil
	}
}

func decodeRawTexture(reader *Reader, tex *Texture) (*image.NRGBA, error) {
	img := image.NewNRGBA(image.Rect(0, 0, tex.Width, tex.Height))
	if tex.Linear {
		for y := 0; y < tex.Height; y++ {
			for x := 0; x < tex.Width; x++ {
				px, err := readRawPixel(reader, tex.PixelInternalFormat)
				if err != nil {
					return nil, err
				}
				img.SetNRGBA(x, y, px)
			}
		}
		return img, nil
	}

	block := 32
	for yb := 0; yb <= tex.Height/block; yb++ {
		for xb := 0; xb <= tex.Width/block; xb++ {
			for y := 0; y < block; y++ {
				py := yb*block + y
				if py >= tex.Height {
					break
				}
				for x := 0; x < block; x++ {
					px := xb*block + x
					if px >= tex.Width {
						break
					}
					c, err := readRawPixel(reader, tex.PixelInternalFormat)
					if err != nil {
						return nil, err
					}
					img.SetNRGBA(px, py, c)
				}
			}
		}
	}
	return img, nil
}

func readRawPixel(reader *Reader, format string) (c color.NRGBA, err error) {
	switch format {
	case "GL_RGBA8":
		var r, g, b, a uint8
		if r, err = reader.ReadU8(); err != nil {
			return
		}
		if g, err = reader.ReadU8(); err != nil {
			return
		}
		if b, err = reader.ReadU8(); err != nil {
			return
		}
		if a, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{R: r, G: g, B: b, A: a}
	case "GL_RGBA4":
		var p uint16
		if p, err = reader.ReadU16(); err != nil {
			return
		}
		c = color.NRGBA{
			R: uint8(((p >> 12) & 15) << 4),
			G: uint8(((p >> 8) & 15) << 4),
			B: uint8(((p >> 4) & 15) << 4),
			A: uint8((p & 15) << 4),
		}
	case "GL_RGB5_A1":
		var p uint16
		if p, err = reader.ReadU16(); err != nil {
			return
		}
		c = color.NRGBA{
			R: uint8(((p >> 11) & 31) << 3),
			G: uint8(((p >> 6) & 31) << 3),
			B: uint8(((p >> 1) & 31) << 3),
			A: uint8((p & 1) * 255),
		}
	case "GL_RGB565":
		var p uint16
		if p, err = reader.ReadU16(); err != nil {
			return
		}
		c = color.NRGBA{
			R: uint8(((p >> 11) & 31) << 3),
			G: uint8(((p >> 5) & 63) << 2),
			B: uint8((p & 31) << 3),
			A: 255,
		}
	case "GL_LUMINANCE8_ALPHA8":
		var l, a uint8
		if l, err = reader.ReadU8(); err != nil {
			return
		}
		if a, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{R: l, G: l, B: l, A: a}
	case "GL_LUMINANCE8":
		var l uint8
		if l, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{R: l, G: l, B: l, A: 255}
	default:
		err = fmt.Errorf("unsupported raw pixel format %s", format)
	}
	return
}

func decodeExternalTexture(swfPath, externalTexturePath string) (*image.NRGBA, error) {
	fullPath := filepath.Join(filepath.Dir(swfPath), externalTexturePath)
	return DecodeTextureFile(fullPath)
}

func decodeKTXBytes(data []byte) (*image.NRGBA, error) {
	tmpDir, err := os.MkdirTemp("", "sc-ktx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	inPath := filepath.Join(tmpDir, "input.ktx")
	outPath := filepath.Join(tmpDir, "output.png")
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return nil, err
	}

	if err := runBundledTool("lib/PVRTexToolCLI.exe", "-i", inPath, "-d", outPath, "-ics", "sRGB", "-noout"); err != nil {
		return nil, err
	}

	return decodePNGFile(outPath)
}

func decodeSCTXFile(path string) (*image.NRGBA, error) {
	tmpDir, err := os.MkdirTemp("", "sc-sctx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	outPath := filepath.Join(tmpDir, "decoded.png")
	if err := runBundledTool("lib/SctxConverter.exe", "decode", path, outPath, "-t"); err != nil {
		return nil, err
	}
	return decodePNGFile(outPath)
}

func DecodeTextureFile(path string) (*image.NRGBA, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".zktx":
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		payload, err := dec.DecodeAll(raw, nil)
		if err != nil {
			return nil, err
		}
		return decodeKTXBytes(payload)
	case ".ktx":
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return decodeKTXBytes(raw)
	case ".sctx":
		return decodeSCTXFile(path)
	default:
		return nil, fmt.Errorf("unsupported texture extension %s", ext)
	}
}

func decodePNGFile(path string) (*image.NRGBA, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return toNRGBA(img), nil
}

func runBundledTool(relativeExe string, args ...string) error {
	return runBundledToolInDir("", relativeExe, args...)
}

func runBundledToolInDir(workDir, relativeExe string, args ...string) error {
	exePath, err := filepath.Abs(relativeExe)
	if err != nil {
		return err
	}

	cmdArgs := args
	cmdName := exePath
	if runtime.GOOS != "windows" {
		cmdArgs = append([]string{exePath}, args...)
		cmdName = "wine"
	}

	cmd := exec.Command(cmdName, cmdArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if runtime.GOOS != "windows" {
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return fmt.Errorf("tool %s failed: %w", filepath.Base(relativeExe), err)
		}
		return fmt.Errorf("tool %s failed: %w: %s", filepath.Base(relativeExe), err, strings.TrimSpace(string(output)))
	}
	return nil
}
