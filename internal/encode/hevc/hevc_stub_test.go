//go:build !darwin || !cgo

package hevc

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNewReportsUnavailable(t *testing.T) {
	encoder, err := New(filepath.Join(t.TempDir(), "a.mov"), 2, 2, Options{Quality: 80})
	if encoder != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("encoder=%v error=%v", encoder, err)
	}
}
