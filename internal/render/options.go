package render

import (
	"context"
	"sort"
	"strings"
)

type ExportOptions struct {
	Context                 context.Context
	RenderScale             int
	SceneryMaxDimension     int
	SceneryMaxDimensionSet  bool
	SceneryFormat           string
	HEVCQuality             int
	HEVCQualitySet          bool
	IncludePrefixes         []string
	AssetNames              []string
	AssetOutputPaths        map[string]string
	AssetBaseNames          map[string]string
	BaseSCPath              string
	PreferWebP              bool
	WebPQuality             int
	WebPQualitySet          bool
	WebPMethod              int
	DisableGPU              bool
	FileConcurrency         int
	Profile                 bool
	ProfileTopN             int
	SkipTinyOutputThreshold int
	FirstFrameOnly          bool
	LastFrameOnly           bool
	FrameIndex              int
	StaticOnly              bool
	PreferredFrameLabel     string
}

func normalizeExportOptions(opts ExportOptions) ExportOptions {
	if opts.RenderScale <= 1 {
		opts.RenderScale = 1
	}
	if !opts.SceneryMaxDimensionSet && opts.SceneryMaxDimension == 0 {
		opts.SceneryMaxDimension = 2048
	}
	if opts.SceneryMaxDimension < 0 {
		opts.SceneryMaxDimension = 0
	}
	opts.SceneryFormat = strings.ToLower(strings.TrimSpace(opts.SceneryFormat))
	if opts.SceneryFormat == "" {
		opts.SceneryFormat = "auto"
	}
	if !opts.HEVCQualitySet && opts.HEVCQuality == 0 {
		opts.HEVCQuality = 80
	}
	if opts.HEVCQuality < 0 {
		opts.HEVCQuality = 0
	} else if opts.HEVCQuality > 100 {
		opts.HEVCQuality = 100
	}
	if opts.FileConcurrency <= 0 {
		opts.FileConcurrency = 1
	}
	if opts.ProfileTopN <= 0 {
		opts.ProfileTopN = 5
	}
	if opts.SkipTinyOutputThreshold < 0 {
		opts.SkipTinyOutputThreshold = 0
	}
	if !opts.WebPQualitySet && opts.WebPQuality == 0 {
		opts.WebPQuality = 88
	}
	if opts.WebPQuality < 0 {
		opts.WebPQuality = 0
	} else if opts.WebPQuality > 100 {
		opts.WebPQuality = 100
	}
	if opts.WebPMethod < 0 {
		opts.WebPMethod = 0
	} else if opts.WebPMethod > 6 {
		opts.WebPMethod = 6
	}
	if opts.FrameIndex < 0 {
		opts.FrameIndex = 0
	}
	opts.PreferredFrameLabel = strings.TrimSpace(opts.PreferredFrameLabel)
	opts.BaseSCPath = strings.TrimSpace(opts.BaseSCPath)
	if opts.StaticOnly {
		opts.FirstFrameOnly = false
		opts.LastFrameOnly = false
		opts.FrameIndex = 0
	} else if opts.FrameIndex > 0 {
		opts.FirstFrameOnly = false
		opts.LastFrameOnly = false
	} else if opts.FirstFrameOnly && opts.LastFrameOnly {
		opts.LastFrameOnly = false
	}
	opts.AssetNames = normalizeNames(opts.AssetNames)
	opts.AssetOutputPaths = normalizeAssetOutputPaths(opts.AssetOutputPaths)
	opts.AssetBaseNames = normalizeAssetOutputPaths(opts.AssetBaseNames)
	if len(opts.AssetNames) == 0 && len(opts.AssetOutputPaths) > 0 {
		opts.AssetNames = sortedAssetOutputNames(opts.AssetOutputPaths)
	}
	return opts
}

func normalizeNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeAssetOutputPaths(paths map[string]string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string]string, len(paths))
	for name, path := range paths {
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if name == "" || path == "" {
			continue
		}
		out[name] = path
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedAssetOutputNames(paths map[string]string) []string {
	if len(paths) == 0 {
		return nil
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
