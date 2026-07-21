package sc

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsNegativeMovieClipElementCount(t *testing.T) {
	payload := make([]byte, 0, 9)
	payload = binary.LittleEndian.AppendUint16(payload, 7)
	payload = append(payload, 24)
	payload = binary.LittleEndian.AppendUint16(payload, 0)
	payload = binary.LittleEndian.AppendUint32(payload, ^uint32(0))

	path := writeLegacyAssetFixture(t, 0, 1, appendLegacyTag(nil, 10, payload))
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want invalid element count error")
	}
	if !strings.Contains(err.Error(), "frame elements count -1") {
		t.Fatalf("Load() error = %q, want negative element count context", err)
	}
}

func TestLoadRejectsNegativeTagLength(t *testing.T) {
	tags := []byte{useLowResTextureTag, 0xff, 0xff, 0xff, 0xff}
	path := writeLegacyAssetFixture(t, 0, 1, tags)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want invalid tag length error")
	}
	if !strings.Contains(err.Error(), "tag 23 length -1") {
		t.Fatalf("Load() error = %q, want negative tag length context", err)
	}
}

func TestLoadAcceptsEmptyLegacyAsset(t *testing.T) {
	path := writeLegacyAssetFixture(t, 0, 0, nil)
	swf, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(swf.Resources) != 0 || len(swf.Textures) != 0 {
		t.Fatalf("Load() resources = %d, textures = %d, want empty", len(swf.Resources), len(swf.Textures))
	}
}

func TestDecodeSC2CompressedClipFramesAcceptsClipWithoutFrames(t *testing.T) {
	clip := &MovieClip{}
	if err := decodeSC2CompressedClipFrames(clip, nil, 0, 0, 0, 0); err != nil {
		t.Fatalf("decodeSC2CompressedClipFrames() error = %v", err)
	}
}

func TestReadSC2FrameElementRejectsMissingStorage(t *testing.T) {
	if _, err := readSC2FrameElement(nil, 0); err == nil {
		t.Fatal("readSC2FrameElement() error = nil, want out-of-range error")
	}
}

func TestLoadReportsHugeShortTextureWithoutAllocatingIt(t *testing.T) {
	payload := []byte{0}
	payload = binary.LittleEndian.AppendUint16(payload, ^uint16(0))
	payload = binary.LittleEndian.AppendUint16(payload, ^uint16(0))
	path := writeLegacyAssetFixture(t, 1, 0, appendLegacyTag(nil, 1, payload))

	swf, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(swf.Textures) != 1 || swf.Textures[0] == nil {
		t.Fatalf("Load() textures = %#v", swf.Textures)
	}
	if !strings.Contains(swf.Textures[0].LoadError, "short raw texture payload") {
		t.Fatalf("Load() texture error = %q, want short payload", swf.Textures[0].LoadError)
	}
}

func TestLoadReportsMalformedSC2Flatbuffer(t *testing.T) {
	raw := []byte{'S', 'C', 0, 0, 0, 5}
	raw = binary.LittleEndian.AppendUint32(raw, 8)
	raw = append(raw, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0)
	path := filepath.Join(t.TempDir(), "malformed.sc")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "invalid SC2 flatbuffer") {
		t.Fatalf("Load() error = %v, want malformed SC2 error", err)
	}
}

func TestLoadRecoversCompressedAssetBeforeSTARTTrailer(t *testing.T) {
	basePath := writeLegacyAssetFixture(t, 0, 1, nil)
	raw, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	withTrailer := append(encodeZstdFixture(t, raw), []byte("STARTtrailer")...)
	path := filepath.Join(t.TempDir(), "trailer.sc")
	if err := os.WriteFile(path, withTrailer, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func BenchmarkLoadLegacyAsset(b *testing.B) {
	path := writeLegacyAssetFixture(b, 0, 1, make([]byte, 8<<20))
	b.SetBytes(8 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

type fixtureTB interface {
	Helper()
	TempDir() string
	Fatalf(format string, args ...any)
}

func writeLegacyAssetFixture(t fixtureTB, textures, movieClips uint16, tags []byte) string {
	t.Helper()

	raw := make([]byte, 0, 19+len(tags)+5)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, movieClips)
	raw = binary.LittleEndian.AppendUint16(raw, textures)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = append(raw, make([]byte, 5)...)
	raw = binary.LittleEndian.AppendUint16(raw, 0)
	raw = append(raw, tags...)
	raw = appendLegacyTag(raw, endTag, nil)

	path := filepath.Join(t.TempDir(), "fixture.sc")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func appendLegacyTag(dst []byte, tag byte, payload []byte) []byte {
	dst = append(dst, tag)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(payload)))
	return append(dst, payload...)
}
