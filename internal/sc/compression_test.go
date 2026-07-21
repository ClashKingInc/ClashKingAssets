package sc

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecompressAssetZstd(t *testing.T) {
	want := bytes.Repeat([]byte("SC asset payload"), 1024)
	encoded := encodeZstdFixture(t, want)

	got, err := DecompressAsset(encoded)
	if err != nil {
		t.Fatalf("DecompressAsset() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DecompressAsset() returned %d bytes, want %d matching bytes", len(got), len(want))
	}
}

func TestDecompressAssetZstdConcurrent(t *testing.T) {
	want := bytes.Repeat([]byte("concurrent SC asset payload"), 1024)
	encoded := encodeZstdFixture(t, want)
	const workers = 16
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := DecompressAsset(encoded)
			if err == nil && !bytes.Equal(got, want) {
				err = fmt.Errorf("decompressed payload does not match")
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkDecompressAssetZstd(b *testing.B) {
	want := bytes.Repeat([]byte("0123456789abcdef"), 512*1024)
	encoded := encodeZstdFixture(b, want)
	b.SetBytes(int64(len(want)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		got, err := DecompressAsset(encoded)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != len(want) {
			b.Fatalf("DecompressAsset() returned %d bytes, want %d", len(got), len(want))
		}
	}
}

type testLogger interface {
	Helper()
	Fatalf(format string, args ...any)
}

func encodeZstdFixture(t testLogger, raw []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(raw, nil)
}
