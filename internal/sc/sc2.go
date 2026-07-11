package sc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	SC2 "sc2fla/internal/sc/sc2fb/sc/flash/SC2"
	SC2Typing "sc2fla/internal/sc/sc2fb/sc/flash/SC2/Typing"
)

type sc2State struct {
	descriptor  *SC2.FileDescriptor
	storage     *SC2.DataStorage
	payload     []byte
	mainPath    string
	version     int
	isTexture   bool
	downgraded  bool
	textureOnly bool
}

func (s *SWF) loadSC2Asset(raw []byte, path string, isTexture bool) error {
	state, err := loadSC2State(raw, path, isTexture)
	if err != nil {
		return err
	}

	if !isTexture {
		s.Filename = path
		s.ShapesCount = int(state.descriptor.ShapeCount())
		s.MovieClipsCount = int(state.descriptor.MovieClipsCount())
		s.TexturesCount = int(state.descriptor.TextureCount())
		s.TextFieldsCount = int(state.descriptor.TextFieldsCount())
		s.Textures = make([]*Texture, s.TexturesCount)
		s.MatrixBanks = nil
	}

	if err := s.loadSC2Data(state); err != nil {
		return err
	}

	return nil
}

func loadSC2State(raw []byte, path string, isTexture bool) (*sc2State, error) {
	version := DetectSCVersion(raw)
	if version < 5 {
		return nil, fmt.Errorf("%s is not an SC2 asset", path)
	}

	pos := 6
	if version == 6 {
		if len(raw) < pos+2 {
			return nil, fmt.Errorf("invalid SC2 v6 header in %s", path)
		}
		pos += 2
	}
	if len(raw) < pos+4 {
		return nil, fmt.Errorf("missing descriptor length in %s", path)
	}

	descriptorSize := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
	if descriptorSize <= 0 || len(raw) < pos+4+descriptorSize {
		return nil, fmt.Errorf("invalid SC2 descriptor size in %s", path)
	}
	descriptorBuf := raw[pos+4 : pos+4+descriptorSize]
	descriptor := SC2.GetRootAsFileDescriptor(descriptorBuf, 0)
	pos += 4 + descriptorSize

	decompressed, err := decodeSC2Payload(raw[pos:], path, int(descriptor.CompressedSize()))
	if err != nil {
		return nil, err
	}

	if len(decompressed) < 4 {
		return nil, fmt.Errorf("SC2 payload is too small in %s", path)
	}
	storageSize := int(binary.LittleEndian.Uint32(decompressed[:4]))
	if storageSize <= 0 || len(decompressed) < 4+storageSize {
		return nil, fmt.Errorf("invalid SC2 storage size in %s", path)
	}
	storage := SC2.GetRootAsDataStorage(decompressed[4:4+storageSize], 0)

	return &sc2State{
		descriptor: descriptor,
		storage:    storage,
		payload:    decompressed,
		mainPath:   path,
		version:    version,
		isTexture:  isTexture,
	}, nil
}

func (s *SWF) loadSC2Data(state *sc2State) error {
	if state.storage == nil || state.descriptor == nil {
		return fmt.Errorf("missing SC2 descriptor or storage")
	}

	if !state.isTexture {
		if err := s.loadSC2MatrixBanks(state); err != nil {
			return err
		}
	}

	resourcePos := int(state.descriptor.ResourcesOffset())
	if resourcePos < 0 || resourcePos > len(state.payload) {
		return fmt.Errorf("SC2 resources offset %d out of range", resourcePos)
	}
	reader := NewReader(state.payload)
	if err := reader.Seek(resourcePos); err != nil {
		return err
	}
	chunks, err := readSC2Chunks(reader)
	if err != nil {
		return err
	}

	if !state.isTexture {
		if err := s.loadSC2ExportNames(chunks.exportNames, state); err != nil {
			return err
		}
		if err := s.loadSC2TextFields(chunks.textFields, state); err != nil {
			return err
		}
	}
	if err := s.loadSC2Textures(chunks.textures, state); err != nil {
		return err
	}
	if !state.isTexture {
		if err := s.loadSC2Shapes(chunks.shapes, state); err != nil {
			return err
		}
		if err := s.loadSC2MovieClips(chunks.movieClips, state); err != nil {
			return err
		}
		if err := s.loadSC2Modifiers(chunks.modifiers, state); err != nil {
			return err
		}
	}

	return nil
}

type sc2Chunks struct {
	exportNames []byte
	textFields  []byte
	shapes      []byte
	movieClips  []byte
	modifiers   []byte
	textures    []byte
}

func decodeSC2Payload(raw []byte, path string, compressedSize int) ([]byte, error) {
	tryDecode := func(src []byte) ([]byte, error) {
		dec, err := zstd.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		defer dec.Close()

		decompressed, err := io.ReadAll(dec)
		if err != nil {
			if isRecoverableSC2DecodeError(err) && looksLikeSC2Payload(decompressed) {
				return decompressed, nil
			}
			return nil, err
		}
		return decompressed, nil
	}

	if compressedSize > 0 {
		if len(raw) < compressedSize {
			return nil, fmt.Errorf("invalid SC2 compressed payload size in %s", path)
		}
		decompressed, err := tryDecode(raw[:compressedSize])
		if err == nil {
			return decompressed, nil
		}
		if !strings.Contains(err.Error(), "unexpected EOF") {
			return nil, fmt.Errorf("decompress SC2 payload %s: %w", path, err)
		}
	}

	decompressed, err := tryDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("decompress SC2 payload %s: %w", path, err)
	}
	return decompressed, nil
}

func isRecoverableSC2DecodeError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "magic number mismatch") || strings.Contains(msg, "unexpected EOF")
}

func looksLikeSC2Payload(buf []byte) bool {
	if len(buf) < 8 {
		return false
	}
	storageSize := int(binary.LittleEndian.Uint32(buf[:4]))
	if storageSize <= 0 || storageSize+4 > len(buf) {
		return false
	}
	return true
}

func readSC2Chunks(reader *Reader) (sc2Chunks, error) {
	exportNames, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 export names: %w", err)
	}
	textFields, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 text fields: %w", err)
	}
	shapes, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 shapes: %w", err)
	}
	movieClips, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 movie clips: %w", err)
	}
	modifiers, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 movie clip modifiers: %w", err)
	}
	textures, err := readSC2Chunk(reader)
	if err != nil {
		return sc2Chunks{}, fmt.Errorf("SC2 textures: %w", err)
	}

	return sc2Chunks{
		exportNames: exportNames,
		textFields:  textFields,
		shapes:      shapes,
		movieClips:  movieClips,
		modifiers:   modifiers,
		textures:    textures,
	}, nil
}

func (s *SWF) loadSC2MatrixBanks(state *sc2State) error {
	if state.descriptor.ExternalMatrixBankSize() > 0 {
		return fmt.Errorf("SC2 v6 external matrix banks are not supported yet")
	}

	for i := 0; i < state.storage.MatrixBanksLength(); i++ {
		var bankData SC2.MatrixBank
		if !state.storage.MatrixBanks(&bankData, i) {
			continue
		}
		bank := &MatrixBank{Index: len(s.MatrixBanks)}
		for j := 0; j < bankData.MatricesLength(); j++ {
			var m SC2Typing.Matrix2x3
			if !bankData.Matrices(&m, j) {
				continue
			}
			bank.Matrices = append(bank.Matrices, Matrix{
				A:  float64(m.A()),
				B:  float64(m.B()),
				C:  float64(m.C()),
				D:  float64(m.D()),
				Tx: float64(m.Tx()),
				Ty: float64(m.Ty()),
			})
		}
		if bankData.MatricesLength() == 0 && bankData.HalfMatricesLength() > 0 {
			scaleDiv := sc2PrecisionDivisor(state.descriptor.ScalePrecision())
			transDiv := sc2PrecisionDivisor(state.descriptor.TranslationPrecision())
			for j := 0; j < bankData.HalfMatricesLength(); j++ {
				var m SC2Typing.HalfMatrix2x3
				if !bankData.HalfMatrices(&m, j) {
					continue
				}
				bank.Matrices = append(bank.Matrices, Matrix{
					A:  float64(m.A()) / scaleDiv,
					B:  float64(m.B()) / scaleDiv,
					C:  float64(m.C()) / scaleDiv,
					D:  float64(m.D()) / scaleDiv,
					Tx: float64(m.Tx()) / transDiv,
					Ty: float64(m.Ty()) / transDiv,
				})
			}
		}
		for j := 0; j < bankData.ColorsLength(); j++ {
			var c SC2Typing.ColorTransform
			if !bankData.Colors(&c, j) {
				continue
			}
			bank.ColorTransforms = append(bank.ColorTransforms, ColorTransform{
				RAdd: float64(c.RAdd()),
				GAdd: float64(c.GAdd()),
				BAdd: float64(c.BAdd()),
				AMul: float64(c.Alpha()) / 255,
				RMul: float64(c.RMul()) / 255,
				GMul: float64(c.GMul()) / 255,
				BMul: float64(c.BMul()) / 255,
			})
		}
		bank.MatricesCount = len(bank.Matrices)
		bank.ColorTransformsCount = len(bank.ColorTransforms)
		s.MatrixBanks = append(s.MatrixBanks, bank)
	}
	return nil
}

func sc2PrecisionDivisor(p SC2.Precision) float64 {
	switch p {
	case SC2.PrecisionTwip:
		return 20
	case SC2.PrecisionOptimized:
		return 1024
	default:
		return 1
	}
}

func (s *SWF) loadSC2ExportNames(chunk []byte, state *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	root := SC2.GetRootAsExportNames(chunk, 0)
	for i := 0; i < root.ObjectIdsLength() && i < root.NameRefIdsLength(); i++ {
		id := root.ObjectIds(i)
		name := sc2String(state.storage, root.NameRefIds(i))
		if name == "" {
			continue
		}
		s.Exports[id] = append(s.Exports[id], name)
	}
	return nil
}

func (s *SWF) loadSC2TextFields(chunk []byte, state *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	root := SC2.GetRootAsTextFields(chunk, 0)
	for i := 0; i < root.TextfieldsLength(); i++ {
		var tf SC2.TextField
		if !root.Textfields(&tf, i) {
			continue
		}
		textField := &TextField{
			ID:           tf.Id(),
			FontName:     sc2String(state.storage, tf.FontNameRefId()),
			Text:         sc2String(state.storage, tf.TextRefId()),
			FontSize:     int(tf.FontSize()),
			FontAlign:    tf.Align(),
			Bold:         tf.Styles()&byte(SC2.TextFieldStylebold) != 0,
			Italic:       tf.Styles()&byte(SC2.TextFieldStyleitalic) != 0,
			Multiline:    tf.Styles()&byte(SC2.TextFieldStyleis_multiline) != 0,
			Outline:      tf.Styles()&byte(SC2.TextFieldStylehas_outline) != 0,
			FontColor:    tf.FontColor(),
			OutlineColor: tf.OutlineColor(),
			Top:          tf.Top(),
			Bottom:       tf.Bottom(),
			Left:         tf.Left(),
			Right:        tf.Right(),
		}
		s.Resources[textField.ID] = textField
	}
	return nil
}

func (s *SWF) loadSC2Shapes(chunk []byte, state *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	root := SC2.GetRootAsShapes(chunk, 0)
	vertices := state.storage.ShapesBitmapPoinsBytes()
	for i := 0; i < root.ShapesLength(); i++ {
		var shapeData SC2.Shape
		if !root.Shapes(&shapeData, i) {
			continue
		}
		shape := &Shape{ID: shapeData.Id()}
		for j := 0; j < shapeData.CommandsLength(); j++ {
			var cmd SC2.ShapeDrawBitmapCommand
			if !shapeData.Commands(&cmd, j) {
				continue
			}
			textureIndex := int(cmd.TextureIndex())
			pointCount := int(cmd.PointsCount())
			if pointCount < 3 {
				continue
			}
			offset := int(cmd.PointsOffset())
			points := make([]shapeVertex, 0, pointCount)
			for v := 0; v < pointCount; v++ {
				base := (offset + v) * 12
				if base+12 > len(vertices) {
					break
				}
				x := math.Float32frombits(binary.LittleEndian.Uint32(vertices[base : base+4]))
				y := math.Float32frombits(binary.LittleEndian.Uint32(vertices[base+4 : base+8]))
				u := binary.LittleEndian.Uint16(vertices[base+8 : base+10])
				vv := binary.LittleEndian.Uint16(vertices[base+10 : base+12])
				points = append(points, shapeVertex{X: float64(x), Y: float64(y), U: u, V: vv})
			}
			shape.Bitmaps = append(shape.Bitmaps, triangulateShapeStrip(points, textureIndex, s.Textures)...)
		}
		s.Resources[shape.ID] = shape
	}
	return nil
}

func (s *SWF) loadSC2MovieClips(chunk []byte, state *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	if state.version == 6 {
		return s.loadSC2CompressedMovieClips(chunk, state)
	}
	root := SC2.GetRootAsMovieClips(chunk, 0)
	for i := 0; i < root.MovieclipsLength(); i++ {
		var clipData SC2.MovieClip
		if !root.Movieclips(&clipData, i) {
			continue
		}
		clip := buildSC2MovieClip(clipData.Id(), clipData.Framerate(), clipData.UnknownBool() != 0, clipData.MatrixBankIndex())
		populateSC2MovieClipCommon(&clipData, state, clip)
		frameElementsOffset := int(clipData.FrameElementsOffset())
		frameElementsCount := 0
		for _, frame := range clip.Frames {
			frameElementsCount += len(frame.Elements)
		}
		for frameIndex := range clip.Frames {
			elementCount := len(clip.Frames[frameIndex].Elements)
			for elementIndex := 0; elementIndex < elementCount; elementIndex++ {
				base := frameElementsOffset + (elementIndex * 3)
				clip.Frames[frameIndex].Elements[elementIndex] = readSC2FrameElement(state.storage, base)
			}
			frameElementsOffset += elementCount * 3
		}
		_ = frameElementsCount
		s.Resources[clip.ID] = clip
	}
	return nil
}

func (s *SWF) loadSC2CompressedMovieClips(chunk []byte, state *sc2State) error {
	if len(s.MatrixBanks) == 0 {
		return fmt.Errorf("SC2 compressed movie clips require matrix banks")
	}
	root := SC2.GetRootAsCompressedMovieClips(chunk, 0)
	for i := 0; i < root.MovieclipsLength(); i++ {
		var clipData SC2.CompressedMovieClip
		if !root.Movieclips(&clipData, i) {
			continue
		}
		clip := buildSC2MovieClip(clipData.Id(), clipData.Framerate(), clipData.UnknownBool() != 0, clipData.MatrixBankIndex())
		populateSC2CompressedMovieClipCommon(&clipData, state, clip)
		if off := clipData.FrameElementsOffset(); off != nil {
			frameElementsOffset := int(*off)
			for frameIndex := range clip.Frames {
				elementCount := len(clip.Frames[frameIndex].Elements)
				for elementIndex := 0; elementIndex < elementCount; elementIndex++ {
					base := frameElementsOffset + (elementIndex * 3)
					clip.Frames[frameIndex].Elements[elementIndex] = readSC2FrameElement(state.storage, base)
				}
				frameElementsOffset += elementCount * 3
			}
		} else {
			bankIndex := clip.MatrixBank
			if bankIndex >= len(s.MatrixBanks) {
				return fmt.Errorf("SC2 compressed movie clip %d matrix bank %d out of range", clip.ID, bankIndex)
			}
			bank := s.MatrixBanks[bankIndex]
			if len(bank.MovieClipElements) == 0 {
				return fmt.Errorf("SC2 compressed movie clip %d requires external matrix bank clip data", clip.ID)
			}
			offset := int(clipData.CompressedDataOffset())
			if err := decodeSC2CompressedClipFrames(clip, bank.MovieClipElements, offset, len(clip.Binds), len(bank.Matrices), len(bank.ColorTransforms)); err != nil {
				return fmt.Errorf("SC2 compressed movie clip %d: %w", clip.ID, err)
			}
		}
		s.Resources[clip.ID] = clip
	}
	return nil
}

func (s *SWF) loadSC2Modifiers(chunk []byte, _ *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	root := SC2.GetRootAsMovieClipModifiers(chunk, 0)
	for i := 0; i < root.ModifiersLength(); i++ {
		var mod SC2.MovieClipModifier
		if !root.Modifiers(&mod, i) {
			continue
		}
		modifier := &MovieClipModifier{ID: mod.Id(), Modifier: mod.Type()}
		s.Resources[modifier.ID] = modifier
	}
	return nil
}

func (s *SWF) loadSC2Textures(chunk []byte, state *sc2State) error {
	if len(chunk) == 0 {
		return nil
	}
	root := SC2.GetRootAsTextures(chunk, 0)
	if !state.isTexture && len(s.Textures) == 0 {
		s.Textures = make([]*Texture, root.TexturesLength())
	}
	for i := 0; i < root.TexturesLength(); i++ {
		var set SC2.TextureSet
		if !root.Textures(&set, i) {
			continue
		}
		selected := set.Highres(nil)
		if state.isTexture {
			if selected == nil {
				selected = set.Lowres(nil)
			}
		} else if selected == nil {
			selected = set.Lowres(nil)
		}
		if selected == nil {
			continue
		}
		tex, err := loadSC2TextureData(selected, state.mainPath)
		if err != nil {
			return fmt.Errorf("SC2 texture %d: %w", i, err)
		}
		if i >= len(s.Textures) {
			s.Textures = append(s.Textures, tex)
		} else {
			s.Textures[i] = tex
		}
	}
	return nil
}

func readSC2Chunk(reader *Reader) ([]byte, error) {
	size, err := reader.ReadU32()
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	return reader.Read(int(size))
}

func sc2String(storage *SC2.DataStorage, ref uint32) string {
	if storage == nil {
		return ""
	}
	if ref >= uint32(storage.StringsLength()) {
		return ""
	}
	return string(storage.Strings(int(ref)))
}

type shapeVertex struct {
	X float64
	Y float64
	U uint16
	V uint16
}

func triangulateShapeStrip(points []shapeVertex, textureIndex int, textures []*Texture) []ShapeBitmap {
	if len(points) < 3 {
		return nil
	}
	capacity := len(points) - 2
	if capacity < 0 {
		capacity = 0
	}
	result := make([]ShapeBitmap, 0, capacity)
	for i := 0; i+2 < len(points); i++ {
		tri := []shapeVertex{points[i], points[i+1], points[i+2]}
		if i%2 == 1 {
			tri[1], tri[2] = tri[2], tri[1]
		}
		if degenerateShapeTriangle(tri) {
			continue
		}
		result = append(result, shapeBitmapFromTriangle(tri, textureIndex, textures))
	}
	return result
}

func degenerateShapeTriangle(points []shapeVertex) bool {
	if len(points) != 3 {
		return true
	}
	xyArea := (points[1].X-points[0].X)*(points[2].Y-points[0].Y) -
		(points[1].Y-points[0].Y)*(points[2].X-points[0].X)
	uvArea := float64(int(points[1].U)-int(points[0].U))*float64(int(points[2].V)-int(points[0].V)) -
		float64(int(points[1].V)-int(points[0].V))*float64(int(points[2].U)-int(points[0].U))
	return math.Abs(xyArea) < 1e-8 || math.Abs(uvArea) < 1e-8
}

func shapeBitmapFromTriangle(points []shapeVertex, textureIndex int, textures []*Texture) ShapeBitmap {
	texWidth := 0.0
	texHeight := 0.0
	if textureIndex >= 0 && textureIndex < len(textures) && textures[textureIndex] != nil {
		texWidth = float64(textures[textureIndex].Width)
		texHeight = float64(textures[textureIndex].Height)
	}
	bm := ShapeBitmap{TextureIndex: textureIndex}
	for _, p := range points {
		bm.XYCoords = append(bm.XYCoords, Point{X: p.X, Y: p.Y})
		bm.UVCoords = append(bm.UVCoords, Point{
			X: math.Ceil(float64(p.U) / 65535.0 * texWidth),
			Y: math.Ceil(float64(p.V) / 65535.0 * texHeight),
		})
	}
	return bm
}

func buildSC2MovieClip(id uint16, frameRate byte, unknown bool, matrixBankIndex uint32) *MovieClip {
	return &MovieClip{
		ID:          id,
		FrameRate:   int(frameRate),
		MatrixBank:  int(matrixBankIndex),
		UnknownFlag: unknown,
	}
}

func populateSC2MovieClipCommon(data *SC2.MovieClip, state *sc2State, clip *MovieClip) {
	childrenCount := data.ChildrenIdsLength()
	clip.Binds = make([]Bind, childrenCount)
	for i := 0; i < childrenCount; i++ {
		clip.Binds[i] = Bind{ID: data.ChildrenIds(i)}
		if i < data.ChildrenNameRefIdsLength() {
			clip.Binds[i].Name = sc2String(state.storage, data.ChildrenNameRefIds(i))
		}
		if i < data.ChildrenBlendingLength() {
			if blend := sc2BlendMode(data.ChildrenBlending(i)); blend != "" {
				clip.Binds[i].Blend = blend
			}
		}
	}
	if grid := data.ScalingGridIndex(); grid != nil {
		var rect SC2Typing.Rect
		if state.storage.Rectangles(&rect, int(*grid)) {
			clip.NineSlice = []float64{float64(rect.Left()), float64(rect.Top()), float64(rect.Right()), float64(rect.Bottom())}
		}
	}
	if data.FramesLength() > 0 {
		clip.Frames = make([]MovieClipFrame, data.FramesLength())
		for i := 0; i < data.FramesLength(); i++ {
			var frame SC2.MovieClipFrame
			if !data.Frames(&frame, i) {
				continue
			}
			clip.Frames[i] = MovieClipFrame{
				Name:     sc2String(state.storage, frame.LabelRefId()),
				Elements: make([]FrameElement, int(frame.UsedTransform())),
			}
		}
	} else {
		clip.Frames = make([]MovieClipFrame, data.ShortFramesLength())
		for i := 0; i < data.ShortFramesLength(); i++ {
			var frame SC2.MovieClipShortFrame
			if !data.ShortFrames(&frame, i) {
				continue
			}
			clip.Frames[i] = MovieClipFrame{Elements: make([]FrameElement, int(frame.UsedTransform()))}
		}
	}
}

func populateSC2CompressedMovieClipCommon(data *SC2.CompressedMovieClip, state *sc2State, clip *MovieClip) {
	childrenCount := data.ChildrenIdsLength()
	clip.Binds = make([]Bind, childrenCount)
	for i := 0; i < childrenCount; i++ {
		clip.Binds[i] = Bind{ID: data.ChildrenIds(i)}
		if i < data.ChildrenNameRefIdsLength() {
			clip.Binds[i].Name = sc2String(state.storage, data.ChildrenNameRefIds(i))
		}
		if i < data.ChildrenBlendingLength() {
			if blend := sc2BlendMode(data.ChildrenBlending(i)); blend != "" {
				clip.Binds[i].Blend = blend
			}
		}
	}
	if grid := data.ScalingGridIndex(); grid != nil {
		var rect SC2Typing.Rect
		if state.storage.Rectangles(&rect, int(*grid)) {
			clip.NineSlice = []float64{float64(rect.Left()), float64(rect.Top()), float64(rect.Right()), float64(rect.Bottom())}
		}
	}
	clip.Frames = make([]MovieClipFrame, data.FramesLength())
	for i := 0; i < data.FramesLength(); i++ {
		var frame SC2.MovieClipFrame
		if !data.Frames(&frame, i) {
			continue
		}
		clip.Frames[i] = MovieClipFrame{
			Name:     sc2String(state.storage, frame.LabelRefId()),
			Elements: make([]FrameElement, int(frame.UsedTransform())),
		}
	}
}

func readSC2FrameElement(storage *SC2.DataStorage, offset int) FrameElement {
	return FrameElement{
		Bind:   storage.MovieclipsFrameElements(offset),
		Matrix: storage.MovieclipsFrameElements(offset + 1),
		Color:  storage.MovieclipsFrameElements(offset + 2),
	}
}

func sc2BlendMode(idx byte) string {
	masked := idx & 0x3F
	if int(masked) < len(blendModes) {
		return blendModes[masked]
	}
	return ""
}

func loadSC2TextureData(data *SC2.TextureData, mainPath string) (*Texture, error) {
	tex := &Texture{Width: int(data.Width()), Height: int(data.Height())}
	pixelIndex := int(data.PixelType())
	if pixelIndex >= 0 && pixelIndex < len(pixelFormats) {
		tex.PixelFormat = pixelFormats[pixelIndex]
		tex.PixelInternalFormat = pixelInternalFormats[pixelIndex]
		tex.PixelType = pixelTypes[pixelIndex]
	}
	if external := strings.TrimSpace(string(data.ExternalTexture())); external != "" {
		img, err := decodeExternalTexture(mainPath, filepath.Clean(external))
		if err != nil {
			tex.LoadError = err.Error()
			return tex, nil
		}
		tex.Image = img
		return tex, nil
	}
	if payload := data.DataBytes(); len(payload) > 0 {
		switch data.TextureFormat() {
		case SC2.TextureFormatUnk3, SC2.TextureFormatKhronosTexture:
			img, err := decodeKTXBytes(payload)
			if err != nil {
				tex.LoadError = fmt.Sprintf(
					"SC2 KTX texture decode failed: format=%s(%d) pixel_index=%d size=%dx%d payload=%d: %v",
					data.TextureFormat().String(),
					data.TextureFormat(),
					pixelIndex,
					tex.Width,
					tex.Height,
					len(payload),
					err,
				)
				return tex, nil
			}
			tex.Image = img
			return tex, nil
		default:
			if tex.PixelInternalFormat == "" {
				return tex, nil
			}
			reader := NewReader(payload)
			img, err := decodeRawTexture(reader, tex)
			if err != nil {
				expectedBytes, expectedKnown := expectedRawTextureBytes(tex)
				expectedLabel := "unknown"
				if expectedKnown {
					expectedLabel = fmt.Sprintf("%d", expectedBytes)
				}
				header := payload
				if len(header) > 16 {
					header = header[:16]
				}
				tex.LoadError = fmt.Sprintf(
					"SC2 raw texture decode failed: format=%s(%d) pixel_index=%d pixel_format=%s pixel_internal_format=%s pixel_type=%s size=%dx%d payload=%d expected=%s header=%x: %v",
					data.TextureFormat().String(),
					data.TextureFormat(),
					pixelIndex,
					tex.PixelFormat,
					tex.PixelInternalFormat,
					tex.PixelType,
					tex.Width,
					tex.Height,
					len(payload),
					expectedLabel,
					header,
					err,
				)
				return tex, nil
			}
			tex.Image = img
			return tex, nil
		}
	}
	return tex, nil
}

func decodeSC2CompressedClipFrames(clip *MovieClip, data []byte, offset int, childrenCount, matrixCount, colorCount int) error {
	if offset < 0 || offset >= len(data) {
		return fmt.Errorf("compressed clip data offset %d out of range", offset)
	}
	compressed := data[offset:]
	compressedEnd := len(compressed)
	for frameIndex := range clip.Frames {
		headerPos := 8 + frameIndex*8
		if headerPos+8 > compressedEnd {
			return fmt.Errorf("compressed frame header %d out of range", frameIndex)
		}
		frameOffset := int(int32(binary.LittleEndian.Uint32(compressed[headerPos : headerPos+4])))
		if frameOffset < 0 || frameOffset+6 > compressedEnd {
			return fmt.Errorf("compressed frame offset %d out of range", frameOffset)
		}
		startOffset := int(binary.LittleEndian.Uint16(compressed[headerPos+4 : headerPos+6]))
		endOffset := int(binary.LittleEndian.Uint16(compressed[headerPos+6 : headerPos+8]))
		elementData, err := bytesToU16Slice(compressed[frameOffset:])
		if err != nil {
			return err
		}
		startIndex := startOffset * 2
		endIndex := endOffset * 2
		if startIndex < 0 || endIndex < startIndex || endIndex > len(elementData) {
			return fmt.Errorf("compressed frame %d start/end out of range", frameIndex)
		}
		decoded, err := decodeCompressedFrameData(elementData, startIndex, endIndex)
		if err != nil {
			return err
		}
		if len(decoded)%3 != 0 {
			return fmt.Errorf("compressed frame %d decoded invalid element count", frameIndex)
		}
		clip.Frames[frameIndex].Elements = make([]FrameElement, 0, len(decoded)/3)
		for i := 0; i < len(decoded); i += 3 {
			elem := FrameElement{Bind: decoded[i], Matrix: decoded[i+1], Color: decoded[i+2]}
			if elem.Bind != 0xFFFF && int(elem.Bind) >= childrenCount {
				return fmt.Errorf("compressed frame %d bind %d out of range", frameIndex, elem.Bind)
			}
			if elem.Matrix != 0xFFFF && int(elem.Matrix) >= matrixCount {
				return fmt.Errorf("compressed frame %d matrix %d out of range", frameIndex, elem.Matrix)
			}
			if elem.Color != 0xFFFF && int(elem.Color) >= colorCount {
				return fmt.Errorf("compressed frame %d color %d out of range", frameIndex, elem.Color)
			}
			clip.Frames[frameIndex].Elements = append(clip.Frames[frameIndex].Elements, elem)
		}
	}
	return nil
}

func bytesToU16Slice(buf []byte) ([]uint16, error) {
	length := len(buf) / 2
	if length == 0 {
		return nil, nil
	}
	result := make([]uint16, length)
	for i := range result {
		base := i * 2
		result[i] = binary.LittleEndian.Uint16(buf[base : base+2])
	}
	return result, nil
}

func decodeCompressedFrameData(elementData []uint16, startIndex, endIndex int) ([]uint16, error) {
	result := make([]uint16, 0, 1024)
	v6 := 0
	if startIndex == 0 {
		if endIndex > len(elementData) {
			return nil, fmt.Errorf("compressed movieclip frame data out of bound")
		}
		return append(result, elementData[:endIndex]...), nil
	}
	if startIndex == endIndex {
		return nil, fmt.Errorf("invalid compressed movieclip frame data")
	}
	idxData := 0
	idxStart := startIndex
	idxEnd := endIndex
	for idxStart < idxEnd {
		if len(result)+6 > 65536 {
			return nil, fmt.Errorf("compressed movieclip frame data too large")
		}
		if idxStart >= len(elementData) {
			return nil, fmt.Errorf("compressed movieclip frame data out of bound")
		}
		v17 := elementData[idxStart]
		if idxData+2 >= len(elementData) {
			return nil, fmt.Errorf("compressed movieclip frame base data out of bound")
		}
		v13 := elementData[idxData]
		v14 := elementData[idxData+1]
		v16 := elementData[idxData+2]
		if (v6 & 1) != 0 {
			result = append(result, v13, v14, v16)
			idxData += 3
			v6 >>= 1
			continue
		}
		v6 >>= 1
		if (v17 & 3) != 0 {
			switch v17 & 7 {
			case 1:
				result = append(result, v13, uint16(int32(signBits(v17, 13, 3))+int32(v14)), v16)
				idxData += 3
				idxStart++
			case 2:
				result = append(result,
					v13,
					uint16(int32(signBits(v17, 4, 3))+int32(v14)),
					uint16(int32(signBits(v17, 9, 7))+int32(v16)),
				)
				idxData += 3
				idxStart++
			case 3:
				if idxStart+1 >= len(elementData) {
					return nil, fmt.Errorf("compressed movieclip frame delta out of bound")
				}
				result = append(result,
					v13,
					uint16(int32(elementData[idxStart+1])+int32(v14)),
					uint16(int32(signBits(v17, 13, 3))+int32(v16)),
				)
				idxData += 3
				idxStart += 2
			case 5:
				result = append(result, v13, v14, v16)
				idxData += 3
				idxStart++
				v6 = int(v17) >> 3
			case 6:
				idxData += 3 * int(signBits(v17, 13, 3))
				idxStart++
			case 7:
				if idxStart+2 >= len(elementData) {
					return nil, fmt.Errorf("compressed movieclip frame literal out of bound")
				}
				result = append(result,
					uint16(signBits(v17, 12, 3)),
					elementData[idxStart+1],
					elementData[idxStart+2],
				)
				idxStart += 3
				if (v17 & 0x8000) != 0 {
					idxData += 3
				}
			default:
				continue
			}
		} else {
			if idxData+5 >= len(elementData) {
				return nil, fmt.Errorf("compressed movieclip frame wide data out of bound")
			}
			result = append(result,
				v13,
				uint16(int32(signBits(v17, 7, 2))+int32(v14)),
				v16,
				elementData[idxData+3],
				uint16(int32(signBits(v17, 7, 9))+int32(elementData[idxData+4])),
				elementData[idxData+5],
			)
			idxData += 6
			idxStart++
			v6 >>= 1
		}
	}
	if idxStart == idxEnd {
		for v6 != 0 {
			if (v6 & 1) == 0 {
				return nil, fmt.Errorf("invalid compressed movieclip frame data")
			}
			if idxData+2 >= len(elementData) {
				return nil, fmt.Errorf("compressed movieclip frame tail out of bound")
			}
			result = append(result, elementData[idxData], elementData[idxData+1], elementData[idxData+2])
			idxData += 3
			v6 >>= 1
		}
	}
	return result, nil
}

func signBits(v uint16, bits uint, shift uint) int32 {
	return int32(uint32(v)<<(32-bits-shift)) >> (32 - bits)
}
