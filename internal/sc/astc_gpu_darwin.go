//go:build darwin && cgo

package sc

/*
#cgo LDFLAGS: -framework Foundation -framework Metal
#include <stdint.h>
#include <stdlib.h>

int sc_decode_astc_metal(
	const uint8_t *src,
	size_t src_len,
	uint8_t *dst,
	int width,
	int height,
	int block_x,
	int block_y,
	int srgb,
	char **error_message
);
*/
import "C"

import (
	"fmt"
	"image"
	"runtime"
	"unsafe"
)

func decodeASTCGPU(data []byte, width, height int, blockX, blockY byte, srgb bool) (*image.NRGBA, bool, error) {
	if width <= 0 || height <= 0 {
		return nil, true, fmt.Errorf("invalid ASTC dimensions %dx%d", width, height)
	}
	expected, ok := astcLevelSize(width, height, blockX, blockY)
	if !ok {
		return nil, true, fmt.Errorf("invalid ASTC dimensions %dx%d or block size %dx%d", width, height, blockX, blockY)
	}
	if len(data) < expected {
		return nil, true, fmt.Errorf("short ASTC payload: got %d want %d", len(data), expected)
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	var errorMessage *C.char
	result := C.sc_decode_astc_metal(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(expected),
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0])),
		C.int(width),
		C.int(height),
		C.int(blockX),
		C.int(blockY),
		C.int(boolToInt(srgb)),
		&errorMessage,
	)
	runtime.KeepAlive(data)
	runtime.KeepAlive(img)
	if result == 0 {
		if errorMessage == nil {
			return nil, true, fmt.Errorf("Metal ASTC decoder failed")
		}
		defer C.free(unsafe.Pointer(errorMessage))
		return nil, true, fmt.Errorf("%s", C.GoString(errorMessage))
	}
	return img, true, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
