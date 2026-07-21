package hevc

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewValidatesOptionsBeforeStartingNativeEncoder(t *testing.T) {
	tests := []struct {
		name, path    string
		width, height int
		opts          Options
		want          string
	}{
		{"empty path", "", 2, 2, Options{Quality: 80}, "path is empty"},
		{"wrong extension", "a.webp", 2, 2, Options{Quality: 80}, "must end in .mov"},
		{"zero width", "a.mov", 0, 2, Options{Quality: 80}, "invalid HEVC canvas"},
		{"zero height", "a.mov", 2, 0, Options{Quality: 80}, "invalid HEVC canvas"},
		{"odd width", "a.mov", 3, 2, Options{Quality: 80}, "even dimensions"},
		{"odd height", "a.mov", 2, 3, Options{Quality: 80}, "even dimensions"},
		{"low quality", "a.mov", 2, 2, Options{Quality: -1}, "quality must be"},
		{"high quality", "a.mov", 2, 2, Options{Quality: 101}, "quality must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path != "" {
				path = filepath.Join(t.TempDir(), path)
			}
			encoder, err := New(path, tt.width, tt.height, tt.opts)
			if encoder != nil {
				encoder.Abort()
				t.Fatal("New returned encoder")
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want %q", err, tt.want)
			}
		})
	}
}

func TestAddFrameValidatesInput(t *testing.T) {
	encoder := &Encoder{width: 2, height: 2, native: validationNativeEncoder{}}
	if encoder.AddFrame(nil, 1) == nil {
		t.Fatal("accepted nil")
	}
	if encoder.AddFrame(image.NewNRGBA(image.Rect(0, 0, 3, 2)), 1) == nil {
		t.Fatal("accepted size")
	}
	if encoder.AddFrame(image.NewNRGBA(image.Rect(0, 0, 2, 2)), 0) == nil {
		t.Fatal("accepted duration")
	}
}

type validationNativeEncoder struct{}

func (validationNativeEncoder) addFrame(*image.NRGBA, int64) error { return nil }
func (validationNativeEncoder) finish(int64) error                 { return nil }
func (validationNativeEncoder) abort()                             {}
