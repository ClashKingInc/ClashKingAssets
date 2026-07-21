package sc

import (
	"encoding/binary"
	"fmt"
)

type Reader struct {
	buf []byte
	pos int
}

func NewReader(buf []byte) *Reader {
	return &Reader{buf: buf}
}

func (r *Reader) Len() int {
	return len(r.buf)
}

func (r *Reader) Pos() int {
	return r.pos
}

func (r *Reader) Remaining() int {
	return len(r.buf) - r.pos
}

func (r *Reader) Seek(pos int) error {
	if pos < 0 || pos > len(r.buf) {
		return fmt.Errorf("seek out of range: %d", pos)
	}
	r.pos = pos
	return nil
}

func (r *Reader) Skip(n int) error {
	if n < -r.pos || n > len(r.buf)-r.pos {
		return fmt.Errorf("skip out of range: pos=%d len=%d skip=%d", r.pos, len(r.buf), n)
	}
	r.pos += n
	return nil
}

func (r *Reader) Read(n int) ([]byte, error) {
	if n < 0 || n > len(r.buf)-r.pos {
		return nil, fmt.Errorf("read out of range: pos=%d len=%d need=%d", r.pos, len(r.buf), n)
	}
	out := r.buf[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *Reader) SectionEnd(length int) (int, error) {
	if length < 0 || length > len(r.buf)-r.pos {
		return 0, fmt.Errorf("section out of range: pos=%d len=%d section=%d", r.pos, len(r.buf), length)
	}
	return r.pos + length, nil
}

func (r *Reader) ReadU8() (uint8, error) {
	b, err := r.Read(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *Reader) ReadBool() (bool, error) {
	v, err := r.ReadU8()
	return v >= 1, err
}

func (r *Reader) ReadI16() (int16, error) {
	b, err := r.Read(2)
	if err != nil {
		return 0, err
	}
	return int16(binary.LittleEndian.Uint16(b)), nil
}

func (r *Reader) ReadU16() (uint16, error) {
	b, err := r.Read(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (r *Reader) ReadI32() (int32, error) {
	b, err := r.Read(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.LittleEndian.Uint32(b)), nil
}

func (r *Reader) ReadU32() (uint32, error) {
	b, err := r.Read(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *Reader) ReadU32Length() (int, error) {
	value, err := r.ReadU32()
	if err != nil {
		return 0, err
	}
	length, ok := uint32ToInt(value)
	if !ok {
		return 0, fmt.Errorf("length %d overflows int", value)
	}
	return length, nil
}

func uint32ToInt(value uint32) (int, bool) {
	const maxInt = int(^uint(0) >> 1)
	if uint64(value) > uint64(maxInt) {
		return 0, false
	}
	return int(value), true
}

func (r *Reader) ReadASCII() (string, error) {
	size, err := r.ReadU8()
	if err != nil {
		return "", err
	}
	if size == 0xFF {
		return "", nil
	}
	b, err := r.Read(int(size))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Reader) ReadTwip() (float64, error) {
	v, err := r.ReadI32()
	if err != nil {
		return 0, err
	}
	return float64(v) / 20.0, nil
}
