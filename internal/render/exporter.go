package render

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	hevcencode "sc2fla/internal/encode/hevc"
	"sc2fla/internal/sc"
)

type Manifest struct {
	SourceSC string          `json:"source_sc"`
	AssetDir string          `json:"asset_dir"`
	Exports  []ManifestEntry `json:"exports"`
	Skipped  []SkippedEntry  `json:"skipped,omitempty"`
	Profile  []TargetProfile `json:"-"`
}

type ManifestEntry struct {
	SourceSC            string         `json:"source_sc"`
	ExportName          string         `json:"export_name"`
	OutputFile          string         `json:"output_file"`
	ResourceID          uint16         `json:"resource_id"`
	ResourceType        string         `json:"resource_type"`
	ResolvedTimelineID  uint16         `json:"resolved_timeline_id"`
	IsWrapperExport     bool           `json:"is_wrapper_export"`
	FrameCount          int            `json:"frame_count"`
	DurationMS          int            `json:"duration_ms"`
	BindLabels          []string       `json:"bind_labels,omitempty"`
	FrameLabels         []string       `json:"frame_labels,omitempty"`
	FrameSegments       []FrameSegment `json:"frame_segments,omitempty"`
	AncestorResourceIDs []uint16       `json:"ancestor_resource_ids"`
}

type FrameSegment struct {
	Label      string `json:"label"`
	StartFrame int    `json:"start_frame"`
	EndFrame   int    `json:"end_frame"`
}

type FrameLabelTarget struct {
	Label      string
	ResourceID uint16
	FrameIndex int
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
	FrameSegments    []FrameSegment
	FrameLabelLookup map[string]FrameLabelTarget
	Duration         float64
	SelectedBind     string
	SelectedFrame    *FrameLabelTarget
}

type bitmapCacheKey struct {
	shapeID uint16
	index   int
}

type bitmapRenderable struct {
	sprite         *image.NRGBA
	transform      sc.Matrix
	luminanceOnce  sync.Once
	luminanceFloor int
}

type sceneryBaseImage struct {
	swf    *sc.SWF
	target Target
}

type sceneryRenderContext struct {
	exporter    *Exporter
	target      Target
	spriteCache map[bitmapCacheKey]*bitmapRenderable
	baseMatrix  sc.Matrix
	allowedMask *image.NRGBA
}

type Exporter struct {
	swf               *sc.SWF
	opts              ExportOptions
	sceneryBaseMu     sync.Mutex
	sceneryBaseCache  map[string]*sceneryBaseImage
	assetBaseOnce     sync.Once
	assetBaseExporter *Exporter
	assetBaseError    error
	cancel            <-chan struct{}
	bitmapCache       sync.Map
}

var errExportCancelled = errors.New("export cancelled")

type ParseProfile struct {
	MainPrepare    time.Duration
	MainLoad       time.Duration
	TexturePrepare time.Duration
	TextureLoad    time.Duration
}

type TargetProfile struct {
	ExportName      string
	ResourceID      uint16
	Status          string
	OutputFile      string
	Frames          int
	ChangePoints    int
	SampledSteps    int
	CanvasWidth     int
	CanvasHeight    int
	GPUFrames       int
	Encoder         string
	HardwareEncode  bool
	PrepareDuration time.Duration
	BoundsDuration  time.Duration
	RenderDuration  time.Duration
	EncodeDuration  time.Duration
	TotalDuration   time.Duration
}

type AssetStats struct {
	Source         string
	AssetDir       string
	ExportsDir     string
	ManifestPath   string
	ProfileEnabled bool
	ParseProfile   ParseProfile
	TopTargets     []TargetProfile
	ParseDuration  time.Duration
	ExportDuration time.Duration
	TotalDuration  time.Duration
	Targets        int
	Outputs        int
	PNGs           int
	WEBPs          int
	MOVs           int
	Skipped        int
	SplitOutputs   int
	Frames         int
	DurationMS     int
	BytesWritten   int64
}

type renderStep struct {
	Time    float64
	DelayMS int
}

type renderProfile struct {
	Frames          int
	ChangePoints    int
	SampledSteps    int
	CanvasWidth     int
	CanvasHeight    int
	GPUFrames       int
	PrepareDuration time.Duration
	BoundsDuration  time.Duration
	RenderDuration  time.Duration
	EncodeDuration  time.Duration
}

type stateHasher struct {
	value uint64
}

func (s *stateHasher) addFloat(v float64) {
	s.add(math.Float64bits(v))
}

func (s *stateHasher) addString(v string) {
	for i := 0; i < len(v); i++ {
		s.add(uint64(v[i]))
	}
	s.add(uint64(len(v)))
}

func NewExporter(swf *sc.SWF) *Exporter {
	return NewExporterWithOptions(swf, ExportOptions{})
}

func NewExporterWithOptions(swf *sc.SWF, opts ExportOptions) *Exporter {
	opts = normalizeExportOptions(opts)
	return &Exporter{
		swf:              swf,
		opts:             opts,
		sceneryBaseCache: map[string]*sceneryBaseImage{},
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
	swf, loadStats, err := sc.LoadWithStats(source)
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
		ProfileEnabled: runOpts.Profile,
		ParseProfile: ParseProfile{
			MainPrepare:    loadStats.MainPrepareDuration,
			MainLoad:       loadStats.MainLoadDuration,
			TexturePrepare: loadStats.TexturePrepareDuration,
			TextureLoad:    loadStats.TextureLoadDuration,
		},
		TopTargets:     topTargetProfiles(manifest.Profile, runOpts.ProfileTopN),
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
		if strings.HasSuffix(entry.OutputFile, ".mov") {
			stats.MOVs++
		}
		if strings.Contains(entry.ExportName, "__") || strings.Contains(entry.ExportName, "/") {
			stats.SplitOutputs++
		}
		if info, err := os.Stat(manifestOutputPath(stats.ExportsDir, entry.OutputFile)); err == nil {
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
	fmt.Printf("  Outputs: %d (%d png, %d webp, %d mov)\n", stats.Outputs, stats.PNGs, stats.WEBPs, stats.MOVs)
	fmt.Printf("  Split:   %d\n", stats.SplitOutputs)
	fmt.Printf("  Skipped: %d\n", stats.Skipped)
	fmt.Printf("  Frames:  %d\n", stats.Frames)
	fmt.Printf("  Length:  %.2fs total animation\n", float64(stats.DurationMS)/1000)
	fmt.Printf("  Rate:    %.2f outputs/s, %.2f targets/s\n", outputRate, targetRate)
	fmt.Printf("  Avg:     %s per output\n", avgPerOutput.Round(time.Millisecond))
	fmt.Printf("  Size:    %.2f MB\n", float64(stats.BytesWritten)/(1024*1024))
	fmt.Printf("  Exports: %s\n", stats.ExportsDir)
	fmt.Printf("  Manifest: %s\n", stats.ManifestPath)
	if stats.ProfileEnabled {
		printProfileSummary(stats)
	}
}

func printRunSummary(allStats []AssetStats, total time.Duration) {
	if len(allStats) <= 1 {
		return
	}
	var targets, outputs, pngs, webps, movs, skipped, split, frames, durationMS int
	var bytes int64
	for _, stats := range allStats {
		targets += stats.Targets
		outputs += stats.Outputs
		pngs += stats.PNGs
		webps += stats.WEBPs
		movs += stats.MOVs
		skipped += stats.Skipped
		split += stats.SplitOutputs
		frames += stats.Frames
		durationMS += stats.DurationMS
		bytes += stats.BytesWritten
	}
	fmt.Printf("\nRun Summary\n")
	fmt.Printf("  Assets:  %d\n", len(allStats))
	fmt.Printf("  Targets: %d\n", targets)
	fmt.Printf("  Outputs: %d (%d png, %d webp, %d mov)\n", outputs, pngs, webps, movs)
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

func printProfileSummary(stats AssetStats) {
	parseOther := stats.ParseDuration - stats.ParseProfile.MainPrepare - stats.ParseProfile.MainLoad - stats.ParseProfile.TexturePrepare - stats.ParseProfile.TextureLoad
	if parseOther < 0 {
		parseOther = 0
	}
	fmt.Printf("  Profile:\n")
	fmt.Printf(
		"    Parse: prepare-main=%s load-main=%s prepare-tex=%s load-tex=%s other=%s\n",
		stats.ParseProfile.MainPrepare.Round(time.Millisecond),
		stats.ParseProfile.MainLoad.Round(time.Millisecond),
		stats.ParseProfile.TexturePrepare.Round(time.Millisecond),
		stats.ParseProfile.TextureLoad.Round(time.Millisecond),
		parseOther.Round(time.Millisecond),
	)
	if len(stats.TopTargets) == 0 {
		fmt.Printf("    Slowest: none\n")
		return
	}
	fmt.Printf("    Slowest (%d):\n", len(stats.TopTargets))
	for _, profile := range stats.TopTargets {
		size := "-"
		if profile.CanvasWidth > 0 && profile.CanvasHeight > 0 {
			size = fmt.Sprintf("%dx%d", profile.CanvasWidth, profile.CanvasHeight)
		}
		encoder := profile.Encoder
		if encoder == "" {
			encoder = profile.Status
		}
		fmt.Printf(
			"      %s [%s] total=%s prepare=%s bounds=%s render=%s encode=%s encoder=%s hw=%t frames=%d gpu=%d steps=%d/%d size=%s\n",
			profile.ExportName,
			profile.Status,
			profile.TotalDuration.Round(time.Millisecond),
			profile.PrepareDuration.Round(time.Millisecond),
			profile.BoundsDuration.Round(time.Millisecond),
			profile.RenderDuration.Round(time.Millisecond),
			profile.EncodeDuration.Round(time.Millisecond),
			encoder,
			profile.HardwareEncode,
			profile.Frames,
			profile.GPUFrames,
			profile.SampledSteps,
			profile.ChangePoints,
			size,
		)
	}
}

func topTargetProfiles(profiles []TargetProfile, limit int) []TargetProfile {
	if len(profiles) == 0 || limit <= 0 {
		return nil
	}
	sorted := append([]TargetProfile(nil), profiles...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].TotalDuration == sorted[j].TotalDuration {
			if sorted[i].ExportName == sorted[j].ExportName {
				return sorted[i].ResourceID < sorted[j].ResourceID
			}
			return sorted[i].ExportName < sorted[j].ExportName
		}
		return sorted[i].TotalDuration > sorted[j].TotalDuration
	})
	if limit > len(sorted) {
		limit = len(sorted)
	}
	return sorted[:limit]
}

func (h *stateHasher) add(v uint64) {
	h.value ^= v + 0x9e3779b97f4a7c15 + (h.value << 6) + (h.value >> 2)
}

func tinyOutputReason(bounds image.Rectangle, threshold int) string {
	if threshold <= 0 {
		return ""
	}
	if bounds.Dx() <= threshold && bounds.Dy() <= threshold {
		return fmt.Sprintf("tiny output %dx%d <= %d", bounds.Dx(), bounds.Dy(), threshold)
	}
	return ""
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeOutputAlternates(outputBase, keepExtension string) error {
	for _, extension := range []string{".png", ".webp", ".mov"} {
		if extension == keepExtension {
			continue
		}
		if err := removeIfExists(outputBase + extension); err != nil {
			return err
		}
	}
	return nil
}

func (e *Exporter) ExportAll(assetDir string, workers int) (*Manifest, error) {
	targets, skipped := e.prepareRequestedTargets(e.opts.AssetNames)
	targets, skipped, err := filterRequestedTargets(targets, skipped, e.opts.AssetNames, e.swf.Filename)
	if err != nil {
		return nil, err
	}
	targets = preferFrameLabel(targets, e.opts.PreferredFrameLabel)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return nil, err
	}
	fmt.Printf("Starting %s\n", e.swf.Filename)
	fmt.Printf("  Workers: %d\n", maxInt(workers, 1))
	fmt.Printf("  Output:  %s\n", assetDir)
	nameAllocator := newNameAllocator(assetDir, e.opts.AssetOutputPaths)
	manifest := &Manifest{
		SourceSC: e.swf.Filename,
		AssetDir: assetDir,
		Exports:  []ManifestEntry{},
		Skipped:  skipped,
		Profile:  []TargetProfile{},
	}

	type result struct {
		target  Target
		entry   *ManifestEntry
		skipped *SkippedEntry
		profile TargetProfile
		err     error
	}

	jobs := make(chan Target)
	results := make(chan result, len(targets))
	done := make(chan struct{})
	e.cancel = done
	defer func() { e.cancel = nil }()
	var cancelOnce sync.Once
	cancel := func() { cancelOnce.Do(func() { close(done) }) }
	var wg sync.WaitGroup
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				if err := e.checkCancelled(); err != nil {
					results <- result{target: target, err: err}
					return
				}
				entry, skip, profile, err := e.exportTarget(target, assetDir, nameAllocator)
				results <- result{target: target, entry: entry, skipped: skip, profile: profile, err: err}
			}
		}()
	}

	go func() {
	dispatch:
		for _, target := range targets {
			select {
			case jobs <- target:
			case <-done:
				break dispatch
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	processed := 0
	var firstErr error
	for res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
				cancel()
			}
			continue
		}
		if e.opts.Profile {
			manifest.Profile = append(manifest.Profile, res.profile)
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
	if firstErr != nil {
		return nil, firstErr
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

func (e *Exporter) checkCancelled() error {
	if e == nil {
		return nil
	}
	if e.cancel != nil {
		select {
		case <-e.cancel:
			return errExportCancelled
		default:
		}
	}
	if e.opts.Context != nil {
		select {
		case <-e.opts.Context.Done():
			return fmt.Errorf("%w: %v", errExportCancelled, e.opts.Context.Err())
		default:
		}
	}
	return nil
}

func filterRequestedTargets(targets []Target, skipped []SkippedEntry, requested []string, sourceSC string) ([]Target, []SkippedEntry, error) {
	if len(requested) == 0 {
		return targets, skipped, nil
	}

	requestedSet := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		requestedSet[name] = struct{}{}
	}

	filteredTargets := make([]Target, 0, len(requested))
	filteredSkipped := make([]SkippedEntry, 0, len(requested))
	matched := make(map[string]bool, len(requested))

	for _, requestedName := range requested {
		exactMatched := false
		for _, target := range targets {
			if target.Name != requestedName {
				continue
			}
			filteredTargets = append(filteredTargets, target)
			matched[requestedName] = true
			exactMatched = true
		}
		if exactMatched {
			continue
		}

		baseName, frameLabel, ok := parseFrameLabelSelector(requestedName)
		if !ok {
			continue
		}
		for _, target := range targets {
			if target.Name != baseName {
				continue
			}
			selected, ok := target.selectFrameLabel(requestedName, frameLabel)
			if !ok {
				continue
			}
			filteredTargets = append(filteredTargets, selected)
			matched[requestedName] = true
		}
	}
	for _, entry := range skipped {
		if _, ok := requestedSet[entry.ExportName]; ok {
			filteredSkipped = append(filteredSkipped, entry)
			matched[entry.ExportName] = true
		}
	}

	missing := make([]string, 0)
	for _, name := range requested {
		if !matched[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("requested asset(s) not found in %s: %s", sourceSC, strings.Join(missing, ", "))
	}

	return filteredTargets, filteredSkipped, nil
}

func preferFrameLabel(targets []Target, label string) []Target {
	if label == "" {
		return targets
	}
	labels := strings.Split(label, ",")
	selectedTargets := make([]Target, 0, len(targets))
	for _, target := range targets {
		selected := target
		for _, candidate := range labels {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if labeled, ok := target.selectFrameLabel(target.Name, candidate); ok {
				selected = labeled
				break
			}
		}
		selectedTargets = append(selectedTargets, selected)
	}
	return selectedTargets
}

func parseFrameLabelSelector(name string) (string, string, bool) {
	baseName, frameLabel, ok := strings.Cut(name, "@")
	if !ok {
		return "", "", false
	}
	baseName = strings.TrimSpace(baseName)
	frameLabel = strings.TrimSpace(frameLabel)
	if baseName == "" || frameLabel == "" {
		return "", "", false
	}
	return baseName, frameLabel, true
}

func (t Target) selectFrameLabel(requestedName, frameLabel string) (Target, bool) {
	labelTarget, ok := t.FrameLabelLookup[frameLabel]
	if !ok {
		return Target{}, false
	}
	selected := t
	selected.Name = requestedName
	selected.Duration = 0
	selected.IsWrapper = false
	selected.ResolvedTimeline = t.ResourceID
	selected.AncestorIDs = []uint16{t.ResourceID}
	selected.FrameLabels = nil
	selected.FrameSegments = nil
	selected.FrameLabelLookup = nil
	selected.SelectedFrame = &labelTarget
	return selected, true
}

func (e *Exporter) prepareTargets() ([]Target, []SkippedEntry) {
	return e.prepareRequestedTargets(nil)
}

func (e *Exporter) prepareRequestedTargets(requested []string) ([]Target, []SkippedEntry) {
	targets := make([]Target, 0)
	skipped := make([]SkippedEntry, 0)
	for _, resourceID := range sc.SortedExportIDs(e.swf.Exports) {
		resource := e.swf.Resources[resourceID]
		for _, exportName := range e.swf.Exports[resourceID] {
			if !exportMayMatchRequest(exportName, requested) {
				continue
			}
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
			if shouldSkipUIScreenTarget(e.swf.Filename, target.Name) {
				skipped = append(skipped, SkippedEntry{
					SourceSC:   e.swf.Filename,
					ExportName: target.Name,
					ResourceID: target.ResourceID,
					Reason:     "skipped ui screen/window/popup/darkener target",
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

func exportMayMatchRequest(exportName string, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	for _, name := range requested {
		if name == exportName || strings.HasPrefix(name, exportName+"@") || strings.HasPrefix(name, exportName+"/") {
			return true
		}
	}
	return false
}

func shouldSkipUIScreenTarget(sourceSC, exportName string) bool {
	base := strings.ToLower(filepath.Base(sourceSC))
	if !strings.HasPrefix(base, "ui") {
		return false
	}
	name := strings.ToLower(exportName)
	for _, needle := range []string{"screen", "window", "popup", "darkener"} {
		if strings.Contains(name, needle) {
			return true
		}
	}
	return false
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
	if exportName == "playerhouse_parts" {
		return true
	}
	if strings.HasPrefix(exportName, "worker_building_armed_lvl") {
		return true
	}
	return false
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
		bindLabels, frameLabels, frameSegments, frameLabelLookup := e.collectLabels(resourceID)
		target.BindLabels = bindLabels
		target.FrameLabels = frameLabels
		target.FrameSegments = frameSegments
		target.FrameLabelLookup = frameLabelLookup

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

func (e *Exporter) collectLabels(rootID uint16) ([]string, []string, []FrameSegment, map[string]FrameLabelTarget) {
	bindSet := map[string]bool{}
	frameSet := map[string]bool{}
	frameLookup := map[string]FrameLabelTarget{}
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
		for idx, frame := range clip.Frames {
			if frame.Name != "" {
				frameSet[frame.Name] = true
				if _, exists := frameLookup[frame.Name]; !exists {
					frameLookup[frame.Name] = FrameLabelTarget{
						Label:      frame.Name,
						ResourceID: id,
						FrameIndex: idx,
					}
				}
			}
		}
	}

	visit(rootID, map[uint16]bool{})
	var segments []FrameSegment
	if clip, ok := e.swf.Resources[rootID].(*sc.MovieClip); ok {
		segments = frameSegments(clip)
	}
	return sortedKeys(bindSet), sortedKeys(frameSet), segments, frameLookup
}

func clipFrameIndexForLabel(clip *sc.MovieClip, label string) (int, bool) {
	if clip == nil || label == "" {
		return 0, false
	}
	for idx, frame := range clip.Frames {
		if frame.Name == label {
			return idx, true
		}
	}
	return 0, false
}

func frameSegments(clip *sc.MovieClip) []FrameSegment {
	if clip == nil || len(clip.Frames) == 0 {
		return nil
	}
	labelFrames := make([]FrameSegment, 0)
	for idx, frame := range clip.Frames {
		if frame.Name == "" {
			continue
		}
		labelFrames = append(labelFrames, FrameSegment{Label: frame.Name, StartFrame: idx})
	}
	if len(labelFrames) == 0 {
		return nil
	}
	for i := range labelFrames {
		end := len(clip.Frames)
		if i+1 < len(labelFrames) {
			end = labelFrames[i+1].StartFrame
		}
		labelFrames[i].EndFrame = end
	}
	return labelFrames
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (e *Exporter) exportTarget(target Target, exportsDir string, allocator *nameAllocator) (*ManifestEntry, *SkippedEntry, TargetProfile, error) {
	start := time.Now()
	profile := TargetProfile{
		ExportName: target.Name,
		ResourceID: target.ResourceID,
		Status:     "failed",
	}
	outputBase, entryOutputBase, err := allocator.Next(target.Name, target.ResourceID)
	if err != nil {
		return nil, nil, profile, err
	}
	outputPath := outputBase
	entry := &ManifestEntry{
		SourceSC:            e.swf.Filename,
		ExportName:          target.Name,
		ResourceID:          target.ResourceID,
		ResourceType:        target.Resource.ResourceType(),
		ResolvedTimelineID:  target.ResolvedTimeline,
		IsWrapperExport:     target.IsWrapper,
		BindLabels:          target.BindLabels,
		FrameLabels:         target.FrameLabels,
		FrameSegments:       target.FrameSegments,
		AncestorResourceIDs: target.AncestorIDs,
	}
	effectiveDuration := target.Duration
	composedScenery := false
	if scenery, sceneErr := e.newSceneryRenderContext(target); sceneErr != nil {
		return nil, nil, profile, sceneErr
	} else if scenery != nil {
		composedScenery = true
		effectiveDuration, sceneErr = sceneDuration(target, scenery)
		if sceneErr != nil {
			return nil, nil, profile, sceneErr
		}
	}

	if _, ok := target.Resource.(*sc.MovieClip); ok && effectiveDuration > 0 && !e.opts.FirstFrameOnly && !e.opts.LastFrameOnly && e.opts.FrameIndex <= 0 && !e.opts.StaticOnly {
		if composedScenery && e.opts.SceneryFormat != "webp" {
			hevcEntry, hevcSkipped, hevcProfile, hevcErr := e.exportAnimatedTargetToHEVC(target, outputBase, entryOutputBase, entry, profile, start)
			if hevcErr == nil {
				return hevcEntry, hevcSkipped, hevcProfile, nil
			}
			if errors.Is(hevcErr, errExportCancelled) {
				return nil, nil, hevcProfile, hevcErr
			}
			if e.opts.SceneryFormat == "hevc" {
				hevcProfile.TotalDuration = time.Since(start)
				return nil, nil, hevcProfile, hevcErr
			}
			if !shouldFallbackFromHEVC(e.opts.SceneryFormat, hevcErr) {
				hevcProfile.TotalDuration = time.Since(start)
				return nil, &SkippedEntry{
					SourceSC:   e.swf.Filename,
					ExportName: target.Name,
					ResourceID: target.ResourceID,
					Reason:     hevcErr.Error(),
				}, hevcProfile, nil
			}
		}
		tempFile, err := os.CreateTemp(filepath.Dir(outputBase), "."+filepath.Base(outputBase)+"-*.webp.tmp")
		if err != nil {
			return nil, nil, profile, err
		}
		tempPath := tempFile.Name()
		committed := false
		defer func() {
			if !committed {
				_ = tempFile.Close()
				_ = os.Remove(tempPath)
			}
		}()

		singleFrame, frameCount, durationMS, skipReason, renderStats, err := e.renderAnimatedTargetToWebP(target, tempFile)
		profile.PrepareDuration = renderStats.PrepareDuration
		profile.BoundsDuration = renderStats.BoundsDuration
		profile.RenderDuration = renderStats.RenderDuration
		profile.EncodeDuration = renderStats.EncodeDuration
		profile.ChangePoints = renderStats.ChangePoints
		profile.SampledSteps = renderStats.SampledSteps
		profile.Frames = renderStats.Frames
		profile.CanvasWidth = renderStats.CanvasWidth
		profile.CanvasHeight = renderStats.CanvasHeight
		profile.GPUFrames = renderStats.GPUFrames
		if err != nil {
			_ = tempFile.Close()
			profile.TotalDuration = time.Since(start)
			if errors.Is(err, errExportCancelled) {
				return nil, nil, profile, err
			}
			return nil, &SkippedEntry{
				SourceSC:   e.swf.Filename,
				ExportName: target.Name,
				ResourceID: target.ResourceID,
				Reason:     err.Error(),
			}, profile, nil
		}
		if skipReason != "" {
			_ = tempFile.Close()
			if err := removeOutputAlternates(outputBase, ""); err != nil {
				return nil, nil, profile, err
			}
			profile.Status = "skipped"
			profile.TotalDuration = time.Since(start)
			return nil, &SkippedEntry{
				SourceSC:   e.swf.Filename,
				ExportName: target.Name,
				ResourceID: target.ResourceID,
				Reason:     skipReason,
			}, profile, nil
		}
		if frameCount == 0 {
			_ = tempFile.Close()
			if err := removeOutputAlternates(outputBase, ""); err != nil {
				return nil, nil, profile, err
			}
			profile.Status = "skipped"
			profile.TotalDuration = time.Since(start)
			return nil, &SkippedEntry{
				SourceSC:   e.swf.Filename,
				ExportName: target.Name,
				ResourceID: target.ResourceID,
				Reason:     "no frames rendered",
			}, profile, nil
		}
		if err := e.checkCancelled(); err != nil {
			return nil, nil, profile, err
		}

		if frameCount == 1 {
			if err := tempFile.Close(); err != nil {
				return nil, nil, profile, err
			}
			if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
				return nil, nil, profile, err
			}
			outputPath += ".png"
			encodeStart := time.Now()
			if err := writeOutputAtomically(outputPath, func(w io.Writer) error {
				return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, singleFrame)
			}); err != nil {
				return nil, nil, profile, err
			}
			if err := removeOutputAlternates(outputBase, ".png"); err != nil {
				return nil, nil, profile, err
			}
			profile.EncodeDuration += time.Since(encodeStart)
			profile.Status = "png"
			entry.OutputFile = entryOutputBase + ".png"
			entry.FrameCount = 1
			entry.DurationMS = 0
			profile.OutputFile = entry.OutputFile
			profile.TotalDuration = time.Since(start)
			committed = true
			return entry, nil, profile, nil
		}

		outputPath += ".webp"
		if err := tempFile.Chmod(0o644); err != nil {
			return nil, nil, profile, err
		}
		if err := tempFile.Close(); err != nil {
			return nil, nil, profile, err
		}
		if err := os.Rename(tempPath, outputPath); err != nil {
			return nil, nil, profile, err
		}
		if err := removeOutputAlternates(outputBase, ".webp"); err != nil {
			return nil, nil, profile, err
		}
		profile.Status = "webp"
		entry.OutputFile = entryOutputBase + ".webp"
		entry.FrameCount = frameCount
		entry.DurationMS = durationMS
		profile.OutputFile = entry.OutputFile
		profile.TotalDuration = time.Since(start)
		committed = true
		return entry, nil, profile, nil
	}

	frames, durationMS, skipReason, renderStats, err := e.renderTarget(target)
	profile.PrepareDuration = renderStats.PrepareDuration
	profile.BoundsDuration = renderStats.BoundsDuration
	profile.RenderDuration = renderStats.RenderDuration
	profile.ChangePoints = renderStats.ChangePoints
	profile.SampledSteps = renderStats.SampledSteps
	profile.Frames = renderStats.Frames
	profile.CanvasWidth = renderStats.CanvasWidth
	profile.CanvasHeight = renderStats.CanvasHeight
	profile.GPUFrames = renderStats.GPUFrames
	if err != nil {
		profile.TotalDuration = time.Since(start)
		if errors.Is(err, errExportCancelled) {
			return nil, nil, profile, err
		}
		return nil, &SkippedEntry{
			SourceSC:   e.swf.Filename,
			ExportName: target.Name,
			ResourceID: target.ResourceID,
			Reason:     err.Error(),
		}, profile, nil
	}
	if skipReason != "" {
		if err := removeOutputAlternates(outputBase, ""); err != nil {
			return nil, nil, profile, err
		}
		profile.Status = "skipped"
		profile.TotalDuration = time.Since(start)
		return nil, &SkippedEntry{
			SourceSC:   e.swf.Filename,
			ExportName: target.Name,
			ResourceID: target.ResourceID,
			Reason:     skipReason,
		}, profile, nil
	}
	if len(frames) == 0 {
		if err := removeOutputAlternates(outputBase, ""); err != nil {
			return nil, nil, profile, err
		}
		profile.Status = "skipped"
		profile.TotalDuration = time.Since(start)
		return nil, &SkippedEntry{
			SourceSC:   e.swf.Filename,
			ExportName: target.Name,
			ResourceID: target.ResourceID,
			Reason:     "no frames rendered",
		}, profile, nil
	}
	if err := e.checkCancelled(); err != nil {
		return nil, nil, profile, err
	}

	switch len(frames) {
	case 1:
		encodeStart := time.Now()
		if e.opts.PreferWebP {
			outputPath += ".webp"
			if err := writeOutputAtomically(outputPath, func(w io.Writer) error {
				return writeStillWebP(w, frames[0].Image, e.opts)
			}); err != nil {
				return nil, nil, profile, err
			}
			profile.EncodeDuration = time.Since(encodeStart)
			profile.Status = "webp"
			entry.OutputFile = entryOutputBase + ".webp"
			entry.FrameCount = 1
			entry.DurationMS = 0
			break
		}

		outputPath += ".png"
		if err := writeOutputAtomically(outputPath, func(w io.Writer) error {
			return png.Encode(w, frames[0].Image)
		}); err != nil {
			return nil, nil, profile, err
		}
		profile.EncodeDuration = time.Since(encodeStart)
		profile.Status = "png"
		entry.OutputFile = entryOutputBase + ".png"
		entry.FrameCount = 1
		entry.DurationMS = 0
	default:
		outputPath += ".webp"
		totalDuration := 0
		for _, frame := range frames {
			totalDuration += frame.DelayMS
		}
		encodeStart := time.Now()
		if err := writeOutputAtomically(outputPath, func(w io.Writer) error {
			return writeAnimatedWebP(w, frames, e.opts)
		}); err != nil {
			return nil, nil, profile, err
		}
		profile.EncodeDuration = time.Since(encodeStart)
		profile.Status = "webp"
		entry.OutputFile = entryOutputBase + ".webp"
		entry.FrameCount = len(frames)
		if durationMS > 0 {
			entry.DurationMS = durationMS
		} else {
			entry.DurationMS = totalDuration
		}
	}
	if err := removeOutputAlternates(outputBase, filepath.Ext(entry.OutputFile)); err != nil {
		return nil, nil, profile, err
	}

	profile.OutputFile = entry.OutputFile
	profile.TotalDuration = time.Since(start)
	return entry, nil, profile, nil
}

func shouldFallbackFromHEVC(sceneryFormat string, err error) bool {
	return sceneryFormat == "auto" && errors.Is(err, hevcencode.ErrUnavailable)
}

func (e *Exporter) exportAnimatedTargetToHEVC(target Target, outputBase, entryOutputBase string, entry *ManifestEntry, profile TargetProfile, started time.Time) (*ManifestEntry, *SkippedEntry, TargetProfile, error) {
	placeholder, err := os.CreateTemp(filepath.Dir(outputBase), "."+filepath.Base(outputBase)+"-*.mov")
	if err != nil {
		return nil, nil, profile, err
	}
	stagingPath := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return nil, nil, profile, err
	}
	if err := os.Remove(stagingPath); err != nil {
		return nil, nil, profile, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(stagingPath)
		}
	}()

	singleFrame, frameCount, durationMS, skipReason, renderStats, result, err := e.renderAnimatedTargetToHEVC(target, stagingPath)
	profile.PrepareDuration = renderStats.PrepareDuration
	profile.BoundsDuration = renderStats.BoundsDuration
	profile.RenderDuration = renderStats.RenderDuration
	profile.EncodeDuration = renderStats.EncodeDuration
	profile.ChangePoints = renderStats.ChangePoints
	profile.SampledSteps = renderStats.SampledSteps
	profile.Frames = renderStats.Frames
	profile.CanvasWidth = renderStats.CanvasWidth
	profile.CanvasHeight = renderStats.CanvasHeight
	profile.GPUFrames = renderStats.GPUFrames
	profile.Encoder = "hevc-videotoolbox"
	profile.HardwareEncode = result.HardwareAccelerated
	if err != nil {
		profile.TotalDuration = time.Since(started)
		return nil, nil, profile, err
	}
	if skipReason != "" {
		if err := removeOutputAlternates(outputBase, ""); err != nil {
			return nil, nil, profile, err
		}
		profile.Status = "skipped"
		profile.TotalDuration = time.Since(started)
		return nil, &SkippedEntry{SourceSC: e.swf.Filename, ExportName: target.Name, ResourceID: target.ResourceID, Reason: skipReason}, profile, nil
	}
	if frameCount == 0 {
		if err := removeOutputAlternates(outputBase, ""); err != nil {
			return nil, nil, profile, err
		}
		profile.Status = "skipped"
		profile.TotalDuration = time.Since(started)
		return nil, &SkippedEntry{SourceSC: e.swf.Filename, ExportName: target.Name, ResourceID: target.ResourceID, Reason: "no frames rendered"}, profile, nil
	}
	if err := e.checkCancelled(); err != nil {
		return nil, nil, profile, err
	}
	if frameCount == 1 {
		outputPath := outputBase + ".png"
		encodeStart := time.Now()
		if err := writeOutputAtomically(outputPath, func(w io.Writer) error {
			return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, singleFrame)
		}); err != nil {
			return nil, nil, profile, err
		}
		if err := removeOutputAlternates(outputBase, ".png"); err != nil {
			return nil, nil, profile, err
		}
		profile.EncodeDuration += time.Since(encodeStart)
		profile.Status = "png"
		entry.OutputFile = entryOutputBase + ".png"
		entry.FrameCount = 1
		entry.DurationMS = 0
		profile.OutputFile = entry.OutputFile
		profile.TotalDuration = time.Since(started)
		committed = true
		return entry, nil, profile, nil
	}
	if int64(durationMS) != result.DurationMS || frameCount != result.Frames {
		return nil, nil, profile, fmt.Errorf("HEVC result mismatch: rendered %d frames/%dms, encoded %d frames/%dms", frameCount, durationMS, result.Frames, result.DurationMS)
	}
	if err := os.Chmod(stagingPath, 0o644); err != nil {
		return nil, nil, profile, err
	}
	outputPath := outputBase + ".mov"
	if err := os.Rename(stagingPath, outputPath); err != nil {
		return nil, nil, profile, err
	}
	committed = true
	if err := removeOutputAlternates(outputBase, ".mov"); err != nil {
		return nil, nil, profile, err
	}
	profile.Status = "mov"
	entry.OutputFile = entryOutputBase + ".mov"
	entry.FrameCount = frameCount
	entry.DurationMS = durationMS
	profile.OutputFile = entry.OutputFile
	profile.TotalDuration = time.Since(started)
	return entry, nil, profile, nil
}

type renderedFrame struct {
	Image   *image.NRGBA
	DelayMS int
}

func (e *Exporter) renderTarget(target Target) ([]renderedFrame, int, string, renderProfile, error) {
	spriteCache := map[bitmapCacheKey]*bitmapRenderable{}
	scenery, err := e.newSceneryRenderContext(target)
	if err != nil {
		return nil, 0, "", renderProfile{}, err
	}
	switch target.Resource.(type) {
	case *sc.Shape:
		boundsStart := time.Now()
		bounds, err := e.collectSceneBounds(target, 0, nil, spriteCache, scenery)
		if err != nil {
			return nil, 0, "", renderProfile{}, err
		}
		profile := renderProfile{BoundsDuration: time.Since(boundsStart), ChangePoints: 1, SampledSteps: 1}
		renderStart := time.Now()
		frame, usedGPU, err := e.renderScenePreferredAtInto(target, 0, bounds, spriteCache, scenery, nil)
		if err != nil {
			return nil, 0, "", profile, err
		}
		if usedGPU {
			profile.GPUFrames = 1
		}
		profile.RenderDuration = time.Since(renderStart)
		profile.Frames = 1
		profile.CanvasWidth = frame.Bounds().Dx()
		profile.CanvasHeight = frame.Bounds().Dy()
		if reason := tinyOutputReason(frame.Bounds(), e.opts.SkipTinyOutputThreshold); reason != "" {
			return nil, 0, reason, profile, nil
		}
		return []renderedFrame{{Image: frame, DelayMS: 0}}, 0, "", profile, nil
	case *sc.MovieClip:
		duration, err := sceneDuration(target, scenery)
		if err != nil {
			return nil, 0, "", renderProfile{}, err
		}
		clip := target.Resource.(*sc.MovieClip)
		if e.opts.StaticOnly {
			staticContainers := e.staticOnlyContainers(target)
			boundsStart := time.Now()
			bounds, err := e.collectStaticOnlyBounds(target, spriteCache, staticContainers)
			if err != nil {
				return nil, 0, "", renderProfile{}, err
			}
			profile := renderProfile{BoundsDuration: time.Since(boundsStart), ChangePoints: 1, SampledSteps: 1}
			renderStart := time.Now()
			frame, err := e.renderStaticBackdrop(target, bounds, spriteCache, staticContainers)
			if err != nil {
				return nil, 0, "", profile, err
			}
			profile.RenderDuration = time.Since(renderStart)
			profile.Frames = 1
			profile.CanvasWidth = frame.Bounds().Dx()
			profile.CanvasHeight = frame.Bounds().Dy()
			if reason := tinyOutputReason(frame.Bounds(), e.opts.SkipTinyOutputThreshold); reason != "" {
				return nil, 0, reason, profile, nil
			}
			return []renderedFrame{{Image: frame, DelayMS: 0}}, 0, "", profile, nil
		}
		if duration <= 0 || e.opts.FirstFrameOnly || e.opts.LastFrameOnly || e.opts.FrameIndex > 0 {
			renderTime := stillRenderTime(clip, duration, e.opts)
			boundsStart := time.Now()
			bounds, err := e.collectSceneBounds(target, duration, []float64{renderTime}, spriteCache, scenery)
			if err != nil {
				return nil, 0, "", renderProfile{}, err
			}
			profile := renderProfile{BoundsDuration: time.Since(boundsStart), ChangePoints: 1, SampledSteps: 1}
			renderStart := time.Now()
			frame, usedGPU, err := e.renderScenePreferredAtInto(target, renderTime, bounds, spriteCache, scenery, nil)
			if err != nil {
				return nil, 0, "", profile, err
			}
			if usedGPU {
				profile.GPUFrames = 1
			}
			profile.RenderDuration = time.Since(renderStart)
			profile.Frames = 1
			profile.CanvasWidth = frame.Bounds().Dx()
			profile.CanvasHeight = frame.Bounds().Dy()
			if reason := tinyOutputReason(frame.Bounds(), e.opts.SkipTinyOutputThreshold); reason != "" {
				return nil, 0, reason, profile, nil
			}
			return []renderedFrame{{Image: frame, DelayMS: 0}}, 0, "", profile, nil
		}

		profile := renderProfile{}
		prepareStart := time.Now()
		changePoints := e.sceneChangePoints(target, duration, scenery)
		if len(changePoints) == 0 {
			changePoints = []float64{0}
		}
		steps, err := e.collapseSceneVisualStates(target, changePoints, duration, scenery)
		if err != nil {
			return nil, 0, "", profile, err
		}
		profile.PrepareDuration = time.Since(prepareStart)
		profile.ChangePoints = len(changePoints)
		profile.SampledSteps = len(steps)
		sampleTimes := make([]float64, 0, len(steps))
		for _, step := range steps {
			sampleTimes = append(sampleTimes, step.Time)
		}
		boundsStart := time.Now()
		bounds, err := e.collectSceneBounds(target, duration, sampleTimes, spriteCache, scenery)
		if err != nil {
			return nil, 0, "", profile, err
		}
		profile.BoundsDuration = time.Since(boundsStart)
		renderScale := e.outputScale(target, bounds)
		canvasWidth := maxInt(1, int(math.Ceil(float64(bounds.Dx())*renderScale)))
		canvasHeight := maxInt(1, int(math.Ceil(float64(bounds.Dy())*renderScale)))
		var metal *metalCompositor
		if !e.opts.DisableGPU {
			metal, _ = newMetalCompositor(canvasWidth, canvasHeight)
		}
		if metal != nil {
			defer metal.close()
		}

		rawFrames := make([]renderedFrame, 0, len(steps))
		totalDuration := 0
		var lastHash [20]byte
		renderStart := time.Now()
		for _, step := range steps {
			if err := e.checkCancelled(); err != nil {
				return nil, 0, "", profile, err
			}
			var img *image.NRGBA
			if metal != nil {
				img, err = e.renderSceneMetalAtInto(target, step.Time, bounds, spriteCache, scenery, metal, nil)
				if err == nil {
					profile.GPUFrames++
				} else {
					_ = metal.close()
					metal = nil
					img, err = e.renderSceneAtInto(target, step.Time, bounds, spriteCache, scenery, nil)
				}
			} else {
				img, err = e.renderSceneAtInto(target, step.Time, bounds, spriteCache, scenery, nil)
			}
			if err != nil {
				return nil, 0, "", profile, err
			}
			hash := sha1.Sum(img.Pix)
			if len(rawFrames) > 0 && hash == lastHash {
				rawFrames[len(rawFrames)-1].DelayMS += step.DelayMS
				totalDuration += step.DelayMS
				continue
			}
			rawFrames = append(rawFrames, renderedFrame{Image: img, DelayMS: step.DelayMS})
			lastHash = hash
			totalDuration += step.DelayMS
		}
		profile.RenderDuration = time.Since(renderStart)
		if len(rawFrames) > 0 {
			profile.CanvasWidth = rawFrames[0].Image.Bounds().Dx()
			profile.CanvasHeight = rawFrames[0].Image.Bounds().Dy()
			if reason := tinyOutputReason(rawFrames[0].Image.Bounds(), e.opts.SkipTinyOutputThreshold); reason != "" {
				profile.Frames = len(rawFrames)
				return nil, 0, reason, profile, nil
			}
		}
		profile.Frames = len(rawFrames)
		if len(rawFrames) == 1 {
			return []renderedFrame{{Image: rawFrames[0].Image, DelayMS: 0}}, 0, "", profile, nil
		}
		return rawFrames, totalDuration, "", profile, nil
	default:
		return nil, 0, "", renderProfile{}, fmt.Errorf("unsupported resource type %s", target.Resource.ResourceType())
	}
}

func stillRenderTime(clip *sc.MovieClip, duration float64, opts ExportOptions) float64 {
	if opts.FrameIndex > 0 && clip != nil {
		fps := clip.FrameRate
		if fps <= 0 {
			fps = 30
		}
		renderTime := float64(opts.FrameIndex-1) / float64(fps)
		if duration > 0 && renderTime >= duration {
			return duration - 1e-6
		}
		return renderTime
	}
	if !opts.LastFrameOnly || duration <= 0 {
		return 0
	}
	lastFrameTime := duration - 1e-6
	if lastFrameTime <= 0 {
		return 0
	}
	return lastFrameTime
}

type streamedAnimationEncoder interface {
	AddFrame(image.Image, int) error
	Close() error
	Abort()
}

type streamedAnimationFactory func(width, height int) (streamedAnimationEncoder, error)

type webPAnimationStream struct {
	encoder interface {
		AddFrame(image.Image, int) error
		Close() error
		Abort()
	}
}

type hevcAnimationStream struct {
	encoder      *hevcencode.Encoder
	width        int
	height       int
	paddedWidth  int
	paddedHeight int
	padded       *image.NRGBA
	result       hevcencode.Result
}

func (s *hevcAnimationStream) AddFrame(frame image.Image, durationMS int) error {
	nrgba, ok := frame.(*image.NRGBA)
	if !ok {
		return fmt.Errorf("HEVC frame has type %T, want *image.NRGBA", frame)
	}
	encodedFrame := nrgba
	if s.width != s.paddedWidth || s.height != s.paddedHeight {
		if s.padded == nil {
			s.padded = image.NewNRGBA(image.Rect(0, 0, s.paddedWidth, s.paddedHeight))
		} else {
			clear(s.padded.Pix)
		}
		stddraw.Draw(s.padded, image.Rect(0, 0, s.width, s.height), nrgba, nrgba.Bounds().Min, stddraw.Src)
		encodedFrame = s.padded
	}
	if err := s.encoder.AddFrame(encodedFrame, durationMS); err != nil {
		return err
	}
	return nil
}

func (s *hevcAnimationStream) Close() error {
	result, err := s.encoder.Close()
	s.result = result
	return err
}

func (s *hevcAnimationStream) Abort() {
	s.encoder.Abort()
}

func (s *webPAnimationStream) AddFrame(frame image.Image, durationMS int) error {
	return s.encoder.AddFrame(frame, durationMS)
}

func (s *webPAnimationStream) Close() error {
	return s.encoder.Close()
}

func (s *webPAnimationStream) Abort() {
	s.encoder.Abort()
}

func (e *Exporter) renderAnimatedTargetToWebP(target Target, w io.Writer) (*image.NRGBA, int, int, string, renderProfile, error) {
	return e.renderAnimatedTargetToEncoder(target, func(width, height int) (streamedAnimationEncoder, error) {
		encoder, err := currentWebPEncoder.NewAnimation(w, width, height, webpOptions(e.opts))
		if err != nil {
			return nil, err
		}
		return &webPAnimationStream{encoder: encoder}, nil
	})
}

func (e *Exporter) renderAnimatedTargetToHEVC(target Target, path string) (*image.NRGBA, int, int, string, renderProfile, hevcencode.Result, error) {
	var stream *hevcAnimationStream
	singleFrame, frameCount, durationMS, skipReason, profile, err := e.renderAnimatedTargetToEncoder(target, func(width, height int) (streamedAnimationEncoder, error) {
		paddedWidth := width + width%2
		paddedHeight := height + height%2
		encoder, newErr := hevcencode.New(path, paddedWidth, paddedHeight, hevcencode.Options{
			Quality:         e.opts.HEVCQuality,
			RequireHardware: true,
		})
		if newErr != nil {
			return nil, newErr
		}
		stream = &hevcAnimationStream{
			encoder:      encoder,
			width:        width,
			height:       height,
			paddedWidth:  paddedWidth,
			paddedHeight: paddedHeight,
		}
		return stream, nil
	})
	if err != nil || stream == nil {
		return singleFrame, frameCount, durationMS, skipReason, profile, hevcencode.Result{}, err
	}
	return singleFrame, frameCount, durationMS, skipReason, profile, stream.result, nil
}

func (e *Exporter) renderAnimatedTargetToEncoder(target Target, newEncoder streamedAnimationFactory) (*image.NRGBA, int, int, string, renderProfile, error) {
	spriteCache := map[bitmapCacheKey]*bitmapRenderable{}
	profile := renderProfile{}
	scenery, err := e.newSceneryRenderContext(target)
	if err != nil {
		return nil, 0, 0, "", profile, err
	}
	duration, err := sceneDuration(target, scenery)
	if err != nil {
		return nil, 0, 0, "", profile, err
	}
	if duration <= 0 {
		return nil, 0, 0, "", profile, fmt.Errorf("renderAnimatedTargetToWebP requires animated target")
	}

	prepareStart := time.Now()
	changePoints := e.sceneChangePoints(target, duration, scenery)
	if len(changePoints) == 0 {
		changePoints = []float64{0}
	}
	steps, err := e.collapseSceneVisualStates(target, changePoints, duration, scenery)
	if err != nil {
		return nil, 0, 0, "", profile, err
	}
	profile.PrepareDuration = time.Since(prepareStart)
	profile.ChangePoints = len(changePoints)
	profile.SampledSteps = len(steps)

	sampleTimes := make([]float64, 0, len(steps))
	for _, step := range steps {
		sampleTimes = append(sampleTimes, step.Time)
	}
	boundsStart := time.Now()
	bounds, err := e.collectSceneBounds(target, duration, sampleTimes, spriteCache, scenery)
	if err != nil {
		return nil, 0, 0, "", profile, err
	}
	profile.BoundsDuration = time.Since(boundsStart)

	renderScale := e.outputScale(target, bounds)
	canvasWidth := maxInt(1, int(math.Ceil(float64(bounds.Dx())*renderScale)))
	canvasHeight := maxInt(1, int(math.Ceil(float64(bounds.Dy())*renderScale)))
	var metal *metalCompositor
	if !e.opts.DisableGPU {
		metal, err = newMetalCompositor(canvasWidth, canvasHeight)
		if err != nil {
			if !errors.Is(err, errMetalCompositorUnavailable) {
				return nil, 0, 0, "", profile, fmt.Errorf("initialize Metal compositor: %w", err)
			}
			metal = nil
		}
	}
	if metal != nil {
		defer metal.close()
	}

	var animation streamedAnimationEncoder
	animationClosed := false
	defer func() {
		if animation != nil && !animationClosed {
			animation.Abort()
		}
	}()

	var pending *image.NRGBA
	var canvas *image.NRGBA
	var pendingHash [20]byte
	pendingDelayMS := 0
	totalDurationMS := 0
	uniqueFrames := 0

	for _, step := range steps {
		if err := e.checkCancelled(); err != nil {
			return nil, 0, 0, "", profile, err
		}
		renderStart := time.Now()
		if metal != nil {
			canvas, err = e.renderSceneMetalAtInto(target, step.Time, bounds, spriteCache, scenery, metal, canvas)
			if err == nil {
				profile.GPUFrames++
			}
		} else {
			canvas, err = e.renderSceneAtInto(target, step.Time, bounds, spriteCache, scenery, canvas)
		}
		profile.RenderDuration += time.Since(renderStart)
		if err != nil {
			return nil, 0, 0, "", profile, err
		}
		if profile.CanvasWidth == 0 {
			profile.CanvasWidth = canvas.Bounds().Dx()
			profile.CanvasHeight = canvas.Bounds().Dy()
			if reason := tinyOutputReason(canvas.Bounds(), e.opts.SkipTinyOutputThreshold); reason != "" {
				return nil, 0, 0, reason, profile, nil
			}
		}

		hash := sha1.Sum(canvas.Pix)
		totalDurationMS += step.DelayMS
		if pending != nil && hash == pendingHash {
			pendingDelayMS += step.DelayMS
			continue
		}

		if pending == nil {
			pending = canvas
			canvas = nil
			pendingHash = hash
			pendingDelayMS = step.DelayMS
			uniqueFrames = 1
			continue
		}

		encodeStart := time.Now()
		if animation == nil {
			animation, err = newEncoder(pending.Bounds().Dx(), pending.Bounds().Dy())
		}
		if err == nil {
			err = animation.AddFrame(pending, pendingDelayMS)
		}
		profile.EncodeDuration += time.Since(encodeStart)
		if err != nil {
			return nil, 0, 0, "", profile, err
		}

		reusable := pending
		pending = canvas
		canvas = reusable
		pendingHash = hash
		pendingDelayMS = step.DelayMS
		uniqueFrames++
	}

	profile.Frames = uniqueFrames
	if pending == nil {
		return nil, 0, 0, "no frames rendered", profile, nil
	}
	if uniqueFrames == 1 {
		return pending, 1, totalDurationMS, "", profile, nil
	}

	encodeStart := time.Now()
	err = animation.AddFrame(pending, pendingDelayMS)
	if err == nil {
		err = animation.Close()
		animationClosed = true
	}
	profile.EncodeDuration += time.Since(encodeStart)
	if err != nil {
		return nil, 0, 0, "", profile, err
	}
	return nil, uniqueFrames, totalDurationMS, "", profile, nil
}

func writeOutputAtomically(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temp.Close()
			_ = os.Remove(tempPath)
		}
	}()
	if err := write(temp); err != nil {
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func (e *Exporter) collapseVisualStates(target Target, changePoints []float64, duration float64) ([]renderStep, error) {
	return e.collapseSceneVisualStates(target, changePoints, duration, nil)
}

func (e *Exporter) collapseSceneVisualStates(target Target, changePoints []float64, duration float64, scenery *sceneryRenderContext) ([]renderStep, error) {
	steps := make([]renderStep, 0, len(changePoints))
	var lastSignature uint64
	haveSignature := false

	for i, t := range changePoints {
		next := duration
		if i+1 < len(changePoints) {
			next = changePoints[i+1]
		}
		startMS := int(math.Round(t * 1000))
		endMS := int(math.Round(next * 1000))
		delayMS := endMS - startMS
		if delayMS <= 0 {
			delayMS = 1
		}

		signature, err := e.visualStateSignature(target, t)
		if err != nil {
			return nil, err
		}
		if scenery != nil {
			baseSignature, err := scenery.exporter.visualStateSignature(scenery.target, t)
			if err != nil {
				return nil, err
			}
			combined := &stateHasher{value: 1469598103934665603}
			combined.add(signature)
			combined.add(baseSignature)
			signature = combined.value
		}
		if haveSignature && signature == lastSignature {
			steps[len(steps)-1].DelayMS += delayMS
			continue
		}
		steps = append(steps, renderStep{Time: t, DelayMS: delayMS})
		lastSignature = signature
		haveSignature = true
	}

	return steps, nil
}

func (e *Exporter) collectChangePoints(target Target, duration float64) []float64 {
	set := map[uint64]float64{math.Float64bits(0): 0}
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
				set[math.Float64bits(t)] = t
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
	for _, t := range set {
		out = append(out, t)
	}
	sort.Float64s(out)
	return out
}

func (e *Exporter) visualStateSignature(target Target, t float64) (uint64, error) {
	hasher := &stateHasher{value: 1469598103934665603}
	if err := e.hashVisualState(target, target.ResourceID, t, sc.IdentityMatrix(), sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", hasher); err != nil {
		return 0, err
	}
	return hasher.value, nil
}

func (e *Exporter) hashVisualState(target Target, resourceID uint16, t float64, matrix sc.Matrix, colorTransform sc.ColorTransform, seen map[uint16]int, selected bool, hasher *stateHasher) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		hasher.add(1)
		hasher.add(uint64(resourceID))
		hasher.add(uint64(len(res.Bitmaps)))
		hasher.addFloat(matrix.A)
		hasher.addFloat(matrix.B)
		hasher.addFloat(matrix.C)
		hasher.addFloat(matrix.D)
		hasher.addFloat(matrix.Tx)
		hasher.addFloat(matrix.Ty)
		hasher.addFloat(colorTransform.RAdd)
		hasher.addFloat(colorTransform.GAdd)
		hasher.addFloat(colorTransform.BAdd)
		hasher.addFloat(colorTransform.AMul)
		hasher.addFloat(colorTransform.RMul)
		hasher.addFloat(colorTransform.GMul)
		hasher.addFloat(colorTransform.BMul)
	case *sc.MovieClip:
		if seen[resourceID] > 4 {
			return nil
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()

		frameIndex := targetClipFrameIndex(target, resourceID, res, t)
		if frameIndex < 0 || frameIndex >= len(res.Frames) {
			return nil
		}
		hasher.add(2)
		hasher.add(uint64(resourceID))
		if selected {
			hasher.add(1)
		} else {
			hasher.add(0)
		}

		frame := res.Frames[frameIndex]
		for _, element := range frame.Elements {
			if int(element.Bind) >= len(res.Binds) {
				continue
			}
			bind := res.Binds[element.Bind]
			child := e.swf.Resources[bind.ID]
			hasher.add(uint64(element.Bind))
			hasher.addString(bind.Blend)
			if modifier, ok := child.(*sc.MovieClipModifier); ok {
				hasher.add(3)
				hasher.add(uint64(modifier.Modifier))
				continue
			}
			childMatrix := matrix
			if bank := matrixBankForClip(e.swf, res); bank != nil {
				if int(element.Matrix) < len(bank.Matrices) {
					childMatrix = matrix.Multiply(bank.Matrices[element.Matrix])
				}
			}
			childColor := colorTransform
			if bank := matrixBankForClip(e.swf, res); bank != nil {
				if int(element.Color) < len(bank.ColorTransforms) {
					childColor = colorTransform.Combine(bank.ColorTransforms[element.Color])
				}
			}
			childSelected := selected || bind.Name == target.SelectedBind
			if err := e.hashVisualState(target, bind.ID, t, childMatrix, childColor, seen, childSelected, hasher); err != nil {
				return err
			}
		}
	case *sc.TextField:
		return nil
	case *sc.MovieClipModifier:
		return nil
	}
	return nil
}

func matrixBankForClip(swf *sc.SWF, clip *sc.MovieClip) *sc.MatrixBank {
	if clip == nil || clip.MatrixBank < 0 || clip.MatrixBank >= len(swf.MatrixBanks) {
		return nil
	}
	return swf.MatrixBanks[clip.MatrixBank]
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

func (e *Exporter) collectBounds(target Target, duration float64, sampleTimes []float64, spriteCache map[bitmapCacheKey]*bitmapRenderable) (image.Rectangle, error) {
	if len(sampleTimes) == 0 {
		sampleTimes = []float64{0}
	}
	var bounds renderBounds
	for _, t := range sampleTimes {
		err := e.visitResource(target, target.ResourceID, t, sc.IdentityMatrix(), sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", "", func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, _ sc.ColorTransform, _ string) error {
			sprite, err := e.bitmapRenderable(shape, idx, spriteCache)
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

func (e *Exporter) renderAt(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable) (*image.NRGBA, error) {
	baseName := e.opts.AssetBaseNames[target.Name]
	if baseName != "" {
		baseExporter, err := e.assetBaseRenderer()
		if err != nil {
			return nil, err
		}
		baseID, baseResource := findExportedResource(baseExporter.swf, baseName)
		if baseResource == nil {
			return nil, fmt.Errorf("base asset %q for %q not found", baseName, target.Name)
		}
		baseTarget, err := baseExporter.prepareTarget(baseName, baseID, baseResource)
		if err != nil {
			return nil, err
		}
		baseCache := spriteCache
		if baseExporter != e {
			baseCache = map[bitmapCacheKey]*bitmapRenderable{}
		}
		baseBounds, err := baseExporter.collectBounds(baseTarget, 0, nil, baseCache)
		if err != nil {
			return nil, err
		}
		baseFrame, err := baseExporter.renderAtInto(baseTarget, 0, baseBounds, baseCache, nil)
		if err != nil {
			return nil, err
		}
		mainFrame, err := e.renderAtInto(target, t, worldBounds, spriteCache, nil)
		if err != nil {
			return nil, err
		}
		return compositeBuildingBase(baseName, baseFrame, mainFrame), nil
	}
	return e.renderAtInto(target, t, worldBounds, spriteCache, nil)
}

func compositeBuildingBase(baseName string, baseFrame, mainFrame *image.NRGBA) *image.NRGBA {
	if !strings.HasPrefix(baseName, "fireplace_") {
		width := maxInt(baseFrame.Bounds().Dx(), mainFrame.Bounds().Dx())
		height := maxInt(baseFrame.Bounds().Dy(), mainFrame.Bounds().Dy())
		canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
		drawFrameAt(canvas, baseFrame, (width-baseFrame.Bounds().Dx())/2, height-baseFrame.Bounds().Dy())
		drawFrameAt(canvas, mainFrame, (width-mainFrame.Bounds().Dx())/2, height-mainFrame.Bounds().Dy())
		return canvas
	}
	mainAnchorX, mainAnchorY := alphaCentroid(mainFrame, 0)
	baseAnchorX, baseAnchorY := alphaCentroid(baseFrame, 0)
	baseX := int(math.Round(mainAnchorX - baseAnchorX))
	baseY := int(math.Round(mainAnchorY - baseAnchorY))
	minX := minInt(0, baseX)
	minY := minInt(0, baseY)
	maxX := maxInt(mainFrame.Bounds().Dx(), baseX+baseFrame.Bounds().Dx())
	maxY := maxInt(mainFrame.Bounds().Dy(), baseY+baseFrame.Bounds().Dy())
	width := maxX - minX
	height := maxY - minY
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	drawFrameAt(canvas, baseFrame, baseX-minX, baseY-minY)
	drawFrameAt(canvas, mainFrame, -minX, -minY)
	return canvas
}

func alphaCentroid(frame *image.NRGBA, minimumYFraction float64) (float64, float64) {
	minimumY := int(math.Floor(float64(frame.Bounds().Dy()) * minimumYFraction))
	var weightedX, weightedY, totalAlpha float64
	for y := minimumY; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			alpha := float64(frame.NRGBAAt(frame.Bounds().Min.X+x, frame.Bounds().Min.Y+y).A)
			weightedX += float64(x) * alpha
			weightedY += float64(y) * alpha
			totalAlpha += alpha
		}
	}
	if totalAlpha == 0 {
		return float64(frame.Bounds().Dx()-1) / 2, float64(frame.Bounds().Dy()-1) / 2
	}
	return weightedX / totalAlpha, weightedY / totalAlpha
}

func drawFrameAt(dst, src *image.NRGBA, offsetX, offsetY int) {
	for y := 0; y < src.Bounds().Dy(); y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			pixel := src.NRGBAAt(src.Bounds().Min.X+x, src.Bounds().Min.Y+y)
			if pixel.A != 0 {
				composeOver(dst, offsetX+x, offsetY+y, pixel)
			}
		}
	}
}

func (e *Exporter) assetBaseRenderer() (*Exporter, error) {
	if e.opts.BaseSCPath == "" {
		return e, nil
	}
	e.assetBaseOnce.Do(func() {
		baseSWF, err := sc.Load(e.opts.BaseSCPath)
		if err != nil {
			e.assetBaseError = err
			return
		}
		baseOptions := e.opts
		baseOptions.AssetBaseNames = nil
		baseOptions.BaseSCPath = ""
		e.assetBaseExporter = NewExporterWithOptions(baseSWF, baseOptions)
	})
	if e.assetBaseError != nil {
		return nil, e.assetBaseError
	}
	return e.assetBaseExporter, nil
}

func (e *Exporter) renderAtInto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, canvas *image.NRGBA) (*image.NRGBA, error) {
	return e.renderResourceAtInto(target, t, worldBounds, spriteCache, canvas, true, e.outputScale(target, worldBounds), sc.IdentityMatrix())
}

func (e *Exporter) renderOnto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, canvas *image.NRGBA) (*image.NRGBA, error) {
	return e.renderResourceAtInto(target, t, worldBounds, spriteCache, canvas, false, e.outputScale(target, worldBounds), sc.IdentityMatrix())
}

func (e *Exporter) outputScale(target Target, worldBounds image.Rectangle) float64 {
	renderScale := float64(maxInt(e.opts.RenderScale, 1))
	if target.Name == "Player_Background" && e.opts.SceneryMaxDimension > 0 {
		longestEdge := maxInt(worldBounds.Dx(), worldBounds.Dy())
		if longestEdge > 0 {
			maximumScale := float64(e.opts.SceneryMaxDimension) / float64(longestEdge)
			if maximumScale < renderScale {
				renderScale = maximumScale
			}
		}
	}
	return renderScale
}

func (e *Exporter) renderResourceAtInto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, canvas *image.NRGBA, clearCanvas bool, renderScale float64, rootMatrix sc.Matrix) (*image.NRGBA, error) {
	return e.renderResourceAtIntoMasked(target, t, worldBounds, spriteCache, canvas, clearCanvas, renderScale, rootMatrix, nil)
}

func (e *Exporter) renderResourceAtIntoMasked(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, canvas *image.NRGBA, clearCanvas bool, renderScale float64, rootMatrix sc.Matrix, alphaMask *image.NRGBA) (*image.NRGBA, error) {
	canvasWidth := maxInt(1, int(math.Ceil(float64(worldBounds.Dx())*renderScale)))
	canvasHeight := maxInt(1, int(math.Ceil(float64(worldBounds.Dy())*renderScale)))
	if canvas == nil || canvas.Bounds().Dx() != canvasWidth || canvas.Bounds().Dy() != canvasHeight {
		canvas = image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	} else if clearCanvas {
		clear(canvas.Pix)
	}
	offset := sc.Matrix{A: 1, D: 1, Tx: float64(-worldBounds.Min.X), Ty: float64(-worldBounds.Min.Y)}
	if renderScale != 1 {
		offset = sc.Matrix{A: renderScale, D: renderScale}.Multiply(offset)
	}
	offset = offset.Multiply(rootMatrix)
	err := e.renderResourceWithMasks(target, target.ResourceID, t, offset, sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", "", canvas, spriteCache, alphaMask)
	if err != nil {
		return nil, err
	}
	return canvas, nil
}

func (e *Exporter) renderResourceWithMasks(
	target Target,
	resourceID uint16,
	t float64,
	matrix sc.Matrix,
	colorTransform sc.ColorTransform,
	seen map[uint16]int,
	selected bool,
	blend string,
	canvas *image.NRGBA,
	spriteCache map[bitmapCacheKey]*bitmapRenderable,
	alphaMask *image.NRGBA,
) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		for idx := range res.Bitmaps {
			renderable, err := e.bitmapRenderable(res, idx, spriteCache)
			if err != nil {
				return err
			}
			full := matrix.Multiply(renderable.transform)
			if err := drawBitmapMaskedWithFloor(canvas, renderable.sprite, full, colorTransform, blend, alphaMask, target.Name == "bonus_gembox", renderable.blendLuminanceFloor(blend)); err != nil {
				return err
			}
		}
		return nil
	case *sc.MovieClip:
		if seen[resourceID] > 4 {
			return nil
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()

		frame := clipFrameAt(target, resourceID, res, t)
		maskMode := uint8(0)
		var maskCanvas *image.NRGBA
		var contentMask *image.NRGBA
		for _, element := range frame.Elements {
			if int(element.Bind) >= len(res.Binds) {
				continue
			}
			bind := res.Binds[element.Bind]
			child := e.swf.Resources[bind.ID]
			if modifier, ok := child.(*sc.MovieClipModifier); ok {
				switch modifier.Modifier {
				case 38:
					maskMode = 1
					maskCanvas = image.NewNRGBA(canvas.Bounds())
					contentMask = nil
				case 39:
					maskMode = 2
					contentMask = combineAlphaMasks(maskCanvas, alphaMask)
				case 40:
					maskMode = 0
					maskCanvas = nil
					contentMask = nil
				}
				continue
			}

			childMatrix := matrix
			if element.Matrix != 0xFFFF && res.MatrixBank >= 0 && res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Matrix) < len(e.swf.MatrixBanks[res.MatrixBank].Matrices) {
				childMatrix = matrix.Multiply(e.swf.MatrixBanks[res.MatrixBank].Matrices[element.Matrix])
			}
			childColor := colorTransform
			if element.Color != 0xFFFF && res.MatrixBank >= 0 && res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Color) < len(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms) {
				childColor = colorTransform.Combine(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms[element.Color])
			}
			childSelected := selected || bind.Name == target.SelectedBind
			childBlend := inheritedBlend(blend, bind.Blend)
			drawCanvas := canvas
			childMask := alphaMask
			if maskMode == 1 {
				drawCanvas = maskCanvas
				childBlend = ""
				childMask = nil
			} else if maskMode == 2 {
				childMask = contentMask
			}
			if err := e.renderResourceWithMasks(target, bind.ID, t, childMatrix, childColor, seen, childSelected, childBlend, drawCanvas, spriteCache, childMask); err != nil {
				return err
			}
		}
		return nil
	case *sc.TextField, *sc.MovieClipModifier:
		return nil
	default:
		return nil
	}
}

func (e *Exporter) renderResourceMetalAt(
	target Target,
	t float64,
	worldBounds image.Rectangle,
	spriteCache map[bitmapCacheKey]*bitmapRenderable,
	compositor *metalCompositor,
	renderScale float64,
	rootMatrix sc.Matrix,
) error {
	return e.renderResourceMetalAtMasked(target, t, worldBounds, spriteCache, compositor, renderScale, rootMatrix, nil)
}

func (e *Exporter) renderResourceMetalAtMasked(
	target Target,
	t float64,
	worldBounds image.Rectangle,
	spriteCache map[bitmapCacheKey]*bitmapRenderable,
	compositor *metalCompositor,
	renderScale float64,
	rootMatrix sc.Matrix,
	alphaMask *metalSurface,
) error {
	offset := sc.Matrix{A: 1, D: 1, Tx: float64(-worldBounds.Min.X), Ty: float64(-worldBounds.Min.Y)}
	if renderScale != 1 {
		offset = sc.Matrix{A: renderScale, D: renderScale}.Multiply(offset)
	}
	offset = offset.Multiply(rootMatrix)
	return e.renderResourceMetalWithMasks(
		target,
		target.ResourceID,
		t,
		offset,
		sc.IdentityColor(),
		map[uint16]int{},
		target.SelectedBind == "",
		"",
		compositor,
		spriteCache,
		nil,
		alphaMask,
	)
}

func (e *Exporter) renderResourceMetalWithMasks(
	target Target,
	resourceID uint16,
	t float64,
	matrix sc.Matrix,
	colorTransform sc.ColorTransform,
	seen map[uint16]int,
	selected bool,
	blend string,
	compositor *metalCompositor,
	spriteCache map[bitmapCacheKey]*bitmapRenderable,
	destination *metalSurface,
	alphaMask *metalSurface,
) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		for idx := range res.Bitmaps {
			renderable, err := e.bitmapRenderable(res, idx, spriteCache)
			if err != nil {
				return err
			}
			full := matrix.Multiply(renderable.transform)
			if err := compositor.drawTo(
				destination,
				renderable.sprite,
				full,
				colorTransform,
				blend,
				target.Name == "bonus_gembox",
				alphaMask,
			); err != nil {
				return err
			}
		}
		return nil
	case *sc.MovieClip:
		if seen[resourceID] > 4 {
			return nil
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()

		frame := clipFrameAt(target, resourceID, res, t)
		maskMode := uint8(0)
		var maskSurface *metalSurface
		var contentMask *metalSurface
		contentMaskOwned := false
		maskAlreadyCached := false
		releaseMasks := func() {
			if contentMaskOwned {
				compositor.releaseSurface(contentMask)
			}
			if maskSurface != nil {
				compositor.releaseSurface(maskSurface)
			}
			maskSurface = nil
			contentMask = nil
			contentMaskOwned = false
			maskAlreadyCached = false
		}
		defer releaseMasks()

		for elementIndex, element := range frame.Elements {
			if int(element.Bind) >= len(res.Binds) {
				continue
			}
			bind := res.Binds[element.Bind]
			child := e.swf.Resources[bind.ID]
			if modifier, ok := child.(*sc.MovieClipModifier); ok {
				switch modifier.Modifier {
				case 38:
					releaseMasks()
					var err error
					if key, ok := e.staticMetalMaskKey(target, resourceID, res, frame, elementIndex, t, matrix, colorTransform, selected); ok {
						maskSurface, maskAlreadyCached, err = compositor.acquirePersistentMask(key)
					} else {
						maskSurface, err = compositor.acquireSurface()
					}
					if err != nil {
						return err
					}
					maskMode = 1
				case 39:
					maskAlreadyCached = false
					if contentMaskOwned {
						compositor.releaseSurface(contentMask)
						contentMask = nil
						contentMaskOwned = false
					}
					if maskSurface == nil {
						contentMask = alphaMask
					} else if alphaMask == nil {
						contentMask = maskSurface
					} else {
						var err error
						contentMask, err = compositor.combineMasks(maskSurface, alphaMask)
						if err != nil {
							return err
						}
						contentMaskOwned = true
					}
					maskMode = 2
				case 40:
					releaseMasks()
					maskMode = 0
				}
				continue
			}

			childMatrix := matrix
			if element.Matrix != 0xFFFF && res.MatrixBank >= 0 && res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Matrix) < len(e.swf.MatrixBanks[res.MatrixBank].Matrices) {
				childMatrix = matrix.Multiply(e.swf.MatrixBanks[res.MatrixBank].Matrices[element.Matrix])
			}
			childColor := colorTransform
			if element.Color != 0xFFFF && res.MatrixBank >= 0 && res.MatrixBank < len(e.swf.MatrixBanks) && int(element.Color) < len(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms) {
				childColor = colorTransform.Combine(e.swf.MatrixBanks[res.MatrixBank].ColorTransforms[element.Color])
			}
			childSelected := selected || bind.Name == target.SelectedBind
			childBlend := inheritedBlend(blend, bind.Blend)
			childDestination := destination
			childMask := alphaMask
			if maskMode == 1 {
				if maskAlreadyCached {
					continue
				}
				childDestination = maskSurface
				childBlend = ""
				childMask = nil
			} else if maskMode == 2 {
				childMask = contentMask
			}
			if err := e.renderResourceMetalWithMasks(
				target,
				bind.ID,
				t,
				childMatrix,
				childColor,
				seen,
				childSelected,
				childBlend,
				compositor,
				spriteCache,
				childDestination,
				childMask,
			); err != nil {
				return err
			}
		}
		return nil
	case *sc.TextField, *sc.MovieClipModifier:
		return nil
	default:
		return nil
	}
}

// staticMetalMaskKey identifies mask groups made only from direct shapes. These
// groups cannot animate within a clip frame, so Metal can rasterize them once
// and reuse the texture for subsequent output frames.
func (e *Exporter) staticMetalMaskKey(
	target Target,
	resourceID uint16,
	clip *sc.MovieClip,
	frame sc.MovieClipFrame,
	modifierIndex int,
	t float64,
	matrix sc.Matrix,
	colorTransform sc.ColorTransform,
	selected bool,
) (uint64, bool) {
	shapeCount := 0
	closed := false
	for i := modifierIndex + 1; i < len(frame.Elements); i++ {
		element := frame.Elements[i]
		if int(element.Bind) >= len(clip.Binds) {
			return 0, false
		}
		bind := clip.Binds[element.Bind]
		child := e.swf.Resources[bind.ID]
		if modifier, ok := child.(*sc.MovieClipModifier); ok {
			closed = modifier.Modifier == 39 && shapeCount > 0
			break
		}
		shape, ok := child.(*sc.Shape)
		if !ok || len(shape.Bitmaps) == 0 {
			return 0, false
		}
		shapeCount++
	}
	if !closed {
		return 0, false
	}
	hasher := &stateHasher{value: 1469598103934665603}
	hasher.add(0x4d41534b)
	hasher.add(uint64(resourceID))
	hasher.add(uint64(targetClipFrameIndex(target, resourceID, clip, t)))
	hasher.add(uint64(modifierIndex))
	hasher.addFloat(matrix.A)
	hasher.addFloat(matrix.B)
	hasher.addFloat(matrix.C)
	hasher.addFloat(matrix.D)
	hasher.addFloat(matrix.Tx)
	hasher.addFloat(matrix.Ty)
	hasher.addFloat(colorTransform.RAdd)
	hasher.addFloat(colorTransform.GAdd)
	hasher.addFloat(colorTransform.BAdd)
	hasher.addFloat(colorTransform.AMul)
	hasher.addFloat(colorTransform.RMul)
	hasher.addFloat(colorTransform.GMul)
	hasher.addFloat(colorTransform.BMul)
	if selected {
		hasher.add(1)
	}
	hasher.addString(target.SelectedBind)
	return hasher.value, true
}

func combineAlphaMasks(mask, parent *image.NRGBA) *image.NRGBA {
	if mask == nil {
		return parent
	}
	if parent == nil {
		return mask
	}
	bounds := mask.Bounds().Intersect(parent.Bounds())
	combined := image.NewNRGBA(mask.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			a := uint8(int(mask.NRGBAAt(x, y).A) * int(parent.NRGBAAt(x, y).A) / 255)
			combined.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: a})
		}
	}
	return combined
}

func (e *Exporter) renderDynamicAtInto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, animatedResources map[uint16]bool, canvas *image.NRGBA) (*image.NRGBA, error) {
	renderScale := float64(maxInt(e.opts.RenderScale, 1))
	canvasWidth := maxInt(1, int(math.Ceil(float64(worldBounds.Dx())*renderScale)))
	canvasHeight := maxInt(1, int(math.Ceil(float64(worldBounds.Dy())*renderScale)))
	if canvas == nil || canvas.Bounds().Dx() != canvasWidth || canvas.Bounds().Dy() != canvasHeight {
		canvas = image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	} else {
		clear(canvas.Pix)
	}
	offset := sc.Matrix{A: 1, D: 1, Tx: float64(-worldBounds.Min.X), Ty: float64(-worldBounds.Min.Y)}
	if renderScale > 1 {
		offset = sc.Matrix{A: renderScale, D: renderScale}.Multiply(offset)
	}
	err := e.visitResourceFiltered(target, target.ResourceID, t, offset, sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", "", animatedResources, true, func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string) error {
		renderable, err := e.bitmapRenderable(shape, idx, spriteCache)
		if err != nil {
			return err
		}
		full := matrix.Multiply(renderable.transform)
		return drawBitmap(canvas, renderable.sprite, full, colorTransform, blend)
	})
	if err != nil {
		return nil, err
	}
	return canvas, nil
}

func (e *Exporter) renderStaticBackdrop(target Target, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, animatedResources map[uint16]bool) (*image.NRGBA, error) {
	renderScale := e.outputScale(target, worldBounds)
	canvas := image.NewNRGBA(image.Rect(
		0,
		0,
		maxInt(1, int(math.Ceil(float64(worldBounds.Dx())*renderScale))),
		maxInt(1, int(math.Ceil(float64(worldBounds.Dy())*renderScale))),
	))
	offset := sc.Matrix{A: 1, D: 1, Tx: float64(-worldBounds.Min.X), Ty: float64(-worldBounds.Min.Y)}
	if renderScale != 1 {
		offset = sc.Matrix{A: renderScale, D: renderScale}.Multiply(offset)
	}
	err := e.visitResourceFiltered(target, target.ResourceID, 0, offset, sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", "", animatedResources, false, func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string) error {
		renderable, err := e.bitmapRenderable(shape, idx, spriteCache)
		if err != nil {
			return err
		}
		full := matrix.Multiply(renderable.transform)
		return drawBitmap(canvas, renderable.sprite, full, colorTransform, blend)
	})
	if err != nil {
		return nil, err
	}
	return canvas, nil
}

func cloneOrClearBackdrop(backdrop *image.NRGBA, worldBounds image.Rectangle) (*image.NRGBA, error) {
	width := maxInt(1, worldBounds.Dx())
	height := maxInt(1, worldBounds.Dy())
	if backdrop == nil {
		return image.NewNRGBA(image.Rect(0, 0, width, height)), nil
	}
	if backdrop.Bounds().Dx() != width || backdrop.Bounds().Dy() != height {
		return nil, fmt.Errorf("backdrop bounds mismatch: have=%dx%d want=%dx%d", backdrop.Bounds().Dx(), backdrop.Bounds().Dy(), width, height)
	}
	return cloneNRGBA(backdrop), nil
}

func (e *Exporter) collectAnimatedResourceSet(rootID uint16) map[uint16]bool {
	animated := map[uint16]bool{}
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
		isAnimated := len(clip.Frames) > 1
		for _, bind := range clip.Binds {
			if _, ok := e.swf.Resources[bind.ID].(*sc.MovieClip); ok {
				isAnimated = true
			}
			visit(bind.ID, seen)
		}
		if isAnimated {
			animated[id] = true
		}
	}
	visit(rootID, map[uint16]bool{})
	return animated
}

func (e *Exporter) staticOnlyContainers(target Target) map[uint16]bool {
	containers := map[uint16]bool{}
	if len(target.AncestorIDs) <= 2 {
		return containers
	}
	for _, resourceID := range target.AncestorIDs[1 : len(target.AncestorIDs)-1] {
		containers[resourceID] = true
	}
	return containers
}

func (e *Exporter) collectStaticOnlyBounds(target Target, spriteCache map[bitmapCacheKey]*bitmapRenderable, animatedResources map[uint16]bool) (image.Rectangle, error) {
	var bounds renderBounds
	err := e.visitResourceFiltered(target, target.ResourceID, 0, sc.IdentityMatrix(), sc.IdentityColor(), map[uint16]int{}, target.SelectedBind == "", "", animatedResources, false, func(_ uint16, shape *sc.Shape, idx int, matrix sc.Matrix, _ sc.ColorTransform, _ string) error {
		sprite, err := e.bitmapRenderable(shape, idx, spriteCache)
		if err != nil {
			return err
		}
		fullMatrix := matrix.Multiply(sprite.transform)
		width := float64(sprite.sprite.Bounds().Dx())
		height := float64(sprite.sprite.Bounds().Dy())
		for _, corner := range [][2]float64{{0, 0}, {width, 0}, {width, height}, {0, height}} {
			x, y := fullMatrix.Apply(corner[0], corner[1])
			bounds.add(x, y)
		}
		return nil
	})
	if err != nil {
		return image.Rectangle{}, err
	}
	if !bounds.set {
		return image.Rect(0, 0, 1, 1), nil
	}
	const padding = 2
	return image.Rect(
		int(math.Floor(bounds.minX))-padding,
		int(math.Floor(bounds.minY))-padding,
		int(math.Ceil(bounds.maxX))+padding,
		int(math.Ceil(bounds.maxY))+padding,
	), nil
}

func (e *Exporter) visitResourceFiltered(target Target, resourceID uint16, t float64, matrix sc.Matrix, colorTransform sc.ColorTransform, seen map[uint16]int, selected bool, blend string, animatedResources map[uint16]bool, wantAnimated bool, drawFn func(uint16, *sc.Shape, int, sc.Matrix, sc.ColorTransform, string) error) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		for idx := range res.Bitmaps {
			if err := drawFn(resourceID, res, idx, matrix, colorTransform, blend); err != nil {
				return err
			}
		}
	case *sc.MovieClip:
		if seen[resourceID] > 4 {
			return nil
		}
		if animatedResources[resourceID] == wantAnimated && resourceID != target.ResourceID {
			if wantAnimated {
				seen[resourceID]++
				defer func() { seen[resourceID]-- }()
				frame := clipFrameAt(target, resourceID, res, t)
				elements := frame.Elements
				if !wantAnimated {
					elements = e.unmaskedFrameElements(res, elements)
				}
				for _, element := range elements {
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
					childBlend := inheritedBlend(blend, bind.Blend)
					if err := e.visitResourceFiltered(target, bind.ID, t, childMatrix, childColor, seen, childSelected, childBlend, animatedResources, wantAnimated, drawFn); err != nil {
						return err
					}
				}
				return nil
			}
			return nil
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()
		frame := clipFrameAt(target, resourceID, res, t)
		elements := frame.Elements
		if !wantAnimated {
			elements = e.unmaskedFrameElements(res, elements)
		}
		for _, element := range elements {
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
			childBlend := inheritedBlend(blend, bind.Blend)
			if err := e.visitResourceFiltered(target, bind.ID, t, childMatrix, childColor, seen, childSelected, childBlend, animatedResources, wantAnimated, drawFn); err != nil {
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

func (e *Exporter) unmaskedFrameElements(clip *sc.MovieClip, elements []sc.FrameElement) []sc.FrameElement {
	visible := make([]sc.FrameElement, 0, len(elements))
	insideMaskGroup := false
	for _, element := range elements {
		if int(element.Bind) >= len(clip.Binds) {
			continue
		}
		resource := e.swf.Resources[clip.Binds[element.Bind].ID]
		if modifier, ok := resource.(*sc.MovieClipModifier); ok {
			switch modifier.Modifier {
			case 38, 39:
				insideMaskGroup = true
			case 40:
				insideMaskGroup = false
			}
			continue
		}
		if !insideMaskGroup {
			visible = append(visible, element)
		}
	}
	return visible
}

func (e *Exporter) visitResource(target Target, resourceID uint16, t float64, matrix sc.Matrix, colorTransform sc.ColorTransform, seen map[uint16]int, selected bool, blend string, drawFn func(uint16, *sc.Shape, int, sc.Matrix, sc.ColorTransform, string) error) error {
	resource := e.swf.Resources[resourceID]
	switch res := resource.(type) {
	case *sc.Shape:
		if !selected {
			return nil
		}
		for idx := range res.Bitmaps {
			if err := drawFn(resourceID, res, idx, matrix, colorTransform, blend); err != nil {
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
			childBlend := inheritedBlend(blend, bind.Blend)
			if err := e.visitResource(target, bind.ID, t, childMatrix, childColor, seen, childSelected, childBlend, drawFn); err != nil {
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

func inheritedBlend(parent, child string) string {
	if child != "" {
		return child
	}
	return parent
}

func clipFrameAt(target Target, resourceID uint16, clip *sc.MovieClip, t float64) sc.MovieClipFrame {
	idx := targetClipFrameIndex(target, resourceID, clip, t)
	if idx < 0 {
		return sc.MovieClipFrame{}
	}
	return clip.Frames[idx]
}

func targetClipFrameIndex(target Target, resourceID uint16, clip *sc.MovieClip, t float64) int {
	if target.SelectedFrame != nil && target.SelectedFrame.ResourceID == resourceID {
		if idx := target.SelectedFrame.FrameIndex; idx >= 0 && idx < len(clip.Frames) {
			return idx
		}
	}
	return clipFrameIndexAt(clip, t)
}

func clipFrameIndexAt(clip *sc.MovieClip, t float64) int {
	if len(clip.Frames) == 0 {
		return -1
	}
	if len(clip.Frames) == 1 {
		return 0
	}
	fps := clip.FrameRate
	if fps <= 0 {
		fps = 30
	}
	idx := int(math.Floor(t*float64(fps) + 1e-9))
	idx %= len(clip.Frames)
	return idx
}

func (e *Exporter) bitmapRenderable(shape *sc.Shape, idx int, spriteCache map[bitmapCacheKey]*bitmapRenderable) (*bitmapRenderable, error) {
	key := bitmapCacheKey{shapeID: shape.ID, index: idx}
	if cached, ok := spriteCache[key]; ok {
		return cached, nil
	}
	if cached, ok := e.bitmapCache.Load(key); ok {
		renderable := cached.(*bitmapRenderable)
		spriteCache[key] = renderable
		return renderable, nil
	}

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
	if cached, loaded := e.bitmapCache.LoadOrStore(key, renderable); loaded {
		renderable = cached.(*bitmapRenderable)
	}
	spriteCache[key] = renderable
	return renderable, nil
}

func (r *bitmapRenderable) blendLuminanceFloor(blend string) int {
	if r == nil || (blend != "add" && blend != "screen") {
		return -1
	}
	r.luminanceOnce.Do(func() {
		r.luminanceFloor = borderLuminanceFloor(r.sprite)
	})
	return r.luminanceFloor
}

func drawBitmap(dst *image.NRGBA, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string) error {
	return drawBitmapWithCoverage(dst, sprite, matrix, colorTransform, blend, false)
}

func drawBitmapWithCoverage(dst *image.NRGBA, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, allowAdditiveCoverage bool) error {
	return drawBitmapMasked(dst, sprite, matrix, colorTransform, blend, nil, allowAdditiveCoverage)
}

func drawBitmapMasked(dst *image.NRGBA, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, alphaMask *image.NRGBA, allowAdditiveCoverage bool) error {
	return drawBitmapMaskedWithFloor(dst, sprite, matrix, colorTransform, blend, alphaMask, allowAdditiveCoverage, -1)
}

func drawBitmapMaskedWithFloor(dst *image.NRGBA, sprite *image.NRGBA, matrix sc.Matrix, colorTransform sc.ColorTransform, blend string, alphaMask *image.NRGBA, allowAdditiveCoverage bool, cachedLuminanceFloor int) error {
	if alphaMask == nil && blend == "" && isIdentityColorTransform(colorTransform) {
		if offset, ok := integerTranslation(matrix); ok {
			destination := sprite.Bounds().Add(offset.Sub(sprite.Bounds().Min))
			clipped := destination.Intersect(dst.Bounds())
			if !clipped.Empty() {
				source := sprite.Bounds().Min.Add(clipped.Min.Sub(destination.Min))
				stddraw.Draw(dst, clipped, sprite, source, stddraw.Over)
			}
			return nil
		}
	}
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

	identityColor := isIdentityColorTransform(colorTransform)
	adjustBlendCoverage := blend == "add" || blend == "screen"
	blendLuminanceFloor := 0
	if adjustBlendCoverage {
		blendLuminanceFloor = cachedLuminanceFloor
		if blendLuminanceFloor < 0 {
			blendLuminanceFloor = borderLuminanceFloor(sprite)
		}
	}
	for y := top; y < bottom; y++ {
		sx := inv.A*(float64(left)+0.5) + inv.C*(float64(y)+0.5) + inv.Tx
		sy := inv.B*(float64(left)+0.5) + inv.D*(float64(y)+0.5) + inv.Ty
		for x := left; x < right; x++ {
			if sx < 0 || sy < 0 || sx >= w || sy >= h {
				sx += inv.A
				sy += inv.B
				continue
			}
			src := sampleNRGBABilinear(sprite, sx-0.5, sy-0.5)
			if alphaMask != nil {
				src.A = uint8(int(src.A) * int(alphaMask.NRGBAAt(x, y).A) / 255)
			}
			if src.A == 0 {
				sx += inv.A
				sy += inv.B
				continue
			}
			if adjustBlendCoverage {
				coverage := maxInt(0, maxInt(int(src.R), maxInt(int(src.G), int(src.B)))-blendLuminanceFloor)
				src.A = uint8(int(src.A) * coverage / 255)
			}
			if !identityColor {
				src = colorTransform.Apply(src)
			}
			if src.A == 0 {
				sx += inv.A
				sy += inv.B
				continue
			}
			switch blend {
			case "add":
				composeAddWithCoverage(dst, x, y, src, allowAdditiveCoverage)
			case "screen":
				composeScreen(dst, x, y, src)
			case "multiply":
				composeMultiply(dst, x, y, src)
			default:
				composeOver(dst, x, y, src)
			}
			sx += inv.A
			sy += inv.B
		}
	}
	return nil
}

func integerTranslation(matrix sc.Matrix) (image.Point, bool) {
	if matrix.A != 1 || matrix.B != 0 || matrix.C != 0 || matrix.D != 1 {
		return image.Point{}, false
	}
	x := math.Round(matrix.Tx)
	y := math.Round(matrix.Ty)
	if math.Abs(matrix.Tx-x) > 1e-9 || math.Abs(matrix.Ty-y) > 1e-9 {
		return image.Point{}, false
	}
	return image.Pt(int(x), int(y)), true
}

func sampleNRGBABilinear(sprite *image.NRGBA, x, y float64) color.NRGBA {
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	x1 := x0 + 1
	y1 := y0 + 1
	x0 = minInt(maxInt(x0, 0), sprite.Bounds().Dx()-1)
	x1 = minInt(maxInt(x1, 0), sprite.Bounds().Dx()-1)
	y0 = minInt(maxInt(y0, 0), sprite.Bounds().Dy()-1)
	y1 = minInt(maxInt(y1, 0), sprite.Bounds().Dy()-1)

	type sample struct {
		color  color.NRGBA
		weight float64
	}
	samples := []sample{
		{sprite.NRGBAAt(sprite.Bounds().Min.X+x0, sprite.Bounds().Min.Y+y0), (1 - fx) * (1 - fy)},
		{sprite.NRGBAAt(sprite.Bounds().Min.X+x1, sprite.Bounds().Min.Y+y0), fx * (1 - fy)},
		{sprite.NRGBAAt(sprite.Bounds().Min.X+x0, sprite.Bounds().Min.Y+y1), (1 - fx) * fy},
		{sprite.NRGBAAt(sprite.Bounds().Min.X+x1, sprite.Bounds().Min.Y+y1), fx * fy},
	}
	var alpha, red, green, blue float64
	for _, sample := range samples {
		a := float64(sample.color.A) / 255
		alpha += a * sample.weight
		red += float64(sample.color.R) / 255 * a * sample.weight
		green += float64(sample.color.G) / 255 * a * sample.weight
		blue += float64(sample.color.B) / 255 * a * sample.weight
	}
	if alpha == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{
		R: clampColorByte(red / alpha * 255),
		G: clampColorByte(green / alpha * 255),
		B: clampColorByte(blue / alpha * 255),
		A: clampColorByte(alpha * 255),
	}
}

func borderLuminanceFloor(sprite *image.NRGBA) int {
	width := sprite.Bounds().Dx()
	height := sprite.Bounds().Dy()
	if width == 0 || height == 0 {
		return 0
	}
	var histogram [256]int
	count := 0
	add := func(x, y int) {
		pixel := sprite.NRGBAAt(sprite.Bounds().Min.X+x, sprite.Bounds().Min.Y+y)
		histogram[maxInt(int(pixel.R), maxInt(int(pixel.G), int(pixel.B)))]++
		count++
	}
	for x := 0; x < width; x++ {
		add(x, 0)
		add(x, height-1)
	}
	for y := 1; y < height-1; y++ {
		add(0, y)
		add(width-1, y)
	}
	middle := count / 2
	seen := 0
	for value, occurrences := range histogram {
		seen += occurrences
		if seen > middle {
			return value
		}
	}
	return 0
}

func composeAdd(dst *image.NRGBA, x, y int, src color.NRGBA) {
	composeAddWithCoverage(dst, x, y, src, false)
}

func composeAddWithCoverage(dst *image.NRGBA, x, y int, src color.NRGBA, allowCoverage bool) {
	if src.A == 0 {
		return
	}
	dstColor := dst.NRGBAAt(x, y)
	if dstColor.A == 0 && !allowCoverage {
		return
	}
	maxChannel := maxInt(int(src.R), maxInt(int(src.G), int(src.B)))
	if maxChannel <= 24 {
		return
	}
	lightAlpha := math.Min(1, float64(maxChannel-24)/64)
	srcAlpha := int(math.Round(float64(src.A) * lightAlpha))
	if srcAlpha == 0 {
		return
	}

	sa := float64(srcAlpha) / 255
	da := float64(dstColor.A) / 255
	outA := sa + da*(1-sa)
	channel := func(srcChannel, dstChannel uint8) uint8 {
		premultiplied := float64(dstChannel)/255*da + float64(srcChannel)/255*sa
		return clampColorByte(math.Min(premultiplied, outA) / outA * 255)
	}
	dst.SetNRGBA(x, y, color.NRGBA{
		R: channel(src.R, dstColor.R),
		G: channel(src.G, dstColor.G),
		B: channel(src.B, dstColor.B),
		A: clampColorByte(outA * 255),
	})
}

func composeScreen(dst *image.NRGBA, x, y int, src color.NRGBA) {
	intensity := float64(maxInt(int(src.R), maxInt(int(src.G), int(src.B)))) / 255
	sa := float64(src.A) / 255 * intensity
	if sa <= 0 {
		return
	}
	dstColor := dst.NRGBAAt(x, y)
	da := float64(dstColor.A) / 255
	outA := sa + da*(1-sa)
	if outA <= 0 {
		return
	}

	channel := func(srcChannel, dstChannel uint8) uint8 {
		cs := float64(srcChannel) / 255
		cb := float64(dstChannel) / 255
		screen := 1 - (1-cb)*(1-cs)
		premultiplied := (1-sa)*cb*da + (1-da)*cs*sa + sa*da*screen
		return clampColorByte(premultiplied / outA * 255)
	}
	dst.SetNRGBA(x, y, color.NRGBA{
		R: channel(src.R, dstColor.R),
		G: channel(src.G, dstColor.G),
		B: channel(src.B, dstColor.B),
		A: clampColorByte(outA * 255),
	})
}

func composeMultiply(dst *image.NRGBA, x, y int, src color.NRGBA) {
	dstColor := dst.NRGBAAt(x, y)
	if dstColor.A == 0 || src.A == 0 {
		return
	}
	sa := float64(src.A) / 255
	channel := func(srcChannel, dstChannel uint8) uint8 {
		base := float64(dstChannel)
		multiplied := base * float64(srcChannel) / 255
		return clampColorByte(base*(1-sa) + multiplied*sa)
	}
	dst.SetNRGBA(x, y, color.NRGBA{
		R: channel(src.R, dstColor.R),
		G: channel(src.G, dstColor.G),
		B: channel(src.B, dstColor.B),
		A: dstColor.A,
	})
}

func clampColorByte(value float64) uint8 {
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(math.Round(value))
}

func isIdentityColorTransform(c sc.ColorTransform) bool {
	return c.RAdd == 0 &&
		c.GAdd == 0 &&
		c.BAdd == 0 &&
		c.AMul == 1 &&
		c.RMul == 1 &&
		c.GMul == 1 &&
		c.BMul == 1
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

func (e *Exporter) newSceneryRenderContext(target Target) (*sceneryRenderContext, error) {
	base, err := e.loadMappedSceneryBase(target.Name)
	if err != nil || base == nil || base.swf == nil {
		return nil, err
	}
	baseOpts := e.opts
	baseOpts.AssetBaseNames = nil
	baseOpts.BaseSCPath = ""
	return &sceneryRenderContext{
		exporter:    NewExporterWithOptions(base.swf, baseOpts),
		target:      base.target,
		spriteCache: map[bitmapCacheKey]*bitmapRenderable{},
	}, nil
}

func sceneDuration(target Target, scenery *sceneryRenderContext) (float64, error) {
	if scenery == nil || scenery.target.Duration <= 0 {
		return target.Duration, nil
	}
	if target.Duration <= 0 {
		return scenery.target.Duration, nil
	}
	const maximumSceneLoopMS = 120_000
	foregroundMS := int64(math.Round(target.Duration * 1000))
	baseMS := int64(math.Round(scenery.target.Duration * 1000))
	commonMS := foregroundMS / gcdInt64(foregroundMS, baseMS) * baseMS
	if commonMS > maximumSceneLoopMS {
		return 0, fmt.Errorf("combined scenery loop is %0.3fs, exceeding the 120s safety limit", float64(commonMS)/1000)
	}
	return float64(commonMS) / 1000, nil
}

func gcdInt64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func (e *Exporter) sceneChangePoints(target Target, duration float64, scenery *sceneryRenderContext) []float64 {
	points := e.collectChangePoints(target, duration)
	if scenery == nil {
		return points
	}
	set := make(map[uint64]float64, len(points))
	for _, point := range points {
		set[math.Float64bits(point)] = point
	}
	for _, point := range scenery.exporter.collectChangePoints(scenery.target, duration) {
		set[math.Float64bits(point)] = point
	}
	points = points[:0]
	for _, point := range set {
		points = append(points, point)
	}
	sort.Float64s(points)
	return points
}

func timelineSteps(changePoints []float64, duration float64) []renderStep {
	steps := make([]renderStep, 0, len(changePoints))
	for index, point := range changePoints {
		next := duration
		if index+1 < len(changePoints) {
			next = changePoints[index+1]
		}
		delayMS := int(math.Round(next*1000)) - int(math.Round(point*1000))
		if delayMS <= 0 {
			delayMS = 1
		}
		steps = append(steps, renderStep{Time: point, DelayMS: delayMS})
	}
	return steps
}

func (e *Exporter) collectSceneBounds(target Target, duration float64, sampleTimes []float64, spriteCache map[bitmapCacheKey]*bitmapRenderable, scenery *sceneryRenderContext) (image.Rectangle, error) {
	bounds, err := e.collectBounds(target, duration, sampleTimes, spriteCache)
	if err != nil || scenery == nil {
		return bounds, err
	}
	baseBounds, err := scenery.exporter.collectBounds(scenery.target, duration, sampleTimes, scenery.spriteCache)
	if err != nil {
		return image.Rectangle{}, err
	}
	scenery.baseMatrix, err = e.sceneryAlignmentMatrix(target, bounds, spriteCache, scenery, baseBounds)
	if err != nil {
		return image.Rectangle{}, err
	}
	translatedBase := image.Rect(
		int(math.Floor(float64(baseBounds.Min.X)+scenery.baseMatrix.Tx)),
		int(math.Floor(float64(baseBounds.Min.Y)+scenery.baseMatrix.Ty)),
		int(math.Ceil(float64(baseBounds.Max.X)+scenery.baseMatrix.Tx)),
		int(math.Ceil(float64(baseBounds.Max.Y)+scenery.baseMatrix.Ty)),
	)
	combined := sceneryViewportBounds(bounds, translatedBase)
	if authored, ok := e.namedTextFieldBounds(target, "camera_bounds"); ok {
		combined = authored
	}
	foreground, _, err := e.renderScenePreferredAtInto(target, 0, combined, spriteCache, nil, nil)
	if err != nil {
		return image.Rectangle{}, err
	}
	scenery.allowedMask = sceneryAllowedMask(foreground)
	return combined, nil
}

// namedTextFieldBounds resolves an authored, invisible rectangle through the
// same movie-clip matrices used by the renderer. Scenery files deliberately
// contain artwork outside camera_bounds, so visible-pixel bounds are not a
// reliable viewport.
func (e *Exporter) namedTextFieldBounds(target Target, name string) (image.Rectangle, bool) {
	var found renderBounds
	var visit func(uint16, sc.Matrix, map[uint16]int)
	visit = func(resourceID uint16, matrix sc.Matrix, seen map[uint16]int) {
		if seen[resourceID] > 4 {
			return
		}
		clip, ok := e.swf.Resources[resourceID].(*sc.MovieClip)
		if !ok {
			return
		}
		seen[resourceID]++
		defer func() { seen[resourceID]-- }()
		frame := clipFrameAt(target, resourceID, clip, 0)
		bank := matrixBankForClip(e.swf, clip)
		for _, element := range frame.Elements {
			if int(element.Bind) >= len(clip.Binds) {
				continue
			}
			bind := clip.Binds[element.Bind]
			childMatrix := matrix
			if bank != nil && element.Matrix != 0xFFFF && int(element.Matrix) < len(bank.Matrices) {
				childMatrix = matrix.Multiply(bank.Matrices[element.Matrix])
			}
			if bind.Name == name {
				field, ok := e.swf.Resources[bind.ID].(*sc.TextField)
				if !ok {
					continue
				}
				for _, corner := range [][2]float64{
					{float64(field.Left), float64(field.Top)},
					{float64(field.Right), float64(field.Top)},
					{float64(field.Right), float64(field.Bottom)},
					{float64(field.Left), float64(field.Bottom)},
				} {
					x, y := childMatrix.Apply(corner[0], corner[1])
					found.add(x, y)
				}
				continue
			}
			if _, ok := e.swf.Resources[bind.ID].(*sc.MovieClip); ok {
				visit(bind.ID, childMatrix, seen)
			}
		}
	}
	visit(target.ResourceID, sc.IdentityMatrix(), map[uint16]int{})
	if !found.set || found.maxX <= found.minX || found.maxY <= found.minY {
		return image.Rectangle{}, false
	}
	return image.Rect(
		int(math.Floor(found.minX)),
		int(math.Floor(found.minY)),
		int(math.Ceil(found.maxX)),
		int(math.Ceil(found.maxY)),
	), true
}

func sceneryViewportBounds(foreground, alignedBase image.Rectangle) image.Rectangle {
	if foreground.Empty() || alignedBase.Empty() {
		return foreground.Union(alignedBase)
	}
	// Player_Background contains staging geometry outside the in-game camera.
	// The aligned game-area base is the stable camera anchor; a 15% horizontal
	// margin retains the surrounding foreground while a 4:3 viewport excludes
	// detached off-camera sprites.
	width := maxInt(alignedBase.Dx(), int(math.Ceil(float64(alignedBase.Dx())*1.15)))
	height := int(math.Ceil(float64(width) * 3 / 4))
	if height < alignedBase.Dy() {
		height = alignedBase.Dy()
		width = int(math.Ceil(float64(height) * 4 / 3))
	}
	width = minInt(width, foreground.Dx())
	height = minInt(height, foreground.Dy())
	centerX := float64(alignedBase.Min.X+alignedBase.Max.X) / 2
	centerY := float64(alignedBase.Min.Y+alignedBase.Max.Y) / 2
	minX := int(math.Round(centerX - float64(width)/2))
	minY := int(math.Round(centerY - float64(height)/2))
	if minX < foreground.Min.X {
		minX = foreground.Min.X
	} else if minX+width > foreground.Max.X {
		minX = foreground.Max.X - width
	}
	if minY < foreground.Min.Y {
		minY = foreground.Min.Y
	} else if minY+height > foreground.Max.Y {
		minY = foreground.Max.Y - height
	}
	return image.Rect(minX, minY, minX+width, minY+height)
}

func (e *Exporter) sceneryAlignmentMatrix(target Target, foregroundBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, scenery *sceneryRenderContext, baseBounds image.Rectangle) (sc.Matrix, error) {
	longestEdge := maxInt(maxInt(foregroundBounds.Dx(), foregroundBounds.Dy()), maxInt(baseBounds.Dx(), baseBounds.Dy()))
	if longestEdge <= 0 {
		return sc.IdentityMatrix(), nil
	}
	sampleScale := math.Min(1, 256/float64(longestEdge))
	foreground, err := e.renderResourceAtInto(target, 0, foregroundBounds, spriteCache, nil, true, sampleScale, sc.IdentityMatrix())
	if err != nil {
		return sc.Matrix{}, err
	}
	base, err := scenery.exporter.renderResourceAtInto(scenery.target, 0, baseBounds, scenery.spriteCache, nil, true, sampleScale, sc.IdentityMatrix())
	if err != nil {
		return sc.Matrix{}, err
	}
	holeX, holeY, foundHole := largestEnclosedTransparentCentroid(foreground)
	baseX, baseY := alphaCentroid(base, 0)
	if foundHole {
		foregroundWorldX := float64(foregroundBounds.Min.X) + holeX/sampleScale
		foregroundWorldY := float64(foregroundBounds.Min.Y) + holeY/sampleScale
		baseWorldX := float64(baseBounds.Min.X) + baseX/sampleScale
		baseWorldY := float64(baseBounds.Min.Y) + baseY/sampleScale
		return sc.Matrix{A: 1, D: 1, Tx: foregroundWorldX - baseWorldX, Ty: foregroundWorldY - baseWorldY}, nil
	}
	foregroundCenterX := float64(foregroundBounds.Min.X+foregroundBounds.Max.X) / 2
	foregroundCenterY := float64(foregroundBounds.Min.Y+foregroundBounds.Max.Y) / 2
	baseCenterX := float64(baseBounds.Min.X+baseBounds.Max.X) / 2
	baseCenterY := float64(baseBounds.Min.Y+baseBounds.Max.Y) / 2
	return sc.Matrix{A: 1, D: 1, Tx: foregroundCenterX - baseCenterX, Ty: foregroundCenterY - baseCenterY}, nil
}

func largestEnclosedTransparentCentroid(frame *image.NRGBA) (float64, float64, bool) {
	if frame == nil || frame.Bounds().Empty() {
		return 0, 0, false
	}
	width, height := frame.Bounds().Dx(), frame.Bounds().Dy()
	transparent := func(index int) bool {
		x := index % width
		y := index / width
		return frame.NRGBAAt(frame.Bounds().Min.X+x, frame.Bounds().Min.Y+y).A <= 8
	}
	outside := make([]bool, width*height)
	queue := make([]int, 0, width*2+height*2)
	pushOutside := func(index int) {
		if index < 0 || index >= len(outside) || outside[index] || !transparent(index) {
			return
		}
		outside[index] = true
		queue = append(queue, index)
	}
	for x := 0; x < width; x++ {
		pushOutside(x)
		pushOutside((height-1)*width + x)
	}
	for y := 0; y < height; y++ {
		pushOutside(y * width)
		pushOutside(y*width + width - 1)
	}
	for head := 0; head < len(queue); head++ {
		index := queue[head]
		x, y := index%width, index/width
		if x > 0 {
			pushOutside(index - 1)
		}
		if x+1 < width {
			pushOutside(index + 1)
		}
		if y > 0 {
			pushOutside(index - width)
		}
		if y+1 < height {
			pushOutside(index + width)
		}
	}
	visited := append([]bool(nil), outside...)
	bestCount := 0
	bestX, bestY := 0.0, 0.0
	component := make([]int, 0, width*height/4)
	for start := range visited {
		if visited[start] || !transparent(start) {
			continue
		}
		component = component[:0]
		component = append(component, start)
		visited[start] = true
		var sumX, sumY int64
		for head := 0; head < len(component); head++ {
			index := component[head]
			x, y := index%width, index/width
			sumX += int64(x)
			sumY += int64(y)
			for _, next := range []int{index - 1, index + 1, index - width, index + width} {
				if next < 0 || next >= len(visited) || visited[next] || !transparent(next) {
					continue
				}
				nextX, nextY := next%width, next/width
				if absInt(nextX-x)+absInt(nextY-y) != 1 {
					continue
				}
				visited[next] = true
				component = append(component, next)
			}
		}
		if len(component) > bestCount {
			bestCount = len(component)
			bestX = float64(sumX) / float64(bestCount)
			bestY = float64(sumY) / float64(bestCount)
		}
	}
	return bestX, bestY, bestCount > 0
}

// sceneryAllowedMask removes the border-connected transparent area around a
// scenery foreground while retaining its enclosed village opening. Opaque and
// antialiased foreground pixels remain allowed so the base still sits behind
// the foreground's edge pixels without leaking beyond its outside silhouette.
func sceneryAllowedMask(foreground *image.NRGBA) *image.NRGBA {
	if foreground == nil || foreground.Bounds().Empty() {
		return nil
	}
	bounds := foreground.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mask := image.NewNRGBA(bounds)

	// Alpha 1 is an unvisited transparent pixel, alpha 255 is a barrier that
	// remains allowed, and alpha 0 is border-connected exterior transparency.
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			destination := mask.PixOffset(x, y)
			if foreground.Pix[foreground.PixOffset(x, y)+3] <= 8 {
				mask.Pix[destination+3] = 1
				continue
			}
			mask.Pix[destination] = 255
			mask.Pix[destination+1] = 255
			mask.Pix[destination+2] = 255
			mask.Pix[destination+3] = 255
		}
	}

	queue := make([]uint32, 0, width*2+height*2)
	pushExterior := func(x, y int) {
		if x < 0 || x >= width || y < 0 || y >= height {
			return
		}
		pixel := mask.PixOffset(bounds.Min.X+x, bounds.Min.Y+y)
		if mask.Pix[pixel+3] != 1 {
			return
		}
		mask.Pix[pixel+3] = 0
		queue = append(queue, uint32(y*width+x))
	}
	for x := 0; x < width; x++ {
		pushExterior(x, 0)
		pushExterior(x, height-1)
	}
	for y := 0; y < height; y++ {
		pushExterior(0, y)
		pushExterior(width-1, y)
	}
	for head := 0; head < len(queue); head++ {
		index := int(queue[head])
		x, y := index%width, index/width
		pushExterior(x-1, y)
		pushExterior(x+1, y)
		pushExterior(x, y-1)
		pushExterior(x, y+1)
	}

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := mask.PixOffset(x, y)
			if mask.Pix[pixel+3] == 0 {
				continue
			}
			mask.Pix[pixel] = 255
			mask.Pix[pixel+1] = 255
			mask.Pix[pixel+2] = 255
			mask.Pix[pixel+3] = 255
		}
	}
	return mask
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (e *Exporter) renderSceneAtInto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, scenery *sceneryRenderContext, canvas *image.NRGBA) (*image.NRGBA, error) {
	var err error
	if scenery == nil {
		return e.renderAtInto(target, t, worldBounds, spriteCache, canvas)
	}
	renderScale := e.outputScale(target, worldBounds)
	canvas, err = scenery.exporter.renderResourceAtIntoMasked(scenery.target, t, worldBounds, scenery.spriteCache, canvas, true, renderScale, scenery.baseMatrix, scenery.allowedMask)
	if err != nil {
		return nil, err
	}
	return e.renderResourceAtInto(target, t, worldBounds, spriteCache, canvas, false, renderScale, sc.IdentityMatrix())
}

func (e *Exporter) renderScenePreferredAtInto(target Target, t float64, worldBounds image.Rectangle, spriteCache map[bitmapCacheKey]*bitmapRenderable, scenery *sceneryRenderContext, canvas *image.NRGBA) (*image.NRGBA, bool, error) {
	if scenery == nil && e.opts.AssetBaseNames[target.Name] != "" {
		frame, err := e.renderAt(target, t, worldBounds, spriteCache)
		return frame, false, err
	}
	if !e.opts.DisableGPU {
		renderScale := e.outputScale(target, worldBounds)
		width := maxInt(1, int(math.Ceil(float64(worldBounds.Dx())*renderScale)))
		height := maxInt(1, int(math.Ceil(float64(worldBounds.Dy())*renderScale)))
		compositor, err := newMetalCompositor(width, height)
		if err == nil {
			frame, renderErr := e.renderSceneMetalAtInto(target, t, worldBounds, spriteCache, scenery, compositor, canvas)
			closeErr := compositor.close()
			if renderErr != nil {
				return nil, false, renderErr
			}
			if closeErr != nil {
				return nil, false, closeErr
			}
			return frame, true, nil
		}
		if !errors.Is(err, errMetalCompositorUnavailable) {
			return nil, false, fmt.Errorf("initialize Metal compositor: %w", err)
		}
	}
	frame, err := e.renderSceneAtInto(target, t, worldBounds, spriteCache, scenery, canvas)
	return frame, false, err
}

func (e *Exporter) renderSceneMetalAtInto(
	target Target,
	t float64,
	worldBounds image.Rectangle,
	spriteCache map[bitmapCacheKey]*bitmapRenderable,
	scenery *sceneryRenderContext,
	compositor *metalCompositor,
	canvas *image.NRGBA,
) (*image.NRGBA, error) {
	if err := compositor.beginFrame(); err != nil {
		return nil, err
	}
	if err := compositor.clear(color.NRGBA{}); err != nil {
		return nil, err
	}
	renderScale := e.outputScale(target, worldBounds)
	if scenery != nil {
		var allowedSurface *metalSurface
		if scenery.allowedMask != nil {
			var err error
			allowedSurface, err = compositor.borrowedSurface(scenery.allowedMask)
			if err != nil {
				return nil, err
			}
		}
		if err := scenery.exporter.renderResourceMetalAtMasked(
			scenery.target,
			t,
			worldBounds,
			scenery.spriteCache,
			compositor,
			renderScale,
			scenery.baseMatrix,
			allowedSurface,
		); err != nil {
			return nil, err
		}
	}
	if err := e.renderResourceMetalAt(
		target,
		t,
		worldBounds,
		spriteCache,
		compositor,
		renderScale,
		sc.IdentityMatrix(),
	); err != nil {
		return nil, err
	}
	return compositor.readback(canvas)
}

func (e *Exporter) loadMappedSceneryBase(exportName string) (*sceneryBaseImage, error) {
	if exportName == "" || exportName != "Player_Background" {
		return nil, nil
	}
	e.sceneryBaseMu.Lock()
	defer e.sceneryBaseMu.Unlock()
	if cached, ok := e.sceneryBaseCache[e.swf.Filename]; ok {
		return cached, nil
	}
	basePath, err := lookupSceneryBaseSWF(e.swf.Filename, exportName)
	if err != nil || basePath == "" {
		if err == nil {
			e.sceneryBaseCache[e.swf.Filename] = nil
		}
		return nil, err
	}
	baseSWF, err := sc.Load(basePath)
	if err != nil {
		return nil, err
	}
	baseExporter := NewExporterWithOptions(baseSWF, e.opts)
	resourceID, resource := findExportedResource(baseSWF, "Player_village_bg")
	if resource == nil {
		return nil, nil
	}
	target, err := baseExporter.prepareTarget("Player_village_bg", resourceID, resource)
	if err != nil {
		return nil, err
	}
	out := &sceneryBaseImage{swf: baseSWF, target: target}
	e.sceneryBaseCache[e.swf.Filename] = out
	return out, nil
}

func lookupSceneryBaseSWF(sourceSC, exportName string) (string, error) {
	if exportName != "Player_Background" {
		return "", nil
	}
	backgroundsPath := filepath.Join(filepath.Dir(filepath.Dir(sourceSC)), "logic", "village_backgrounds.json")
	raw, err := os.ReadFile(backgroundsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var backgrounds map[string]map[string]any
	if err := json.Unmarshal(raw, &backgrounds); err != nil {
		return "", err
	}
	root := filepath.Dir(filepath.Dir(sourceSC))
	sourcePath, err := filepath.Abs(sourceSC)
	if err != nil {
		return "", err
	}
	for _, entry := range backgrounds {
		swf, _ := entry["SWF"].(string)
		foreground, _ := entry["Foreground"].(string)
		candidatePath := swf
		if !filepath.IsAbs(candidatePath) {
			candidatePath = filepath.Join(root, candidatePath)
		}
		candidatePath, err = filepath.Abs(candidatePath)
		if err != nil || filepath.Clean(candidatePath) != filepath.Clean(sourcePath) || foreground != exportName {
			continue
		}
		baseSWF, _ := entry["BaseSWF"].(string)
		if baseSWF == "" {
			return "", nil
		}
		if !filepath.IsAbs(baseSWF) {
			baseSWF = filepath.Join(root, baseSWF)
		}
		return baseSWF, nil
	}
	return "", nil
}

func findExportedResource(swf *sc.SWF, exportName string) (uint16, sc.Resource) {
	for resourceID, names := range swf.Exports {
		for _, name := range names {
			if name == exportName {
				return resourceID, swf.Resources[resourceID]
			}
		}
	}
	return 0, nil
}

func drawOpaqueOver(dst, src *image.NRGBA) {
	if dst == nil || src == nil {
		return
	}
	for y := 0; y < minInt(dst.Bounds().Dy(), src.Bounds().Dy()); y++ {
		for x := 0; x < minInt(dst.Bounds().Dx(), src.Bounds().Dx()); x++ {
			px := src.NRGBAAt(src.Bounds().Min.X+x, src.Bounds().Min.Y+y)
			composeOver(dst, dst.Bounds().Min.X+x, dst.Bounds().Min.Y+y, px)
		}
	}
}

func overlayOpaque(bottom, top *image.NRGBA) *image.NRGBA {
	result := cloneNRGBA(bottom)
	drawOpaqueOver(result, top)
	return result
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	if src == nil {
		return nil
	}
	dst := image.NewNRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

func manifestOutputPath(exportsDir, outputFile string) string {
	if filepath.IsAbs(outputFile) {
		return outputFile
	}
	return filepath.Join(exportsDir, outputFile)
}

type nameAllocator struct {
	mu      sync.Mutex
	used    map[string]int
	dir     string
	mapped  map[string]string
	claimed map[string]string
}

func newNameAllocator(dir string, mapped map[string]string) *nameAllocator {
	return &nameAllocator{used: map[string]int{}, dir: dir, mapped: mapped, claimed: map[string]string{}}
}

func (n *nameAllocator) Next(raw string, resourceID uint16) (string, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if mappedPath, ok := n.mapped[raw]; ok {
		cleanPath := filepath.Clean(mappedPath)
		basePath := strings.TrimSuffix(cleanPath, filepath.Ext(cleanPath))
		claimKey := strings.ToLower(basePath)
		if existing, claimed := n.claimed[claimKey]; claimed {
			return "", "", fmt.Errorf("output path %q already claimed by %q", basePath, existing)
		}
		n.claimed[claimKey] = raw
		if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
			return "", "", err
		}
		return basePath, basePath, nil
	}

	base := sanitizeFilename(raw)
	if base == "" {
		base = fmt.Sprintf("resource_%d", resourceID)
	}
	if _, ok := n.used[base]; !ok {
		n.used[base] = 1
		return filepath.Join(n.dir, base), base, nil
	}

	withID := fmt.Sprintf("%s__%d", base, resourceID)
	if _, ok := n.used[withID]; !ok {
		n.used[withID] = 1
		return filepath.Join(n.dir, withID), withID, nil
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s__%d", withID, i)
		if _, ok := n.used[candidate]; !ok {
			n.used[candidate] = 1
			return filepath.Join(n.dir, candidate), candidate, nil
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
