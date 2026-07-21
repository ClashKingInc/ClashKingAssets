package sc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		ktxSize             int
		externalTexturePath string
		err                 error
	)
	if tag == 45 {
		ktxSize, err = reader.ReadU32Length()
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
		payload, err := reader.Read(ktxSize)
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
	pixels, ok := checkedProduct(tex.Width, tex.Height)
	if !ok {
		return 0, false
	}
	return checkedProduct(pixels, bytesPerPixel)
}

func checkedProduct(a, b int) (int, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	const maxInt = int(^uint(0) >> 1)
	if a != 0 && b > maxInt/a {
		return 0, false
	}
	return a * b, true
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
	expected, ok := expectedRawTextureBytes(tex)
	if !ok {
		return nil, fmt.Errorf("invalid raw texture dimensions %dx%d or pixel format %s", tex.Width, tex.Height, tex.PixelInternalFormat)
	}
	if expected > reader.Remaining() {
		return nil, fmt.Errorf("short raw texture payload: got %d want %d", reader.Remaining(), expected)
	}
	img := image.NewNRGBA(image.Rect(0, 0, tex.Width, tex.Height))
	if tex.Linear {
		data, err := reader.Read(expected)
		if err != nil {
			return nil, err
		}
		if err := decodeLinearRawTexture(img.Pix, data, tex.PixelInternalFormat); err != nil {
			return nil, err
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

func decodeLinearRawTexture(dst, src []byte, format string) error {
	switch format {
	case "GL_RGBA8":
		copy(dst, src)
	case "GL_BGRA8":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+4, dstPos+4 {
			dst[dstPos] = src[srcPos+2]
			dst[dstPos+1] = src[srcPos+1]
			dst[dstPos+2] = src[srcPos]
			dst[dstPos+3] = src[srcPos+3]
		}
	case "GL_RGB8":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+3, dstPos+4 {
			copy(dst[dstPos:dstPos+3], src[srcPos:srcPos+3])
			dst[dstPos+3] = 255
		}
	case "GL_RGBA4":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+2, dstPos+4 {
			p := binary.LittleEndian.Uint16(src[srcPos : srcPos+2])
			dst[dstPos] = expand4To8(uint8((p >> 12) & 15))
			dst[dstPos+1] = expand4To8(uint8((p >> 8) & 15))
			dst[dstPos+2] = expand4To8(uint8((p >> 4) & 15))
			dst[dstPos+3] = expand4To8(uint8(p & 15))
		}
	case "GL_RGB5_A1":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+2, dstPos+4 {
			p := binary.LittleEndian.Uint16(src[srcPos : srcPos+2])
			dst[dstPos] = expand5To8(uint8((p >> 11) & 31))
			dst[dstPos+1] = expand5To8(uint8((p >> 6) & 31))
			dst[dstPos+2] = expand5To8(uint8((p >> 1) & 31))
			dst[dstPos+3] = uint8(p&1) * 255
		}
	case "GL_RGB565":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+2, dstPos+4 {
			p := binary.LittleEndian.Uint16(src[srcPos : srcPos+2])
			dst[dstPos] = expand5To8(uint8((p >> 11) & 31))
			dst[dstPos+1] = expand6To8(uint8((p >> 5) & 63))
			dst[dstPos+2] = expand5To8(uint8(p & 31))
			dst[dstPos+3] = 255
		}
	case "GL_ALPHA8":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+1, dstPos+4 {
			dst[dstPos+3] = src[srcPos]
		}
	case "GL_LUMINANCE8_ALPHA8":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+2, dstPos+4 {
			l := src[srcPos]
			dst[dstPos] = l
			dst[dstPos+1] = l
			dst[dstPos+2] = l
			dst[dstPos+3] = src[srcPos+1]
		}
	case "GL_LUMINANCE8":
		for srcPos, dstPos := 0, 0; srcPos < len(src); srcPos, dstPos = srcPos+1, dstPos+4 {
			l := src[srcPos]
			dst[dstPos] = l
			dst[dstPos+1] = l
			dst[dstPos+2] = l
			dst[dstPos+3] = 255
		}
	default:
		return fmt.Errorf("unsupported raw pixel format %s", format)
	}
	return nil
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
			R: expand4To8(uint8((p >> 12) & 15)),
			G: expand4To8(uint8((p >> 8) & 15)),
			B: expand4To8(uint8((p >> 4) & 15)),
			A: expand4To8(uint8(p & 15)),
		}
	case "GL_RGB5_A1":
		var p uint16
		if p, err = reader.ReadU16(); err != nil {
			return
		}
		c = color.NRGBA{
			R: expand5To8(uint8((p >> 11) & 31)),
			G: expand5To8(uint8((p >> 6) & 31)),
			B: expand5To8(uint8((p >> 1) & 31)),
			A: uint8((p & 1) * 255),
		}
	case "GL_RGB565":
		var p uint16
		if p, err = reader.ReadU16(); err != nil {
			return
		}
		c = color.NRGBA{
			R: expand5To8(uint8((p >> 11) & 31)),
			G: expand6To8(uint8((p >> 5) & 63)),
			B: expand5To8(uint8(p & 31)),
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

func expand4To8(v uint8) uint8 { return v<<4 | v }

func expand5To8(v uint8) uint8 { return v<<3 | v>>2 }

func expand6To8(v uint8) uint8 { return v<<2 | v>>4 }

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

func parseSCTXTexture(raw []byte) (texture *sctxTexture, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			texture = nil
			err = fmt.Errorf("invalid SCTX flatbuffer: %v", recovered)
		}
	}()
	reader := NewReader(raw)

	textureDataLength, err := reader.ReadU32Length()
	if err != nil {
		return nil, err
	}
	textureData, err := reader.Read(textureDataLength)
	if err != nil {
		return nil, err
	}
	if !SCTX.TextureDataBufferHasIdentifier(textureData) {
		return nil, fmt.Errorf("invalid SCTX texture header")
	}
	root := SCTX.GetRootAsTextureData(textureData, 0)

	mipMapsDataLength, err := reader.ReadU32Length()
	if err != nil {
		return nil, err
	}
	mipMapsData, err := reader.Read(mipMapsDataLength)
	if err != nil {
		return nil, err
	}
	mipMapsReader := NewReader(mipMapsData)
	payloadStart := reader.Pos()

	levelsCount := int(root.LevelsCount())
	levels := make([]sctxLevel, 0, levelsCount)
	for i := 0; i < levelsCount; i++ {
		mipMapLength, err := mipMapsReader.ReadU32Length()
		if err != nil {
			return nil, err
		}
		mipMapData, err := mipMapsReader.Read(mipMapLength)
		if err != nil {
			return nil, err
		}
		mipMap := SCTX.GetRootAsMipMap(mipMapData, 0)
		offset, ok := uint32ToInt(mipMap.Offset())
		if !ok {
			return nil, fmt.Errorf("SCTX mip level %d offset overflows int", i)
		}
		levels = append(levels, sctxLevel{
			width:  int(mipMap.Width()),
			height: int(mipMap.Height()),
			offset: offset,
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

	textureLength := root.TextureLength()
	if textureLength < 0 {
		return nil, fmt.Errorf("invalid SCTX texture length %d", textureLength)
	}
	payload, err := decodeSCTXPayload(raw[payloadStart:], int(textureLength))
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
		var err error
		payload, err = decodeZstdAll(raw, nil)
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
		return astcLevelSize(width, height, blockX, blockY)
	}
	pixels, ok := checkedProduct(width, height)
	if !ok {
		return 0, false
	}
	switch pixelType {
	case sctxPixelA8Unorm, sctxPixelR8Unorm, sctxPixelR8UnormSRGB, sctxPixelLuminance:
		return pixels, true
	case sctxPixelLuminanceAlpha, sctxPixelR5G6B5Unorm:
		return checkedProduct(pixels, 2)
	case sctxPixelRGB8Unorm, sctxPixelRGB8UnormSRGB:
		return checkedProduct(pixels, 3)
	case sctxPixelRGBA8Unorm, sctxPixelRGBA8UnormSRGB, sctxPixelBGRA8Unorm, sctxPixelBGRA8UnormSRGB:
		return checkedProduct(pixels, 4)
	default:
		return 0, false
	}
}

func astcLevelSize(width, height int, blockX, blockY byte) (int, bool) {
	if width <= 0 || height <= 0 || blockX == 0 || blockY == 0 {
		return 0, false
	}
	blocksWide := 1 + (width-1)/int(blockX)
	blocksHigh := 1 + (height-1)/int(blockY)
	blocks, ok := checkedProduct(blocksWide, blocksHigh)
	if !ok {
		return 0, false
	}
	return checkedProduct(blocks, 16)
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
	if img, available, gpuErr := decodeASTCGPU(data, width, height, blockX, blockY, srgb); available {
		if gpuErr == nil {
			return img, nil
		}
		img, fallbackErr := decodeASTCLevelExternal(data, width, height, blockX, blockY, srgb)
		if fallbackErr == nil {
			return img, nil
		}
		return nil, fmt.Errorf("Metal ASTC decode failed: %v; astcenc fallback failed: %w", gpuErr, fallbackErr)
	}
	return decodeASTCLevelExternal(data, width, height, blockX, blockY, srgb)
}

func decodeASTCLevelExternal(data []byte, width, height int, blockX, blockY byte, srgb bool) (*image.NRGBA, error) {
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeTextureBytes(path, raw)
}

func DecodeTextureBytes(name string, raw []byte) (*image.NRGBA, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".zktx":
		payload, err := decodeZstdAll(raw, nil)
		if err != nil {
			return nil, err
		}
		return decodeKTXBytes(payload)
	case ".ktx":
		return decodeKTXBytes(raw)
	case ".sctx":
		texture, err := parseSCTXTexture(raw)
		if err != nil {
			return nil, err
		}
		return decodeSCTXTexture(texture)
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

func runCommand(workDir, cmdName string, args ...string) error {
	cmd := exec.Command(cmdName, args...)
	if workDir != "" {
		cmd.Dir = workDir
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
