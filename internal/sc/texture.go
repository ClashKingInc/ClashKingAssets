package sc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"

	SCTX "sc2fla/internal/sc/sctxfb/sc/texture/SCTX"
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

const (
	sctxPixelA8Unorm        = 1
	sctxPixelR8Unorm        = 10
	sctxPixelR8UnormSRGB    = 11
	sctxPixelR5G6B5Unorm    = 40
	sctxPixelRGBA8Unorm     = 70
	sctxPixelRGBA8UnormSRGB = 71
	sctxPixelBGRA8Unorm     = 80
	sctxPixelBGRA8UnormSRGB = 81
	sctxPixelLuminance      = 264
	sctxPixelLuminanceAlpha = 265
	sctxPixelRGB8Unorm      = 266
	sctxPixelRGB8UnormSRGB  = 267
	astcFileMagic           = 0x5CA1AB13
)

type sctxTexture struct {
	pixelType uint32
	width     int
	height    int
	levels    []sctxLevel
	payload   []byte
}

type sctxLevel struct {
	width  int
	height int
	offset int
}

func loadTexture(reader *Reader, tag uint8, tagEnd int, hasExternalTexture bool, swfPath string) (*Texture, error) {
	tagStart := reader.Pos()
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
		rawBytes := tagEnd - reader.Pos()
		if rawBytes < 0 {
			return nil, fmt.Errorf("texture tag %d has invalid bounds: pos=%d end=%d", tag, reader.Pos(), tagEnd)
		}
		payload, err := reader.Read(rawBytes)
		if err != nil {
			return nil, err
		}
		rawReader := NewReader(payload)
		img, err := decodeRawTexture(rawReader, tex)
		if err != nil {
			consumed := len(payload) - rawReader.Remaining()
			expectedBytes, expectedKnown := expectedRawTextureBytes(tex)
			expectedLabel := "unknown"
			if expectedKnown {
				expectedLabel = fmt.Sprintf("%d", expectedBytes)
			}
			tex.LoadError = fmt.Sprintf(
				"raw texture decode failed: tag=%d tag_pos=%d pixel_format=%s pixel_internal_format=%s pixel_type=%s size=%dx%d linear=%t consumed=%d remaining=%d expected=%s: %v",
				tag,
				tagStart,
				tex.PixelFormat,
				tex.PixelInternalFormat,
				tex.PixelType,
				tex.Width,
				tex.Height,
				tex.Linear,
				consumed,
				rawReader.Remaining(),
				expectedLabel,
				err,
			)
			return tex, nil
		}
		tex.Image = img
		return tex, nil
	default:
		return tex, nil
	}
}

func expectedRawTextureBytes(tex *Texture) (int, bool) {
	bytesPerPixel, ok := rawBytesPerPixel(tex.PixelInternalFormat)
	if !ok || tex.Width < 0 || tex.Height < 0 {
		return 0, false
	}
	return tex.Width * tex.Height * bytesPerPixel, true
}

func rawBytesPerPixel(format string) (int, bool) {
	switch format {
	case "GL_RGBA8", "GL_BGRA8":
		return 4, true
	case "GL_RGBA4", "GL_RGB5_A1", "GL_RGB565", "GL_LUMINANCE8_ALPHA8":
		return 2, true
	case "GL_RGB8":
		return 3, true
	case "GL_ALPHA8", "GL_LUMINANCE8":
		return 1, true
	default:
		return 0, false
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
	case "GL_RGB8":
		var r, g, b uint8
		if r, err = reader.ReadU8(); err != nil {
			return
		}
		if g, err = reader.ReadU8(); err != nil {
			return
		}
		if b, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{R: r, G: g, B: b, A: 255}
	case "GL_BGRA8":
		var b, g, r, a uint8
		if b, err = reader.ReadU8(); err != nil {
			return
		}
		if g, err = reader.ReadU8(); err != nil {
			return
		}
		if r, err = reader.ReadU8(); err != nil {
			return
		}
		if a, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{R: r, G: g, B: b, A: a}
	case "GL_ALPHA8":
		var a uint8
		if a, err = reader.ReadU8(); err != nil {
			return
		}
		c = color.NRGBA{A: a}
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

	if err := runASTCEnc(tmpDir, "-ds", inPath, outPath); err != nil {
		return nil, err
	}

	return decodePNGFile(outPath)
}

func decodeSCTXFile(path string) (*image.NRGBA, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tex, err := parseSCTXTexture(raw)
	if err != nil {
		return nil, err
	}
	return decodeSCTXTexture(tex)
}

func parseSCTXTexture(raw []byte) (*sctxTexture, error) {
	reader := NewReader(raw)

	textureDataLength, err := reader.ReadU32()
	if err != nil {
		return nil, err
	}
	textureData, err := reader.Read(int(textureDataLength))
	if err != nil {
		return nil, err
	}
	if !SCTX.TextureDataBufferHasIdentifier(textureData) {
		return nil, fmt.Errorf("invalid SCTX texture header")
	}
	root := SCTX.GetRootAsTextureData(textureData, 0)

	mipMapsDataLength, err := reader.ReadU32()
	if err != nil {
		return nil, err
	}
	mipMapsStart := reader.Pos()
	payloadStart := mipMapsStart + int(mipMapsDataLength)

	levelsCount := int(root.LevelsCount())
	levels := make([]sctxLevel, 0, levelsCount)
	for i := 0; i < levelsCount; i++ {
		mipMapLength, err := reader.ReadU32()
		if err != nil {
			return nil, err
		}
		mipMapData, err := reader.Read(int(mipMapLength))
		if err != nil {
			return nil, err
		}
		mipMap := SCTX.GetRootAsMipMap(mipMapData, 0)
		levels = append(levels, sctxLevel{
			width:  int(mipMap.Width()),
			height: int(mipMap.Height()),
			offset: int(mipMap.Offset()),
		})
	}
	if levelsCount == 0 {
		levels = append(levels, sctxLevel{width: int(root.Width()), height: int(root.Height()), offset: 0})
	}

	if root.Flags()&SCTX.TextureFlagsuse_padding != 0 {
		payloadStart = (payloadStart + 15) &^ 15
	}
	if payloadStart < 0 || payloadStart > len(raw) {
		return nil, fmt.Errorf("invalid SCTX payload offset")
	}

	payload, err := decodeSCTXPayload(raw[payloadStart:], int(root.TextureLength()))
	if err != nil {
		return nil, err
	}

	return &sctxTexture{
		pixelType: root.PixelType(),
		width:     int(root.Width()),
		height:    int(root.Height()),
		levels:    levels,
		payload:   payload,
	}, nil
}

func decodeSCTXPayload(raw []byte, textureLength int) ([]byte, error) {
	payload := raw
	if detectSignature(raw) == sigZSTD {
		dec, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		payload, err = io.ReadAll(dec)
		if err != nil {
			return nil, err
		}
	}
	if textureLength > 0 {
		if len(payload) < textureLength {
			return nil, fmt.Errorf("short SCTX payload: got %d want %d", len(payload), textureLength)
		}
		payload = payload[:textureLength]
	}
	return payload, nil
}

func decodeSCTXTexture(tex *sctxTexture) (*image.NRGBA, error) {
	level, err := tex.levelPayload(0)
	if err != nil {
		return nil, err
	}

	if blockX, blockY, srgb, ok := sctxASTCBlock(tex.pixelType); ok {
		return decodeASTCLevel(level, tex.width, tex.height, blockX, blockY, srgb)
	}

	format, ok := sctxRawFormat(tex.pixelType)
	if !ok {
		return nil, fmt.Errorf("unsupported SCTX pixel type %d", tex.pixelType)
	}
	return decodeRawSCTXLevel(level, tex.width, tex.height, format)
}

func (t *sctxTexture) levelPayload(index int) ([]byte, error) {
	if index < 0 || index >= len(t.levels) {
		return nil, fmt.Errorf("SCTX level %d out of range", index)
	}
	start := t.levels[index].offset
	if start < 0 || start > len(t.payload) {
		return nil, fmt.Errorf("SCTX level %d offset out of range", index)
	}
	end := len(t.payload)
	if index+1 < len(t.levels) {
		end = t.levels[index+1].offset
	} else if expected, ok := sctxLevelSize(t.pixelType, t.levels[index].width, t.levels[index].height); ok {
		end = start + expected
	}
	if end < start || end > len(t.payload) {
		return nil, fmt.Errorf("SCTX level %d bounds out of range", index)
	}
	return t.payload[start:end], nil
}

func decodeRawSCTXLevel(data []byte, width, height int, format string) (*image.NRGBA, error) {
	tex := &Texture{
		Width:               width,
		Height:              height,
		Linear:              true,
		PixelInternalFormat: format,
	}
	return decodeRawTexture(NewReader(data), tex)
}

func sctxRawFormat(pixelType uint32) (string, bool) {
	switch pixelType {
	case sctxPixelA8Unorm:
		return "GL_ALPHA8", true
	case sctxPixelR8Unorm, sctxPixelR8UnormSRGB, sctxPixelLuminance:
		return "GL_LUMINANCE8", true
	case sctxPixelLuminanceAlpha:
		return "GL_LUMINANCE8_ALPHA8", true
	case sctxPixelR5G6B5Unorm:
		return "GL_RGB565", true
	case sctxPixelRGBA8Unorm, sctxPixelRGBA8UnormSRGB:
		return "GL_RGBA8", true
	case sctxPixelBGRA8Unorm, sctxPixelBGRA8UnormSRGB:
		return "GL_BGRA8", true
	case sctxPixelRGB8Unorm, sctxPixelRGB8UnormSRGB:
		return "GL_RGB8", true
	default:
		return "", false
	}
}

func sctxLevelSize(pixelType uint32, width, height int) (int, bool) {
	if width <= 0 || height <= 0 {
		return 0, false
	}
	if blockX, blockY, _, ok := sctxASTCBlock(pixelType); ok {
		blocksWide := (width + int(blockX) - 1) / int(blockX)
		blocksHigh := (height + int(blockY) - 1) / int(blockY)
		return blocksWide * blocksHigh * 16, true
	}
	switch pixelType {
	case sctxPixelA8Unorm, sctxPixelR8Unorm, sctxPixelR8UnormSRGB, sctxPixelLuminance:
		return width * height, true
	case sctxPixelLuminanceAlpha, sctxPixelR5G6B5Unorm:
		return width * height * 2, true
	case sctxPixelRGB8Unorm, sctxPixelRGB8UnormSRGB:
		return width * height * 3, true
	case sctxPixelRGBA8Unorm, sctxPixelRGBA8UnormSRGB, sctxPixelBGRA8Unorm, sctxPixelBGRA8UnormSRGB:
		return width * height * 4, true
	default:
		return 0, false
	}
}

func sctxASTCBlock(pixelType uint32) (byte, byte, bool, bool) {
	srgb := pixelType >= 186 && pixelType <= 198
	switch pixelType {
	case 186, 204:
		return 4, 4, srgb, true
	case 187, 205:
		return 5, 4, srgb, true
	case 188, 206:
		return 5, 5, srgb, true
	case 189, 207:
		return 6, 5, srgb, true
	case 190, 208:
		return 6, 6, srgb, true
	case 192, 210:
		return 8, 5, srgb, true
	case 193, 211:
		return 8, 6, srgb, true
	case 194, 212:
		return 8, 8, srgb, true
	case 195, 213:
		return 10, 5, srgb, true
	case 196, 214:
		return 10, 6, srgb, true
	case 197, 215:
		return 10, 8, srgb, true
	case 198, 216:
		return 10, 10, srgb, true
	case 199, 217:
		return 12, 10, srgb, true
	case 200, 218:
		return 12, 12, srgb, true
	default:
		return 0, 0, false, false
	}
}

func decodeASTCLevel(data []byte, width, height int, blockX, blockY byte, srgb bool) (*image.NRGBA, error) {
	tmpDir, err := os.MkdirTemp("", "sc-astc-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	inPath := filepath.Join(tmpDir, "input.astc")
	outPath := filepath.Join(tmpDir, "output.png")
	if err := os.WriteFile(inPath, buildASTCFile(data, width, height, blockX, blockY), 0o600); err != nil {
		return nil, err
	}

	mode := "-dl"
	if srgb {
		mode = "-ds"
	}
	if err := runASTCEnc(tmpDir, mode, inPath, outPath); err != nil {
		return nil, err
	}
	return decodePNGFile(outPath)
}

func buildASTCFile(data []byte, width, height int, blockX, blockY byte) []byte {
	buf := make([]byte, 16+len(data))
	binary.LittleEndian.PutUint32(buf[:4], astcFileMagic)
	buf[4] = blockX
	buf[5] = blockY
	buf[6] = 1
	put24(buf[7:10], width)
	put24(buf[10:13], height)
	put24(buf[13:16], 1)
	copy(buf[16:], data)
	return buf
}

func put24(dst []byte, v int) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v >> 16)
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

func runASTCEnc(workDir string, args ...string) error {
	cmdName, err := findASTCEnc()
	if err != nil {
		return err
	}
	return runCommand(workDir, cmdName, args...)
}

func findASTCEnc() (string, error) {
	candidates := []string{
		"astcenc",
		"astc-encoder",
		filepath.Join("lib", "astcenc"),
		filepath.Join("lib", "astcenc.exe"),
		filepath.Join("lib", "astcenc-avx2"),
		filepath.Join("lib", "astcenc-avx2.exe"),
		filepath.Join("lib", "astcenc-sse4.1"),
		filepath.Join("lib", "astcenc-sse4.1.exe"),
		filepath.Join("lib", "astcenc-sse2"),
		filepath.Join("lib", "astcenc-sse2.exe"),
		filepath.Join("lib", "astcenc-neon"),
		filepath.Join("lib", "astcenc-neon.exe"),
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, string(filepath.Separator)) {
			absPath, err := resolveWorkspacePath(candidate)
			if err != nil {
				continue
			}
			if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
				return absPath, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("astcenc not found in PATH or lib/")
}

func runBundledTool(relativeExe string, args ...string) error {
	return runBundledToolInDir("", relativeExe, args...)
}

func runBundledToolInDir(workDir, relativeExe string, args ...string) error {
	exePath, err := resolveWorkspacePath(relativeExe)
	if err != nil {
		return err
	}

	cmdArgs := args
	cmdName := exePath
	if runtime.GOOS != "windows" {
		cmdArgs = append([]string{exePath}, args...)
		cmdName = "wine"
	}

	return runCommand(workDir, cmdName, cmdArgs...)
}

func runCommand(workDir, cmdName string, args ...string) error {
	cmd := exec.Command(cmdName, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if runtime.GOOS != "windows" && cmdName == "wine" {
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return fmt.Errorf("tool %s failed: %w", filepath.Base(cmdName), err)
		}
		return fmt.Errorf("tool %s failed: %w: %s", filepath.Base(cmdName), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func resolveWorkspacePath(relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) {
		return relativePath, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(wd, relativePath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return filepath.Abs(relativePath)
}
