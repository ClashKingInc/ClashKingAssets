package sc

import "fmt"

var blendModes = []string{
	"",
	"",
	"",
	"multiply",
	"screen",
	"",
	"",
	"",
	"add",
}

const (
	movieclipEndFrameTag   = 0
	movieclipScalingGrid   = 31
	movieclipMatrixBankTag = 41
)

var movieclipFrameTags = map[uint8]bool{
	5:  true,
	11: true,
}

func loadMovieClipModifier(reader *Reader, tag uint8) (*MovieClipModifier, error) {
	id, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	return &MovieClipModifier{ID: id, Modifier: tag}, nil
}

func loadMovieClip(reader *Reader, tag uint8) (*MovieClip, error) {
	id, err := reader.ReadU16()
	if err != nil {
		return nil, fmt.Errorf("movieclip id: %w", err)
	}
	frameRate, err := reader.ReadU8()
	if err != nil {
		return nil, fmt.Errorf("movieclip %d frame rate: %w", id, err)
	}
	frameCount, err := reader.ReadU16()
	if err != nil {
		return nil, fmt.Errorf("movieclip %d frame count: %w", id, err)
	}

	clip := &MovieClip{
		ID:          id,
		FrameRate:   int(frameRate),
		Frames:      make([]MovieClipFrame, int(frameCount)),
		UnknownFlag: tag == 35,
	}

	frameElements := make([]FrameElement, 0)
	if tag == 14 {
		return nil, fmt.Errorf("movieclip tag 14 is unsupported")
	}
	if tag == 49 {
		customPropertyCount, err := reader.ReadU8()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d custom property count: %w", id, err)
		}
		for range int(customPropertyCount) {
			propertyType, err := reader.ReadU8()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d custom property type: %w", id, err)
			}
			switch propertyType {
			case 0:
				if _, err := reader.ReadU8(); err != nil {
					return nil, fmt.Errorf("movieclip %d custom property bool: %w", id, err)
				}
			default:
				return nil, fmt.Errorf("unknown custom property type %d in movieclip %d", propertyType, id)
			}
		}
	}

	if tag != 3 {
		frameElementsCount, err := reader.ReadI32()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d frame elements count: %w", id, err)
		}
		if frameElementsCount < 0 || int64(frameElementsCount)*6 > int64(reader.Remaining()) {
			return nil, fmt.Errorf("movieclip %d invalid frame elements count %d with %d bytes remaining", id, frameElementsCount, reader.Remaining())
		}
		frameElements = make([]FrameElement, 0, int(frameElementsCount))
		for range int(frameElementsCount) {
			bindIndex, err := reader.ReadU16()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d frame element bind: %w", id, err)
			}
			matrixIndex, err := reader.ReadU16()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d frame element matrix: %w", id, err)
			}
			colorIndex, err := reader.ReadU16()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d frame element color: %w", id, err)
			}
			frameElements = append(frameElements, FrameElement{
				Bind:   bindIndex,
				Matrix: matrixIndex,
				Color:  colorIndex,
			})
		}
	}

	bindsCount, err := reader.ReadU16()
	if err != nil {
		return nil, fmt.Errorf("movieclip %d binds count: %w", id, err)
	}
	clip.Binds = make([]Bind, int(bindsCount))
	for i := range clip.Binds {
		bindID, err := reader.ReadU16()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d bind id: %w", id, err)
		}
		clip.Binds[i] = Bind{ID: bindID}
	}
	if tag == 12 || tag == 35 || tag == 49 {
		for i := range clip.Binds {
			blendIndex, err := reader.ReadU8()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d bind blend: %w", id, err)
			}
			blendIndex &= 0x3F
			if int(blendIndex) < len(blendModes) {
				clip.Binds[i].Blend = blendModes[blendIndex]
			}
		}
	}
	for i := range clip.Binds {
		name, err := reader.ReadASCII()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d bind name: %w", id, err)
		}
		clip.Binds[i].Name = name
	}

	framesLoaded := 0
	frameElementsOffset := 0
	for {
		frameTag, err := reader.ReadU8()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d frame tag: %w", id, err)
		}
		frameTagLength, err := reader.ReadI32()
		if err != nil {
			return nil, fmt.Errorf("movieclip %d frame tag %d length: %w", id, frameTag, err)
		}
		frameTagEnd, err := reader.SectionEnd(int(frameTagLength))
		if err != nil {
			return nil, fmt.Errorf("movieclip %d frame tag %d length %d: %w", id, frameTag, frameTagLength, err)
		}

		switch {
		case frameTag == movieclipEndFrameTag:
			return clip, nil
		case movieclipFrameTags[frameTag]:
			if framesLoaded >= len(clip.Frames) {
				return nil, fmt.Errorf("too many frames in movieclip %d", id)
			}
			elementsCount, err := reader.ReadU16()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d frame %d element count: %w", id, framesLoaded, err)
			}
			frameName, err := reader.ReadASCII()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d frame %d name: %w", id, framesLoaded, err)
			}
			clip.Frames[framesLoaded].Name = frameName
			clip.Frames[framesLoaded].Elements = make([]FrameElement, 0, elementsCount)
			if frameTag == 5 {
				for range int(elementsCount) {
					bindIndex, err := reader.ReadU16()
					if err != nil {
						return nil, fmt.Errorf("movieclip %d frame %d bind: %w", id, framesLoaded, err)
					}
					matrixIndex, err := reader.ReadU16()
					if err != nil {
						return nil, fmt.Errorf("movieclip %d frame %d matrix: %w", id, framesLoaded, err)
					}
					colorIndex, err := reader.ReadU16()
					if err != nil {
						return nil, fmt.Errorf("movieclip %d frame %d color: %w", id, framesLoaded, err)
					}
					clip.Frames[framesLoaded].Elements = append(clip.Frames[framesLoaded].Elements, FrameElement{
						Bind:   bindIndex,
						Matrix: matrixIndex,
						Color:  colorIndex,
					})
				}
			} else {
				for i := 0; i < int(elementsCount); i++ {
					idx := frameElementsOffset + i
					if idx >= len(frameElements) {
						return nil, fmt.Errorf("frame element overflow in movieclip %d", id)
					}
					clip.Frames[framesLoaded].Elements = append(clip.Frames[framesLoaded].Elements, frameElements[idx])
				}
				frameElementsOffset += int(elementsCount)
			}
			framesLoaded++
		case frameTag == movieclipScalingGrid:
			clip.NineSlice = make([]float64, 4)
			for i := range clip.NineSlice {
				v, err := reader.ReadTwip()
				if err != nil {
					return nil, fmt.Errorf("movieclip %d nine-slice: %w", id, err)
				}
				clip.NineSlice[i] = v
			}
		case frameTag == movieclipMatrixBankTag:
			matrixBank, err := reader.ReadU8()
			if err != nil {
				return nil, fmt.Errorf("movieclip %d matrix bank: %w", id, err)
			}
			clip.MatrixBank = int(matrixBank)
		default:
			if err := reader.Seek(frameTagEnd); err != nil {
				return nil, err
			}
		}

		if reader.Pos() > frameTagEnd {
			return nil, fmt.Errorf("movieclip %d frame tag %d consumed past its declared end: pos=%d end=%d", id, frameTag, reader.Pos(), frameTagEnd)
		}
		if reader.Pos() < frameTagEnd {
			if err := reader.Seek(frameTagEnd); err != nil {
				return nil, err
			}
		}
	}
}
