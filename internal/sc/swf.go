package sc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	endTag = 0

	useLowResTextureTag   = 23
	useExternalTextureTag = 26
	useUncommonTextureTag = 30
	texturePostfixesTag   = 32
	movieClipModifiersTag = 37
	matrixBankTag         = 42

	textureExtension = "_tex.sc"
)

var (
	textureTags  = map[uint8]bool{1: true, 16: true, 19: true, 24: true, 27: true, 28: true, 29: true, 34: true, 45: true, 47: true}
	shapeTags    = map[uint8]bool{2: true, 18: true}
	textTags     = map[uint8]bool{7: true, 15: true, 20: true, 21: true, 25: true, 33: true, 43: true, 44: true, 46: true}
	matrixTags   = map[uint8]bool{8: true, 36: true}
	movieTags    = map[uint8]bool{3: true, 10: true, 12: true, 14: true, 35: true, 49: true}
	modifierTags = map[uint8]bool{38: true, 39: true, 40: true}
)

func Load(path string) (*SWF, error) {
	swf := &SWF{
		Filename:              path,
		Resources:             map[uint16]Resource{},
		Exports:               map[uint16][]string{},
		MatrixBanks:           []*MatrixBank{{Index: 0}},
		HighResTexturePostfix: "_highres",
		LowResTexturePostfix:  "_lowres",
	}

	resolvedPath, cleanup, err := prepareMainAssetPath(path)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := swf.loadInternal(resolvedPath, false); err != nil {
		return nil, err
	}

	if swf.HasExternalTexture {
		base := strings.TrimSuffix(swf.Filename, filepath.Ext(swf.Filename))
		textureFile := base + textureExtension
		highResPath := base + swf.HighResTexturePostfix + textureExtension
		lowResPath := base + swf.LowResTexturePostfix + textureExtension

		switch {
		case swf.UseUncommonTexture:
			if fileExists(highResPath) {
				if err := swf.loadInternal(highResPath, true); err != nil {
					return nil, err
				}
			} else if fileExists(lowResPath) {
				if err := swf.loadInternal(lowResPath, true); err != nil {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("cannot find external texture asset for %s", swf.Filename)
			}
		default:
			if swf.UseLowResTexture && !fileExists(textureFile) && fileExists(lowResPath) {
				textureFile = lowResPath
			}
			if !fileExists(textureFile) {
				return nil, fmt.Errorf("cannot find external texture file %s", textureFile)
			}
			if err := swf.loadInternal(textureFile, true); err != nil {
				return nil, err
			}
		}
	}

	return swf, nil
}

func (s *SWF) loadInternal(path string, isTexture bool) error {
	cleanup := func() {}
	if isTexture {
		var err error
		path, cleanup, err = prepareTextureAssetPath(path)
		if err != nil {
			return err
		}
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if idx := strings.Index(string(data), "START"); idx >= 0 {
		data = data[:idx]
	}
	decompressed, err := DecompressAsset(data)
	if err != nil {
		return err
	}
	reader := NewReader(decompressed)

	if !isTexture {
		s.Filename = path
		shapesCount, err := reader.ReadU16()
		if err != nil {
			return fmt.Errorf("header shapes count in %s: %w", path, err)
		}
		movieClipsCount, err := reader.ReadU16()
		if err != nil {
			return fmt.Errorf("header movieclips count in %s: %w", path, err)
		}
		texturesCount, err := reader.ReadU16()
		if err != nil {
			return fmt.Errorf("header textures count in %s: %w", path, err)
		}
		textFieldsCount, err := reader.ReadU16()
		if err != nil {
			return fmt.Errorf("header text fields count in %s: %w", path, err)
		}

		s.ShapesCount = int(shapesCount)
		s.MovieClipsCount = int(movieClipsCount)
		s.TexturesCount = int(texturesCount)
		s.TextFieldsCount = int(textFieldsCount)

		if err := loadMatrixBankCounts(reader, s.MatrixBanks[0]); err != nil {
			return fmt.Errorf("matrix bank counts in %s: %w", path, err)
		}
		if err := reader.Skip(5); err != nil {
			return fmt.Errorf("header reserved bytes in %s: %w", path, err)
		}
		exportCount, err := reader.ReadU16()
		if err != nil {
			return fmt.Errorf("export count in %s: %w", path, err)
		}
		exportIDs := make([]uint16, exportCount)
		for i := range exportIDs {
			id, err := reader.ReadU16()
			if err != nil {
				return fmt.Errorf("export id %d/%d in %s: %w", i+1, exportCount, path, err)
			}
			exportIDs[i] = id
			s.Exports[id] = nil
		}
		for _, exportID := range exportIDs {
			name, err := reader.ReadASCII()
			if err != nil {
				return fmt.Errorf("export name for resource %d in %s: %w", exportID, path, err)
			}
			s.Exports[exportID] = append(s.Exports[exportID], name)
		}
		s.Textures = make([]*Texture, s.TexturesCount)
	}

	return s.loadTags(reader)
}

func prepareMainAssetPath(path string) (string, func(), error) {
	version, err := fileSCVersion(path)
	if err != nil {
		return "", nil, err
	}
	if version <= 4 {
		return path, func() {}, nil
	}
	return downgradeAssetBundle(path)
}

func prepareTextureAssetPath(path string) (string, func(), error) {
	version, err := fileSCVersion(path)
	if err != nil {
		return "", nil, err
	}
	if version <= 4 {
		return path, func() {}, nil
	}
	return downgradeSingleAsset(path)
}

func fileSCVersion(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if idx := strings.Index(string(raw), "START"); idx >= 0 {
		raw = raw[:idx]
	}
	return DetectSCVersion(raw), nil
}

func downgradeAssetBundle(path string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "sc-v6-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	matches, err := bundleAssets(path)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if len(matches) == 0 {
		matches = []string{path}
	}

	var mainTarget string
	copied := make([]string, 0, len(matches))
	for _, src := range matches {
		dst := filepath.Join(tempDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			cleanup()
			return "", nil, err
		}
		copied = append(copied, dst)
		if filepath.Base(src) == filepath.Base(path) {
			mainTarget = dst
		}
	}
	for _, dst := range copied {
		if !strings.EqualFold(filepath.Ext(dst), ".sc") {
			continue
		}
		version, err := fileSCVersion(dst)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if version >= 5 {
			name := filepath.Base(dst)
			if err := runBundledToolInDir(tempDir, "lib/ScDowngrade.exe", name, name); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("failed to downgrade %s: %w", filepath.Base(dst), err)
			}
		}
	}
	if mainTarget == "" {
		cleanup()
		return "", nil, fmt.Errorf("failed to prepare downgraded bundle for %s", path)
	}
	return mainTarget, cleanup, nil
}

func bundleAssets(path string) ([]string, error) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	pattern := filepath.Join(filepath.Dir(path), base+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	assets := make([]string, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		assets = append(assets, match)
	}
	sort.Strings(assets)
	return assets, nil
}

func downgradeSingleAsset(path string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "sc-v6-tex-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	dst := filepath.Join(tempDir, filepath.Base(path))
	if err := copyFile(path, dst); err != nil {
		cleanup()
		return "", nil, err
	}
	name := filepath.Base(dst)
	if err := runBundledToolInDir(tempDir, "lib/ScDowngrade.exe", name, name); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to downgrade %s: %w", filepath.Base(path), err)
	}
	return dst, cleanup, nil
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

func (s *SWF) loadTags(reader *Reader) error {
	texturesLoaded := 0
	currentBank := s.MatrixBanks[len(s.MatrixBanks)-1]
	matricesLoaded := 0
	colorTransformsLoaded := 0

	for {
		tag, err := reader.ReadU8()
		if err != nil {
			return err
		}
		tagLength, err := reader.ReadI32()
		if err != nil {
			return err
		}
		tagEnd := reader.Pos() + int(tagLength)

		switch {
		case tag == endTag:
			return nil
		case tag == useLowResTextureTag:
			s.UseLowResTexture = true
		case tag == useExternalTextureTag:
			s.HasExternalTexture = true
		case tag == useUncommonTextureTag:
			s.UseUncommonTexture = true
			s.UseLowResTexture = true
		case tag == texturePostfixesTag:
			high, err := reader.ReadASCII()
			if err != nil {
				return err
			}
			low, err := reader.ReadASCII()
			if err != nil {
				return err
			}
			s.HighResTexturePostfix = high
			s.LowResTexturePostfix = low
		case textureTags[tag]:
			if texturesLoaded >= len(s.Textures) {
				return fmt.Errorf("too many textures in %s", s.Filename)
			}
			texture, err := loadTexture(reader, tag, s.HasExternalTexture, s.Filename)
			if err != nil {
				return fmt.Errorf("texture tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			s.Textures[texturesLoaded] = texture
			texturesLoaded++
		case tag == movieClipModifiersTag:
			count, err := reader.ReadU16()
			if err != nil {
				return err
			}
			s.MovieClipModifiers = int(count)
		case modifierTags[tag]:
			modifier, err := loadMovieClipModifier(reader, tag)
			if err != nil {
				return fmt.Errorf("modifier tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			s.Resources[modifier.ID] = modifier
		case shapeTags[tag]:
			shape, err := loadShape(reader, tag, s.Textures)
			if err != nil {
				return fmt.Errorf("shape tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			s.Resources[shape.ID] = shape
		case textTags[tag]:
			textField, err := loadTextField(reader, tag)
			if err != nil {
				return fmt.Errorf("text tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			s.Resources[textField.ID] = textField
		case tag == matrixBankTag:
			bank := &MatrixBank{Index: len(s.MatrixBanks)}
			if err := loadMatrixBankCounts(reader, bank); err != nil {
				return err
			}
			s.MatrixBanks = append(s.MatrixBanks, bank)
			currentBank = bank
			matricesLoaded = 0
			colorTransformsLoaded = 0
		case matrixTags[tag]:
			matrix, err := loadMatrix(reader, tag)
			if err != nil {
				return fmt.Errorf("matrix tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			if matricesLoaded < len(currentBank.Matrices) {
				currentBank.Matrices[matricesLoaded] = matrix
			} else {
				currentBank.Matrices = append(currentBank.Matrices, matrix)
			}
			matricesLoaded++
		case tag == 9:
			colorTransform, err := loadColorTransform(reader)
			if err != nil {
				return fmt.Errorf("color transform tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			if colorTransformsLoaded < len(currentBank.ColorTransforms) {
				currentBank.ColorTransforms[colorTransformsLoaded] = colorTransform
			} else {
				currentBank.ColorTransforms = append(currentBank.ColorTransforms, colorTransform)
			}
			colorTransformsLoaded++
		case movieTags[tag]:
			movieClip, err := loadMovieClip(reader, tag)
			if err != nil {
				return fmt.Errorf("movieclip tag %d at pos %d in %s: %w", tag, reader.Pos(), s.Filename, err)
			}
			s.Resources[movieClip.ID] = movieClip
		default:
			if err := reader.Seek(tagEnd); err != nil {
				return err
			}
		}

		if reader.Pos() < tagEnd {
			if err := reader.Seek(tagEnd); err != nil {
				return err
			}
		}
	}
}

func loadMatrixBankCounts(reader *Reader, bank *MatrixBank) error {
	matricesCount, err := reader.ReadU16()
	if err != nil {
		return err
	}
	colorCount, err := reader.ReadU16()
	if err != nil {
		return err
	}
	bank.MatricesCount = int(matricesCount)
	bank.ColorTransformsCount = int(colorCount)
	bank.Matrices = make([]Matrix, int(matricesCount))
	bank.ColorTransforms = make([]ColorTransform, int(colorCount))
	return nil
}

func loadMatrix(reader *Reader, tag uint8) (Matrix, error) {
	divider := 1024.0
	if tag == 36 {
		divider = 65535.0
	}
	a, err := reader.ReadI32()
	if err != nil {
		return Matrix{}, err
	}
	b, err := reader.ReadI32()
	if err != nil {
		return Matrix{}, err
	}
	c, err := reader.ReadI32()
	if err != nil {
		return Matrix{}, err
	}
	d, err := reader.ReadI32()
	if err != nil {
		return Matrix{}, err
	}
	tx, err := reader.ReadTwip()
	if err != nil {
		return Matrix{}, err
	}
	ty, err := reader.ReadTwip()
	if err != nil {
		return Matrix{}, err
	}
	return Matrix{
		A:  float64(a) / divider,
		B:  float64(b) / divider,
		C:  float64(c) / divider,
		D:  float64(d) / divider,
		Tx: tx,
		Ty: ty,
	}, nil
}

func loadColorTransform(reader *Reader) (ColorTransform, error) {
	rAdd, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	gAdd, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	bAdd, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	aMul, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	rMul, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	gMul, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	bMul, err := reader.ReadU8()
	if err != nil {
		return ColorTransform{}, err
	}
	return ColorTransform{
		RAdd: float64(rAdd),
		GAdd: float64(gAdd),
		BAdd: float64(bAdd),
		AMul: float64(aMul) / 255.0,
		RMul: float64(rMul) / 255.0,
		GMul: float64(gMul) / 255.0,
		BMul: float64(bMul) / 255.0,
	}, nil
}

func loadTextField(reader *Reader, tag uint8) (*TextField, error) {
	id, err := reader.ReadU16()
	if err != nil {
		return nil, err
	}
	fontName, err := reader.ReadASCII()
	if err != nil {
		return nil, err
	}
	fontColor, err := reader.ReadI32()
	if err != nil {
		return nil, err
	}
	bold, err := reader.ReadBool()
	if err != nil {
		return nil, err
	}
	italic, err := reader.ReadBool()
	if err != nil {
		return nil, err
	}
	multiline, err := reader.ReadBool()
	if err != nil {
		return nil, err
	}
	isDynamic, err := reader.ReadBool()
	if err != nil {
		return nil, err
	}
	align, err := reader.ReadU8()
	if err != nil {
		return nil, err
	}
	size, err := reader.ReadU8()
	if err != nil {
		return nil, err
	}
	top, err := reader.ReadI16()
	if err != nil {
		return nil, err
	}
	bottom, err := reader.ReadI16()
	if err != nil {
		return nil, err
	}
	left, err := reader.ReadI16()
	if err != nil {
		return nil, err
	}
	right, err := reader.ReadI16()
	if err != nil {
		return nil, err
	}
	outline, err := reader.ReadBool()
	if err != nil {
		return nil, err
	}
	text, err := reader.ReadASCII()
	if err != nil {
		return nil, err
	}

	tf := &TextField{
		ID:        id,
		FontName:  fontName,
		Text:      text,
		FontSize:  int(size),
		FontAlign: align,
		Bold:      bold,
		Italic:    italic,
		Multiline: multiline,
		IsDynamic: isDynamic,
		Outline:   outline,
		FontColor: uint32(fontColor),
		Top:       top,
		Bottom:    bottom,
		Left:      left,
		Right:     right,
	}
	if tag == 7 {
		return tf, nil
	}
	if _, err := reader.ReadBool(); err != nil {
		return nil, err
	}
	if tag > 20 {
		outlineColor, err := reader.ReadI32()
		if err != nil {
			return nil, err
		}
		tf.OutlineColor = uint32(outlineColor)
	}
	if tag > 25 {
		c1, err := reader.ReadI16()
		if err != nil {
			return nil, err
		}
		tf.C1 = c1
		if _, err := reader.ReadI16(); err != nil {
			return nil, err
		}
	}
	if tag > 33 {
		c2, err := reader.ReadI16()
		if err != nil {
			return nil, err
		}
		tf.C2 = c2
	}
	if tag > 43 {
		if _, err := reader.ReadBool(); err != nil {
			return nil, err
		}
	}
	if tag > 44 {
		if _, err := reader.ReadASCII(); err != nil {
			return nil, err
		}
	}
	return tf, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func SortedExportIDs(exports map[uint16][]string) []uint16 {
	ids := make([]uint16, 0, len(exports))
	for id := range exports {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
