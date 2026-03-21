package render

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"sc2fla/internal/sc"
)

type Manifest struct {
	SourceSC string          `json:"source_sc"`
	AssetDir string          `json:"asset_dir"`
	Exports  []ManifestEntry `json:"exports"`
	Skipped  []SkippedEntry  `json:"skipped,omitempty"`
}

type ManifestEntry struct {
	SourceSC            string   `json:"source_sc"`
	ExportName          string   `json:"export_name"`
	OutputFile          string   `json:"output_file"`
	ResourceID          uint16   `json:"resource_id"`
	ResourceType        string   `json:"resource_type"`
	ResolvedTimelineID  uint16   `json:"resolved_timeline_id"`
	IsWrapperExport     bool     `json:"is_wrapper_export"`
	FrameCount          int      `json:"frame_count"`
	DurationMS          int      `json:"duration_ms"`
	BindLabels          []string `json:"bind_labels,omitempty"`
	FrameLabels         []string `json:"frame_labels,omitempty"`
	AncestorResourceIDs []uint16 `json:"ancestor_resource_ids"`
}

type SkippedEntry struct {
	SourceSC   string `json:"source_sc"`
	ExportName string `json:"export_name"`
	ResourceID uint16 `json:"resource_id"`
	Reason     string `json:"reason"`
}

type Target struct {
	Name             string
	ResourceID       uint16
	Resource         sc.Resource
	ResolvedTimeline uint16
	IsWrapper        bool
	AncestorIDs      []uint16
	BindLabels       []string
	FrameLabels      []string
	Duration         float64
	SelectedBind     string
}

type bitmapCacheKey struct {
	shapeID uint16
	index   int
}

type bitmapRenderable struct {
	sprite    *image.NRGBA
	transform sc.Matrix
}

type Exporter struct {
	swf         *sc.SWF
	spriteMu    sync.Mutex
	spriteCache map[bitmapCacheKey]*bitmapRenderable
	opts        ExportOptions
}

type AssetStats struct {
	Source         string
	AssetDir       string
	ExportsDir     string
	ManifestPath   string
	ParseDuration  time.Duration
	ExportDuration time.Duration
	TotalDuration  time.Duration
	Targets        int
	Outputs        int
	PNGs           int
	WEBPs          int
	Skipped        int
	SplitOutputs   int
	Frames         int
	DurationMS     int
	BytesWritten   int64
}

func NewExporter(swf *sc.SWF) *Exporter {
	return NewExporterWithOptions(swf, ExportOptions{})
}

func NewExporterWithOptions(swf *sc.SWF, opts ExportOptions) *Exporter {
	opts = normalizeExportOptions(opts)
	return &Exporter{
		swf:         swf,
		spriteCache: map[bitmapCacheKey]*bitmapRenderable{},
		opts:        opts,
	}
}

func ExportPath(inputPath, outPath string, workers int, opts ExportOptions) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if isDirectTextureInput(inputPath) {
		return exportTextureFile(inputPath, outPath, opts)
	}
	runStart := time.Now()
	var allStats []AssetStats

	info, err := os.Stat(inputPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(inputPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".sc") || strings.HasSuffix(name, "_tex.sc") {
				continue
			}
			source := filepath.Join(inputPath, name)
			assetDir := outPath
			if assetDir == "" {
				assetDir = strings.TrimSuffix(source, filepath.Ext(source)) + "_assets"
			} else {
				assetDir = filepath.Join(outPath, strings.TrimSuffix(name, filepath.Ext(name))+"_assets")
			}
			stats, err := exportSingle(source, assetDir, workers, opts)
			if err != nil {
				return err
			}
			allStats = append(allStats, stats)
			printAssetStats(stats)
		}
		printRunSummary(allStats, time.Since(runStart))
		return nil
	}

	assetDir := outPath
	if assetDir == "" {
		assetDir = strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_assets"
	}
	stats, err := exportSingle(inputPath, assetDir, workers, opts)
	if err != nil {
		return err
	}
	printAssetStats(stats)
	return nil
}

func exportSingle(source, assetDir string, workers int, opts ExportOptions) (AssetStats, error) {
	start := time.Now()
	parseStart := time.Now()
	swf, err := sc.Load(source)
	if err != nil {
		return AssetStats{}, err
	}
	parseDuration := time.Since(parseStart)
	runOpts := normalizeExportOptions(opts)
	exporter := NewExporterWithOptions(swf, runOpts)
	exportStart := time.Now()
	manifest, err := exporter.ExportAll(assetDir, workers)
	if err != nil {
		return AssetStats{}, err
	}
	exportDuration := time.Since(exportStart)
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return AssetStats{}, err
	}
	manifestPath := filepath.Join(assetDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return AssetStats{}, err
	}

	stats := AssetStats{
		Source:         source,
		AssetDir:       assetDir,
		ExportsDir:     assetDir,
		ManifestPath:   manifestPath,
		ParseDuration:  parseDuration,
		ExportDuration: exportDuration,
		TotalDuration:  time.Since(start),
		Targets:        len(manifest.Exports) + len(manifest.Skipped),
		Skipped:        len(manifest.Skipped),
	}
	for _, entry := range manifest.Exports {
		stats.Outputs++
		stats.Frames += entry.FrameCount
		stats.DurationMS += entry.DurationMS
		if strings.HasSuffix(entry.OutputFile, ".png") {
			stats.PNGs++
		}
		if strings.HasSuffix(entry.OutputFile, ".webp") {
			stats.WEBPs++
		}
		if strings.Contains(entry.ExportName, "__") || strings.Contains(entry.ExportName, "/") {
			stats.SplitOutputs++
		}
		if info, err := os.Stat(filepath.Join(stats.ExportsDir, entry.OutputFile)); err == nil {
			stats.BytesWritten += info.Size()
		}
	}
	if info, err := os.Stat(manifestPath); err == nil {
		stats.BytesWritten += info.Size()
	}
	return stats, nil
}

func printAssetStats(stats AssetStats) {
	outputRate := safeRate(stats.Outputs, stats.ExportDuration)
	targetRate := safeRate(stats.Targets, stats.TotalDuration)
	avgPerOutput := safeAverage(stats.ExportDuration, stats.Outputs)

	fmt.Printf("\nFinished %s\n", stats.Source)
	fmt.Printf("  Parse:   %s\n", stats.ParseDuration.Round(time.Millisecond))
	fmt.Printf("  Export:  %s\n", stats.ExportDuration.Round(time.Millisecond))
	fmt.Printf("  Total:   %s\n", stats.TotalDuration.Round(time.Millisecond))
	fmt.Printf("  Targets: %d\n", stats.Targets)
	fmt.Printf("  Outputs: %d (%d png, %d webp)\n", stats.Outputs, stats.PNGs, stats.WEBPs)
	fmt.Printf("  Split:   %d\n", stats.SplitOutputs)
	fmt.Printf("  Skipped: %d\n", stats.Skipped)
	fmt.Printf("  Frames:  %d\n", stats.Frames)
	fmt.Printf("  Length:  %.2fs total animation\n", float64(stats.DurationMS)/1000)
	fmt.Printf("  Rate:    %.2f outputs/s, %.2f targets/s\n", outputRate, targetRate)
	fmt.Printf("  Avg:     %s per output\n", avgPerOutput.Round(time.Millisecond))
	fmt.Printf("  Size:    %.2f MB\n", float64(stats.BytesWritten)/(1024*1024))
	fmt.Printf("  Exports: %s\n", stats.ExportsDir)
	fmt.Printf("  Manifest: %s\n", stats.ManifestPath)
}

func printRunSummary(allStats []AssetStats, total time.Duration) {
	if len(allStats) <= 1 {
		return
	}
	var targets, outputs, pngs, webps, skipped, split, frames, durationMS int
	var bytes int64
	for _, stats := range allStats {
		targets += stats.Targets
		outputs += stats.Outputs
		pngs += stats.PNGs
		webps += stats.WEBPs
		skipped += stats.Skipped
		split += stats.SplitOutputs
		frames += stats.Frames
		durationMS += stats.DurationMS
		bytes += stats.BytesWritten
	}
	fmt.Printf("\nRun Summary\n")
	fmt.Printf("  Assets:  %d\n", len(allStats))
	fmt.Printf("  Targets: %d\n", targets)
	fmt.Printf("  Outputs: %d (%d png, %d webp)\n", outputs, pngs, webps)
	fmt.Printf("  Split:   %d\n", split)
	fmt.Printf("  Skipped: %d\n", skipped)
	fmt.Printf("  Frames:  %d\n", frames)
	fmt.Printf("  Length:  %.2fs total animation\n", float64(durationMS)/1000)
	fmt.Printf("  Rate:    %.2f outputs/s, %.2f targets/s\n", safeRate(outputs, total), safeRate(targets, total))
	fmt.Printf("  Size:    %.2f MB\n", float64(bytes)/(1024*1024))
	fmt.Printf("  Total:   %s\n", total.Round(time.Millisecond))
}

func safeRate(count int, d time.Duration) float64 {
	if count <= 0 || d <= 0 {
		return 0
	}
	return float64(count) / d.Seconds()
}

func safeAverage(total time.Duration, count int) time.Duration {
	if count <= 0 || total <= 0 {
		return 0
	}
	return time.Duration(int64(total) / int64(count))
}

func (e *Exporter) ExportAll(assetDir string, workers int) (*Manifest, error) {
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return nil, err
	}

	targets, skipped := e.prepareTargets()
	fmt.Printf("Starting %s\n", e.swf.Filename)
	fmt.Printf("  Targets: %d\n", len(targets))
	fmt.Printf("  Workers: %d\n", maxInt(workers, 1))
	fmt.Printf("  Output:  %s\n", assetDir)
	nameAllocator := newNameAllocator(assetDir)
	manifest := &Manifest{
		SourceSC: e.swf.Filename,
		AssetDir: assetDir,
		Exports:  []ManifestEntry{},
		Skipped:  skipped,
	}

	type result struct {
		entry   *ManifestEntry
		skipped *SkippedEntry
		err     error
	}

	jobs := make(chan Target)
	results := make(chan result, len(targets))
	var wg sync.WaitGroup
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				entry, skip, err := e.exportTarget(target, assetDir, nameAllocator)
				results <- result{entry: entry, skipped: skip, err: err}
			}
		}()
	}

	go func() {
		for _, target := range targets {
			jobs <- target
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	processed := 0
	for res := range results {
		if res.err != nil {
			return nil, res.err
		}
		if res.skipped != nil {
			manifest.Skipped = append(manifest.Skipped, *res.skipped)
		} else if res.entry != nil {
			manifest.Exports = append(manifest.Exports, *res.entry)
		}
		processed++
		if processed == len(targets) || processed%25 == 0 {
			fmt.Printf("  Progress: %d/%d\n", processed, len(targets))
		}
	}

	sort.Slice(manifest.Exports, func(i, j int) bool {
		return manifest.Exports[i].OutputFile < manifest.Exports[j].OutputFile
	})
	sort.Slice(manifest.Skipped, func(i, j int) bool {
		if manifest.Skipped[i].ExportName == manifest.Skipped[j].ExportName {
			return manifest.Skipped[i].ResourceID < manifest.Skipped[j].ResourceID
		}
		return manifest.Skipped[i].ExportName < manifest.Skipped[j].ExportName
	})

	return manifest, nil
}

func (e *Exporter) prepareTargets() ([]Target, []SkippedEntry) {
	targets := make([]Target, 0)
	skipped := make([]SkippedEntry, 0)
	for _, resourceID := range sc.SortedExportIDs(e.swf.Exports) {
		resource := e.swf.Resources[resourceID]
		for _, exportName := range e.swf.Exports[resourceID] {
			if resource == nil {
				skipped = append(skipped, SkippedEntry{
					SourceSC:   e.swf.Filename,
					ExportName: exportName,
					ResourceID: resourceID,
					Reason:     "missing resource",
				})
				continue
			}
			target, err := e.prepareTarget(exportName, resourceID, resource)
			if err != nil {
				skipped = append(skipped, SkippedEntry{
					SourceSC:   e.swf.Filename,
					ExportName: exportName,
					ResourceID: resourceID,
					Reason:     err.Error(),
				})
				continue
			}
			targets = append(targets, target)
			targets = append(targets, e.splitBindTargets(target)...)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Name == targets[j].Name {
			return targets[i].ResourceID < targets[j].ResourceID
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, skipped
}

func (e *Exporter) splitBindTargets(target Target) []Target {
	if !shouldSplitNamedBinds(target.Name) || len(target.BindLabels) == 0 {
		return nil
	}

	splitTargets := make([]Target, 0, len(target.BindLabels))
	for _, bindLabel := range target.BindLabels {
		if !isRenderableBindLabel(bindLabel) {
			continue
		}
		splitTarget := target
		splitTarget.Name = target.Name + "/" + bindLabel
		splitTarget.SelectedBind = bindLabel
		splitTarget.BindLabels = nil
		splitTargets = append(splitTargets, splitTarget)
	}
	return splitTargets
}

func shouldSplitNamedBinds(exportName string) bool {
	switch exportName {
	case "playerhouse_parts":
		return true
	default:
		return false
	}
}

func isRenderableBindLabel(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "bounds"
}

func (e *Exporter) prepareTarget(name string, resourceID uint16, resource sc.Resource) (Target, error) {
	target := Target{
		Name:             name,
		ResourceID:       resourceID,
		Resource:         resource,
		ResolvedTimeline: resourceID,
		AncestorIDs:      []uint16{resourceID},
	}

	switch res := resource.(type) {
	case *sc.Shape:
		return target, nil
	case *sc.MovieClip:
		bindLabels, frameLabels := e.collectLabels(resourceID)
		target.BindLabels = bindLabels
		target.FrameLabels = frameLabels

		if len(res.Frames) > 1 {
			target.Duration = clipDuration(res)
			return target, nil
		}
		path, descendant, duration := e.findLongestAnimatedDescendant(resourceID)
		if descendant != 0 {
			target.IsWrapper = true
			target.ResolvedTimeline = descendant
			target.AncestorIDs = path
			target.Duration = duration
		}
		return target, nil
	default:
		return Target{}, fmt.Errorf("unsupported exported resource type %s", resource.ResourceType())
	}
}

func clipDuration(clip *sc.MovieClip) float64 {
	if clip == nil || len(clip.Frames) <= 1 {
		return 0
	}
	fps := clip.FrameRate
	if fps <= 0 {
		fps = 30
	}
	return float64(len(clip.Frames)) / float64(fps)
}

func (e *Exporter) findLongestAnimatedDescendant(rootID uint16) ([]uint16, uint16, float64) {
	type candidate struct {
		path     []uint16
		id       uint16
		duration float64
	}
	var best candidate
	var visit func(id uint16, path []uint16, seen map[uint16]bool)
	visit = func(id uint16, path []uint16, seen map[uint16]bool) {
		if seen[id] {
			return
		}
		seen[id] = true
		defer delete(seen, id)

		resource := e.swf.Resources[id]
		clip, ok := resource.(*sc.MovieClip)
		if !ok {
			return
		}
		for _, bind := range clip.Binds {
			childClip, ok := e.swf.Resources[bind.ID].(*sc.MovieClip)
			if !ok {
				continue
			}
			childPath := append(append([]uint16{}, path...), bind.ID)
			if len(childClip.Frames) > 1 {
				duration := clipDuration(childClip)
				if duration > best.duration {
					best = candidate{path: childPath, id: bind.ID, duration: duration}
				}
			}
			visit(bind.ID, childPath, seen)
		}
	}

	visit(rootID, []uint16{rootID}, map[uint16]bool{})
	return best.path, best.id, best.duration
}

func (e *Exporter) collectLabels(rootID uint16) ([]string, []string) {
	bindSet := map[string]bool{}
	frameSet := map[string]bool{}
	var visit func(id uint16, seen map[uint16]bool)
	visit = func(id uint16, seen map[uint16]bool) {
		if seen[id] {
			return
		}
		seen[id] = true
		defer delete(seen, id)

		clip, ok := e.swf.Resources[id].(*sc.MovieClip)
		if !ok {
			return
		}
		for _, bind := range clip.Binds {
			if bind.Name != "" {
				bindSet[bind.Name] = true
			}
			if _, ok := e.swf.Resources[bind.ID].(*sc.MovieClip); ok {
				visit(bind.ID, seen)
			}
		}
		for _, frame := range clip.Frames {
			if frame.Name != "" {
				frameSet[frame.Name] = true
			}
		}
	}

	visit(rootID, map[uint16]bool{})
	return sortedKeys(bindSet), sortedKeys(frameSet)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (e *Exporter) exportTarget(target Target, exportsDir string, allocator *nameAllocator) (*ManifestEntry, *SkippedEntry, error) {
	frames, durationMS, err := e.renderTarget(target)
	if err != nil {
		return nil, &SkippedEntry{
			SourceSC:   e.swf.Filename,
			ExportName: target.Name,
			ResourceID: target.ResourceID,
			Reason:     err.Error(),
		}, nil
	}
	if len(frames) == 0 {
		return nil, &SkippedEntry{
			SourceSC:   e.swf.Filename,
			ExportName: target.Name,
			ResourceID: target.ResourceID,
			Reason:     "no frames rendered",
		}, nil
	}

	fileBase := allocator.Next(target.Name, target.ResourceID)
	outputPath := filepath.Join(exportsDir, fileBase)
	entry := &ManifestEntry{
		SourceSC:            e.swf.Filename,
		ExportName:          target.Name,
		ResourceID:          target.ResourceID,
		ResourceType:        target.Resource.ResourceType(),
		ResolvedTimelineID:  target.ResolvedTimeline,
		IsWrapperExport:     target.IsWrapper,
		BindLabels:          target.BindLabels,
		FrameLabels:         target.FrameLabels,
		AncestorResourceIDs: target.AncestorIDs,
	}

	switch len(frames) {
	case 1:
		outputPath += ".png"
		file, err := os.Create(outputPath)
		if err != nil {
			return nil, nil, err
		}
		if err := png.Encode(file, frames[0].Image); err != nil {
			file.Close()
			return nil, nil, err
		}
		if err := file.Close(); err != nil {
			return nil, nil, err
		}
		entry.OutputFile = filepath.Base(outputPath)
		entry.FrameCount = 1
		entry.DurationMS = 0
	default:
		outputPath += ".webp"
		totalDuration := 0
		for _, frame := range frames {
			totalDuration += frame.DelayCS * 10
		}
		file, err := os.Create(outputPath)
		if err != nil {
			return nil, nil, err
		}
		if err := writeAnimatedWebP(file, frames); err != nil {
			file.Close()
			return nil, nil, err
		}
		if err := file.Close(); err != nil {
			return nil, nil, err
		}
		entry.OutputFile = filepath.Base(outputPath)
		entry.FrameCount = len(frames)
		if durationMS > 0 {
			entry.DurationMS = durationMS
		} else {
			entry.DurationMS = totalDuration
		}
	}

	return entry, nil, nil
}

type renderedFrame struct {
	Image   *image.NRGBA
	DelayCS int
}

func (e *Exporter) renderTarget(target Target) ([]renderedFrame, int, error) {
	switch target.Resource.(type) {
	case *sc.Shape:
		bounds, err := e.collectBounds(target, 0, nil)
		if err != nil {
			return nil, 0, err
		}
		frame, err := e.renderAt(target, 0, bounds)
		if err != nil {
			return nil, 0, err
		}
		return []renderedFrame{{Image: frame, DelayCS: 0}}, 0, nil
	case *sc.MovieClip:
		duration := target.Duration
		if duration <= 0 {
			bounds, err := e.collectBounds(target, 0, nil)
			if err != nil {
				return nil, 0, err
			}
			frame, err := e.renderAt(target, 0, bounds)
			if err != nil {
				return nil, 0, err
			}
			return []renderedFrame{{Image: frame, DelayCS: 0}}, 0, nil
		}

		changePoints := e.collectChangePoints(target, duration)
		if len(changePoints) == 0 {
			changePoints = []float64{0}
		}
		bounds, err := e.collectBounds(target, duration, changePoints)
		if err != nil {
			return nil, 0, err
		}

		rawFrames := make([]renderedFrame, 0, len(changePoints))
		totalDuration := 0
		var lastHash [20]byte
		for i, t := range changePoints {
			next := duration
			if i+1 < len(changePoints) {
				next = changePoints[i+1]
			}
			delayCS := int(math.Round((next - t) * 100))
			if delayCS <= 0 {
				delayCS = 1
			}
			img, err := e.renderAt(target, t, bounds)
			if err != nil {
				return nil, 0, err
			}
			hash := sha1.Sum(img.Pix)
			if len(rawFrames) > 0 && hash == lastHash {
				rawFrames[len(rawFrames)-1].DelayCS += delayCS
				totalDuration += delayCS * 10
				continue
			}
			rawFrames = append(rawFrames, renderedFrame{Image: img, DelayCS: delayCS})
			lastHash = hash
			totalDuration += delayCS * 10
		}
		if len(rawFrames) == 1 {
			return []renderedFrame{{Image: rawFrames[0].Image, DelayCS: 0}}, 0, nil
		}
		return rawFrames, totalDuration, nil
	default:
		return nil, 0, fmt.Errorf("unsupported resource type %s", target.Resource.ResourceType())
	}
}

func (e *Exporter) collectChangePoints(target Target, duration float64) []float64 {
	set := map[int64]bool{0: true}
	var visit func(id uint16, seen map[uint16]bool)
	visit = func(id uint16, seen map[uint16]bool) {
		if seen[id] {
			return
		}
		seen[id] = true
		defer delete(seen, id)

		clip, ok := e.swf.Resources[id].(*sc.MovieClip)
		if !ok {
			return
		}
		if len(clip.Frames) > 1 {
			fps := clip.FrameRate
			if fps <= 0 {
				fps = 30
			}
			for step := 0; ; step++ {
				t := float64(step) / float64(fps)
				if t >= duration-1e-9 {
					break
				}
				set[quantizeTime(t)] = true
			}
		}
		for _, bind := range clip.Binds {
			if _, ok := e.swf.Resources[bind.ID].(*sc.MovieClip); ok {
				visit(bind.ID, seen)
			}
		}
	}
	visit(target.ResourceID, map[uint16]bool{})
	out := make([]float64, 0, len(set))
	for q := range set {
		out = append(out, dequantizeTime(q))
	}
	sort.Float64s(out)
	return out
}

func quantizeTime(t float64) int64 {
	return int64(math.Round(t * 1_000_000))
}

func dequantizeTime(v int64) float64 {
	return float64(v) / 1_000_000
}

type renderBounds struct {
	minX float64
	minY float64
	maxX float64
	maxY float64
	set  bool
}

func (b *renderBounds) add(x, y float64) {
	if !b.set {
		b.minX, b.maxX = x, x
		b.minY, b.maxY = y, y
		b.set = true
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if y > b.maxY {
		b.maxY = y
	}
}

func (e *Exporter) collectBounds(target Target, duration float64, sampleTimes []float64) (image.Rectangle, error) {
	if len(sampleTimes) == 0 {
		sampleTimes = []float64{0}
	}
	var bounds renderBounds
	for _, t := range sampleTimes {
		err := e.visitResource(target, target.ResourceID, t, sc.IdentityMatrix(), sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, _ sc.ColorTransform) error {
			sprite, err := e.bitmapRenderable(shape, idx)
			if err != nil {
				return err
			}
			fullMatrix := matrix.Multiply(sprite.transform)
			w := float64(sprite.sprite.Bounds().Dx())
			h := float64(sprite.sprite.Bounds().Dy())
			corners := [][2]float64{{0, 0}, {w, 0}, {w, h}, {0, h}}
			for _, corner := range corners {
				x, y := fullMatrix.Apply(corner[0], corner[1])
				bounds.add(x, y)
			}
			return nil
		})
		if err != nil {
			return image.Rectangle{}, err
		}
	}

	if !bounds.set {
		return image.Rect(0, 0, 1, 1), nil
	}
	padding := 2
	return image.Rect(
		int(math.Floor(bounds.minX))-padding,
		int(math.Floor(bounds.minY))-padding,
		int(math.Ceil(bounds.maxX))+padding,
		int(math.Ceil(bounds.maxY))+padding,
	), nil
}

func (e *Exporter) renderAt(target Target, t float64, worldBounds image.Rectangle) (*image.NRGBA, error) {
	renderScale := float64(maxInt(e.opts.RenderScale, 1))
	canvasWidth := maxInt(1, int(math.Ceil(float64(worldBounds.Dx())*renderScale)))
	canvasHeight := maxInt(1, int(math.Ceil(float64(worldBounds.Dy())*renderScale)))
	canvas := image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	offset := sc.Matrix{A: 1, D: 1, Tx: float64(-worldBounds.Min.X), Ty: float64(-worldBounds.Min.Y)}
	if renderScale > 1 {
		offset = sc.Matrix{A: renderScale, D: renderScale}.Multiply(offset)
	}
	err := e.visitResource(target, target.ResourceID, t, offset, sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, colorTransform sc.ColorTransform) error {
		renderable, err := e.bitmapRenderable(shape, idx)
		if err != nil {
			return err
		}
		full := matrix.Multiply(renderable.transform)
		return drawBitmap(canvas, renderable.sprite, full, colorTransform)
	})
	if err != nil {
		return nil, err
	}
	return canvas, nil
}

func (e *Exporter) visitResource(target Target, resourceID uint16, t float64, matrix sc.Matrix, colorTransform sc.ColorTransform, seen map[uint16]int, selected bool, drawFn func(uint16, *sc.Shape, int, sc.Matrix, sc.ColorTransform) error) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		for idx := range res.Bitmaps {
			if err := drawFn(resourceID, res, idx, matrix, colorTransform); err != nil {
				return err
			}
		}
	case *sc.MovieClip:
		if seen[resourceID] > 4 {
			return nil
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()

		frame := clipFrameAt(target, resourceID, res, t)
		for _, element := range frame.Elements {
			if int(element.Bind) >= len(res.Binds) {
				continue
			}
			bind := res.Binds[element.Bind]
			child := e.swf.Resources[bind.ID]
			if _, ok := child.(*sc.MovieClipModifier); ok {
				continue
			}

			childMatrix := matrix
			if element.Matrix != 0xFFFF {
				if res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Matrix) < len(e.swf.MatrixBanks[res.MatrixBank].Matrices) {
					childMatrix = matrix.Multiply(e.swf.MatrixBanks[res.MatrixBank].Matrices[element.Matrix])
				}
			}
			childColor := colorTransform
			if element.Color != 0xFFFF {
				if res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Color) < len(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms) {
					childColor = colorTransform.Combine(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms[element.Color])
				}
			}
			childSelected := selected || bind.Name == target.SelectedBind
			if err := e.visitResource(target, bind.ID, t, childMatrix, childColor, seen, childSelected, drawFn); err != nil {
				return err
			}
		}
	case *sc.TextField:
		return nil
	case *sc.MovieClipModifier:
		return nil
	default:
		return nil
	}
	return nil
}

func clipFrameAt(target Target, resourceID uint16, clip *sc.MovieClip, t float64) sc.MovieClipFrame {
	if len(clip.Frames) == 0 {
		return sc.MovieClipFrame{}
	}
	if len(clip.Frames) == 1 {
		return clip.Frames[0]
	}
	fps := clip.FrameRate
	if fps <= 0 {
		fps = 30
	}
	idx := int(math.Floor(t*float64(fps) + 1e-9))
	idx %= len(clip.Frames)
	return clip.Frames[idx]
}

func (e *Exporter) bitmapRenderable(shape *sc.Shape, idx int) (*bitmapRenderable, error) {
	key := bitmapCacheKey{shapeID: shape.ID, index: idx}
	e.spriteMu.Lock()
	if cached, ok := e.spriteCache[key]; ok {
		e.spriteMu.Unlock()
		return cached, nil
	}
	e.spriteMu.Unlock()

	if idx < 0 || idx >= len(shape.Bitmaps) {
		return nil, fmt.Errorf("bitmap index %d out of range for shape %d", idx, shape.ID)
	}
	sprite, err := shape.Bitmaps[idx].SpriteImage(e.swf.Textures)
	if err != nil {
		return nil, err
	}
	transform, err := shape.Bitmaps[idx].LocalTransform()
	if err != nil {
		return nil, err
	}
	renderable := &bitmapRenderable{sprite: sprite, transform: transform}

	e.spriteMu.Lock()
	e.spriteCache[key] = renderable
	e.spriteMu.Unlock()
	return renderable, nil
}

func drawBitmap(dst *image.NRGBA, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform) error {
	inv, err := matrix.Inverse()
	if err != nil {
		return nil
	}

	w := float64(sprite.Bounds().Dx())
	h := float64(sprite.Bounds().Dy())
	corners := [][2]float64{{0, 0}, {w, 0}, {w, h}, {0, h}}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range corners {
		x, y := matrix.Apply(c[0], c[1])
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	left := maxInt(int(math.Floor(minX)), dst.Rect.Min.X)
	top := maxInt(int(math.Floor(minY)), dst.Rect.Min.Y)
	right := minInt(int(math.Ceil(maxX)), dst.Rect.Max.X)
	bottom := minInt(int(math.Ceil(maxY)), dst.Rect.Max.Y)
	if right <= left || bottom <= top {
		return nil
	}

	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			sx, sy := inv.Apply(float64(x)+0.5, float64(y)+0.5)
			if sx < 0 || sy < 0 || sx >= w || sy >= h {
				continue
			}
			src := sampleNearest(sprite, sx, sy)
			if src.A == 0 {
				continue
			}
			src = colorTransform.Apply(src)
			if src.A == 0 {
				continue
			}
			composeOver(dst, x, y, src)
		}
	}
	return nil
}

func sampleNearest(img *image.NRGBA, x, y float64) color.NRGBA {
	ix := int(math.Round(x - 0.5))
	iy := int(math.Round(y - 0.5))
	if ix < 0 || iy < 0 || ix >= img.Bounds().Dx() || iy >= img.Bounds().Dy() {
		return color.NRGBA{}
	}
	return img.NRGBAAt(ix, iy)
}

func composeOver(dst *image.NRGBA, x, y int, src color.NRGBA) {
	dstPx := dst.NRGBAAt(x, y)
	sa := float64(src.A) / 255.0
	da := float64(dstPx.A) / 255.0
	outA := sa + da*(1-sa)
	if outA <= 0 {
		dst.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	outR := (float64(src.R)*sa + float64(dstPx.R)*da*(1-sa)) / outA
	outG := (float64(src.G)*sa + float64(dstPx.G)*da*(1-sa)) / outA
	outB := (float64(src.B)*sa + float64(dstPx.B)*da*(1-sa)) / outA
	dst.SetNRGBA(x, y, color.NRGBA{
		R: uint8(math.Round(outR)),
		G: uint8(math.Round(outG)),
		B: uint8(math.Round(outB)),
		A: uint8(math.Round(outA * 255)),
	})
}

type nameAllocator struct {
	mu   sync.Mutex
	used map[string]int
	dir  string
}

func newNameAllocator(dir string) *nameAllocator {
	return &nameAllocator{used: map[string]int{}, dir: dir}
}

func (n *nameAllocator) Next(raw string, resourceID uint16) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	base := sanitizeFilename(raw)
	if base == "" {
		base = fmt.Sprintf("resource_%d", resourceID)
	}
	if _, ok := n.used[base]; !ok {
		n.used[base] = 1
		return base
	}

	withID := fmt.Sprintf("%s__%d", base, resourceID)
	if _, ok := n.used[withID]; !ok {
		n.used[withID] = 1
		return withID
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s__%d", withID, i)
		if _, ok := n.used[candidate]; !ok {
			n.used[candidate] = 1
			return candidate
		}
	}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	name = replacer.Replace(name)
	return strings.Trim(name, ". ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
