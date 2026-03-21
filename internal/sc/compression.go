package sc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

var (
	errUnsupportedCompression = errors.New("unsupported compression signature")
)

func DecompressAsset(buffer []byte) ([]byte, error) {
	if len(buffer) == 0 {
		return nil, nil
	}

	switch detectSignature(buffer) {
	case sigNone:
		return buffer, nil
	case sigSC:
		return decompressSC(buffer)
	case sigZSTD:
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return dec.DecodeAll(buffer, nil)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedCompression, detectSignature(buffer))
	}
}

func DetectSCVersion(buffer []byte) int {
	if len(buffer) < 6 || !bytes.Equal(buffer[:2], []byte("SC")) {
		return 0
	}
	be := binary.BigEndian.Uint32(buffer[2:6])
	if be >= 1 && be <= 6 {
		return int(be)
	}
	le := binary.LittleEndian.Uint32(buffer[2:6])
	if le >= 1 && le <= 6 {
		return int(le)
	}
	return 0
}

type signature string

const (
	sigNone signature = "none"
	sigSC   signature = "sc"
	sigZSTD signature = "zstd"
	sigLZMA signature = "lzma"
	sigSCLZ signature = "sclz"
	sigSIG  signature = "sig"
)

func detectSignature(buffer []byte) signature {
	if len(buffer) >= 4 {
		switch {
		case bytes.Equal(buffer[:4], []byte("SCLZ")):
			return sigSCLZ
		case bytes.Equal(buffer[:2], []byte("SC")):
			return sigSC
		case bytes.Equal(buffer[:4], []byte("Sig:")):
			return sigSIG
		case bytes.Equal(buffer[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}):
			return sigZSTD
		}
	}

	if len(buffer) >= 5 && buffer[1] == 0x00 && buffer[2] == 0x00 && buffer[4] == 0x00 {
		return sigLZMA
	}

	return sigNone
}

func decompressSC(buffer []byte) ([]byte, error) {
	if len(buffer) < 10 {
		return nil, fmt.Errorf("invalid SC wrapper length: %d", len(buffer))
	}

	reader := bytes.NewReader(buffer)
	magic := make([]byte, 2)
	if _, err := io.ReadFull(reader, magic); err != nil {
		return nil, err
	}
	if !bytes.Equal(magic, []byte("SC")) {
		return nil, fmt.Errorf("invalid SC magic: %q", magic)
	}

	var version int32
	if err := binary.Read(reader, binary.BigEndian, &version); err != nil {
		return nil, err
	}
	if version >= 4 {
		if err := binary.Read(reader, binary.BigEndian, &version); err != nil {
			return nil, err
		}
	}

	var hashLen int32
	if err := binary.Read(reader, binary.BigEndian, &hashLen); err != nil {
		return nil, err
	}
	if hashLen < 0 || int(hashLen) > reader.Len() {
		return nil, fmt.Errorf("invalid SC hash length: %d", hashLen)
	}
	if _, err := reader.Seek(int64(hashLen), io.SeekCurrent); err != nil {
		return nil, err
	}

	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	return DecompressAsset(payload)
}
