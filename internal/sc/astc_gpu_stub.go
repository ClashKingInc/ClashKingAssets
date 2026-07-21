//go:build !darwin || !cgo

package sc

import "image"

func decodeASTCGPU(_ []byte, _, _ int, _, _ byte, _ bool) (*image.NRGBA, bool, error) {
	return nil, false, nil
}
