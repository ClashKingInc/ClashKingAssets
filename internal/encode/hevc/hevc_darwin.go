//go:build darwin && cgo

package hevc

/*
#cgo LDFLAGS: -framework Foundation -framework AVFoundation -framework CoreMedia -framework CoreVideo -framework VideoToolbox
#include <stdint.h>
#include <stdlib.h>
typedef struct sc_hevc_encoder sc_hevc_encoder;
sc_hevc_encoder *sc_hevc_encoder_create(const char *, int, int, int, int, int *, char **);
int sc_hevc_encoder_add_frame(sc_hevc_encoder *, const uint8_t *, int, int64_t, char **);
int sc_hevc_encoder_finish(sc_hevc_encoder *, int64_t, char **);
void sc_hevc_encoder_abort(sc_hevc_encoder *);
void sc_hevc_encoder_destroy(sc_hevc_encoder *);
int sc_hevc_inspect(const char *, int *, int64_t *, uint32_t *, int *, int *, int *, char **);
*/
import "C"

import (
	"fmt"
	"image"
	"runtime"
	"unsafe"
)

type avFoundationEncoder struct{ handle *C.sc_hevc_encoder }

func newNativeEncoder(path string, width, height int, opts Options) (nativeEncoder, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var kind C.int
	var message *C.char
	handle := C.sc_hevc_encoder_create(cpath, C.int(width), C.int(height), C.int(opts.Quality), C.int(boolInt(opts.RequireHardware)), &kind, &message)
	if handle == nil {
		return nil, bridgeError(kind, message)
	}
	return &avFoundationEncoder{handle: handle}, nil
}

func (e *avFoundationEncoder) addFrame(frame *image.NRGBA, pts int64) error {
	var message *C.char
	result := C.sc_hevc_encoder_add_frame(e.handle, (*C.uint8_t)(unsafe.Pointer(&frame.Pix[0])), C.int(frame.Stride), C.int64_t(pts), &message)
	runtime.KeepAlive(frame)
	if result != 1 {
		return bridgeError(result, message)
	}
	return nil
}

func (e *avFoundationEncoder) finish(duration int64) error {
	if e.handle == nil {
		return fmt.Errorf("native HEVC encoder is closed")
	}
	var message *C.char
	result := C.sc_hevc_encoder_finish(e.handle, C.int64_t(duration), &message)
	C.sc_hevc_encoder_destroy(e.handle)
	e.handle = nil
	if result != 1 {
		return bridgeError(result, message)
	}
	return nil
}

func (e *avFoundationEncoder) abort() {
	if e == nil || e.handle == nil {
		return
	}
	C.sc_hevc_encoder_abort(e.handle)
	C.sc_hevc_encoder_destroy(e.handle)
	e.handle = nil
}

func bridgeError(kind C.int, message *C.char) error {
	text := "unknown AVFoundation error"
	if message != nil {
		text = C.GoString(message)
		C.free(unsafe.Pointer(message))
	}
	if kind == -1 {
		return fmt.Errorf("%w: %s", ErrUnavailable, text)
	}
	return fmt.Errorf("%s", text)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type fileInspection struct {
	frames                     int
	durationMS                 int64
	codec                      uint32
	straightAlpha              bool
	minimumAlpha, maximumAlpha int
}

func inspectFile(path string) (fileInspection, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var frames, straight, minimumAlpha, maximumAlpha C.int
	var duration C.int64_t
	var codec C.uint32_t
	var message *C.char
	result := C.sc_hevc_inspect(cpath, &frames, &duration, &codec, &straight, &minimumAlpha, &maximumAlpha, &message)
	if result != 1 {
		return fileInspection{}, bridgeError(result, message)
	}
	return fileInspection{int(frames), int64(duration), uint32(codec), straight != 0, int(minimumAlpha), int(maximumAlpha)}, nil
}
