package sc

import (
	"math"
	"testing"
)

func TestReaderRejectsOverflowingReadLength(t *testing.T) {
	reader := NewReader([]byte{1})
	if err := reader.Skip(1); err != nil {
		t.Fatalf("Skip() error = %v", err)
	}

	if _, err := reader.Read(math.MaxInt); err == nil {
		t.Fatal("Read() error = nil, want out-of-range error")
	}
	if got := reader.Pos(); got != 1 {
		t.Fatalf("Read() position = %d, want 1", got)
	}
}
