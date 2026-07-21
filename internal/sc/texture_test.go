package sc

import (
	"encoding/binary"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"

	SCTX "sc2fla/internal/sc/sctxfb/sc/texture/SCTX"
)

func TestLoadExpandsPackedTextureChannelsToEightBits(t *testing.T) {
	tests := []struct {
		name       string
		pixelIndex byte
		packed     uint16
		want       color.NRGBA
	}{
		{name: "RGBA4", pixelIndex: 2, packed: 0x8CEF, want: color.NRGBA{R: 136, G: 204, B: 238, A: 255}},
		{name: "RGB5_A1", pixelIndex: 3, packed: uint16(16<<11 | 24<<6 | 30<<1 | 1), want: color.NRGBA{R: 132, G: 198, B: 247, A: 255}},
		{name: "RGB565", pixelIndex: 4, packed: uint16(16<<11 | 32<<5 | 30), want: color.NRGBA{R: 132, G: 130, B: 247, A: 255}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLegacyTextureFixture(t, tc.pixelIndex, tc.packed)
			swf, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(swf.Textures) != 1 || swf.Textures[0] == nil || swf.Textures[0].Image == nil {
				t.Fatalf("Load() texture = %#v", swf.Textures)
			}
			if got := swf.Textures[0].Image.NRGBAAt(0, 0); got != tc.want {
				t.Fatalf("Load() pixel = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDecodeTextureFileReportsMalformedSCTXFlatbuffer(t *testing.T) {
	raw := binary.LittleEndian.AppendUint32(nil, 8)
	raw = append(raw, 0xff, 0xff, 0xff, 0xff, 'S', 'C', 'T', 'X')
	raw = binary.LittleEndian.AppendUint32(raw, 0)
	path := filepath.Join(t.TempDir(), "malformed.sctx")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := DecodeTextureFile(path)
	if err == nil || !strings.Contains(err.Error(), "invalid SCTX flatbuffer") {
		t.Fatalf("DecodeTextureFile() error = %v, want malformed SCTX error", err)
	}
}

func TestLoadDoesNotTreatSTARTPixelBytesAsTrailer(t *testing.T) {
	payload := []byte{0}
	payload = binary.LittleEndian.AppendUint16(payload, 2)
	payload = binary.LittleEndian.AppendUint16(payload, 1)
	payload = append(payload, []byte("STARTxyz")...)
	path := writeLegacyAssetFixture(t, 1, 0, appendLegacyTag(nil, 1, payload))

	swf, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := swf.Textures[0].Image.NRGBAAt(0, 0); got != (color.NRGBA{R: 'S', G: 'T', B: 'A', A: 'R'}) {
		t.Fatalf("Load() first pixel = %#v", got)
	}
}

func TestDecodeTextureFileParsesSyntheticSCTX(t *testing.T) {
	builder := flatbuffers.NewBuilder(64)
	SCTX.TextureDataStart(builder)
	SCTX.TextureDataAddPixelType(builder, sctxPixelR5G6B5Unorm)
	SCTX.TextureDataAddWidth(builder, 1)
	SCTX.TextureDataAddHeight(builder, 1)
	SCTX.TextureDataAddLevelsCount(builder, 0)
	SCTX.TextureDataAddTextureLength(builder, 2)
	header := SCTX.TextureDataEnd(builder)
	SCTX.FinishTextureDataBuffer(builder, header)
	headerBytes := builder.FinishedBytes()

	raw := binary.LittleEndian.AppendUint32(nil, uint32(len(headerBytes)))
	raw = append(raw, headerBytes...)
	raw = binary.LittleEndian.AppendUint32(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, uint16(16<<11|32<<5|30))
	path := filepath.Join(t.TempDir(), "synthetic.sctx")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	img, err := DecodeTextureFile(path)
	if err != nil {
		t.Fatalf("DecodeTextureFile() error = %v", err)
	}
	if got, want := img.NRGBAAt(0, 0), (color.NRGBA{R: 132, G: 130, B: 247, A: 255}); got != want {
		t.Fatalf("DecodeTextureFile() pixel = %#v, want %#v", got, want)
	}
	bytesImage, err := DecodeTextureBytes("synthetic.sctx", raw)
	if err != nil {
		t.Fatalf("DecodeTextureBytes() error = %v", err)
	}
	if got, want := bytesImage.NRGBAAt(0, 0), img.NRGBAAt(0, 0); got != want {
		t.Fatalf("DecodeTextureBytes() pixel = %#v, want %#v", got, want)
	}
}

func writeLegacyTextureFixture(t *testing.T, pixelIndex byte, packed uint16) string {
	t.Helper()

	// The uncompressed legacy header declares one texture and no other
	// resources, matrices, color transforms, or exports.
	raw := make([]byte, 0, 32)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 1)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = append(raw, make([]byte, 5)...)
	raw = binary.LittleEndian.AppendUint16(raw, 0)

	const texturePayloadLength = 7
	raw = append(raw, 1)
	raw = binary.LittleEndian.AppendUint32(raw, texturePayloadLength)
	raw = append(raw, pixelIndex)
	raw = binary.LittleEndian.AppendUint16(raw, 1)
	raw = binary.LittleEndian.AppendUint16(raw, 1)
	raw = binary.LittleEndian.AppendUint16(raw, packed)
	raw = append(raw, endTag)
	raw = binary.LittleEndian.AppendUint32(raw, 0)

	path := filepath.Join(t.TempDir(), "texture.sc")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func BenchmarkDecodeRawTexture(b *testing.B) {
	for _, tc := range []struct {
		name   string
		format string
		bpp    int
	}{
		{name: "RGBA8_2048", format: "GL_RGBA8", bpp: 4},
		{name: "RGB565_2048", format: "GL_RGB565", bpp: 2},
	} {
		b.Run(tc.name, func(b *testing.B) {
			const width, height = 2048, 2048
			data := make([]byte, width*height*tc.bpp)
			tex := &Texture{Width: width, Height: height, Linear: true, PixelInternalFormat: tc.format}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := decodeRawTexture(NewReader(data), tex); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
