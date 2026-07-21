package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"sc2fla/internal/render"
	"sc2fla/internal/sc3d"
)

type cliCommand string

const (
	commandExport      cliCommand = "export"
	commandTextureRoot cliCommand = "texture-root"
	commandSCRoot      cliCommand = "sc-root"
	commandSC3DViewer  cliCommand = "sc3d-viewer"
)

type cliConfig struct {
	command      cliCommand
	input        string
	outDir       string
	workers      int
	deleteSource bool
	deleteSctx   bool
	viewerAddr   string
	fingerprint  string
	opts         render.ExportOptions
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type assetOutputFlag map[string]string

func (m *assetOutputFlag) String() string {
	if len(*m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(*m))
	for key := range *m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+(*m)[key])
	}
	return strings.Join(parts, ",")
}

func (m *assetOutputFlag) Set(value string) error {
	name, path, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("asset-output must use NAME=PATH")
	}
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	if name == "" || path == "" {
		return fmt.Errorf("asset-output requires both NAME and PATH")
	}
	if *m == nil {
		*m = assetOutputFlag{}
	}
	if existing, ok := (*m)[name]; ok && existing != path {
		return fmt.Errorf("asset-output for %q already set to %q", name, existing)
	}
	(*m)[name] = path
	return nil
}

func parseCLI(args []string, stderr io.Writer) (cliConfig, error) {
	config := cliConfig{}
	command := cliCommand("")
	if len(args) > 0 && args[0] == "help" {
		writeUsage(stderr)
		return config, flag.ErrHelp
	}
	if len(args) > 0 {
		switch cliCommand(args[0]) {
		case commandExport, commandTextureRoot, commandSCRoot, commandSC3DViewer:
			command = cliCommand(args[0])
			args = args[1:]
		}
	}

	flags := flag.NewFlagSet("sc-export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeUsage(stderr)
		fmt.Fprintln(stderr, "\noptions:")
		flags.PrintDefaults()
	}
	outDir := flags.String("out", "", "output directory")
	workers := flags.Int("workers", runtime.NumCPU(), "global number of export workers; 0 uses the CPU count")
	fileConcurrency := flags.Int("file-concurrency", 1, "number of SC files to process concurrently")
	renderScale := flags.Int("render-scale", 1, "final output canvas scale")
	sceneryMaxDimension := flags.Int("scenery-max-dimension", 2048, "maximum scenery width or height; 0 keeps original resolution")
	sceneryFormat := flags.String("scenery-format", "auto", "animated scenery format: auto, hevc, or webp")
	hevcQuality := flags.Int("hevc-quality", 80, "HEVC video quality from 0 to 100")
	preferWebP := flags.Bool("prefer-webp", false, "write still images as WebP instead of PNG")
	webPQuality := flags.Int("webp-quality", 88, "WebP quality from 0 to 100")
	webPMethod := flags.Int("webp-method", 0, "WebP encoding method from 0 (fastest) to 6 (smallest)")
	disableGPU := flags.Bool("disable-gpu", false, "disable Metal rendering and use the CPU compositor")
	firstFrame := flags.Bool("first-frame", false, "render only the first frame of movie clips")
	lastFrame := flags.Bool("last-frame", false, "render only the last frame of movie clips")
	frameIndex := flags.Int("frame", 0, "render a specific 1-based frame of movie clips")
	staticOnly := flags.Bool("static-only", false, "render static layers while excluding animated child clips")
	preferredFrameLabel := flags.String("prefer-frame-label", "", "render the first available comma-separated frame label")
	baseSC := flags.String("base-sc", "", "load base assets from a companion SC file")
	profile := flags.Bool("profile", false, "print compact bottleneck timing summaries")
	profileTopN := flags.Int("profile-top-n", 5, "number of slowest targets to include when profiling")
	skipTinyThreshold := flags.Int("skip-tiny-threshold", 0, "skip outputs whose width and height are both <= this threshold")
	processImageRoot := flags.String("process-image-root", "", "legacy alias for the texture-root subcommand")
	processSCRoot := flags.String("process-sc-root", "", "legacy alias for the sc-root subcommand")
	viewerAddr := flags.String("addr", "127.0.0.1:4785", "SC3D viewer listen address")
	fingerprint := flags.String("fingerprint", "", "asset fingerprint for sc3d-viewer; defaults to FINGERPRINT")
	deleteSource := flags.Bool("delete-source", false, "delete each source only after its output is committed")
	deleteSctx := flags.Bool("delete-sctx", false, "delete .sctx sidecars after successful SC root processing")
	var includePrefixes stringSliceFlag
	var assetNames stringSliceFlag
	var assetOutputs assetOutputFlag
	var baseAssets assetOutputFlag
	flags.Var(&includePrefixes, "include-prefix", "limit SC root processing to top-level basenames with this prefix; repeatable")
	flags.Var(&assetNames, "asset", "limit export to a specific asset/export name; repeatable")
	flags.Var(&assetOutputs, "asset-output", "write a specific asset/export name to NAME=PATH; repeatable")
	flags.Var(&baseAssets, "base-asset", "render a base export beneath an asset using NAME=BASE; repeatable")
	if err := flags.Parse(flagsBeforePositionals(flags, args)); err != nil {
		return config, err
	}

	provided := map[string]bool{}
	flags.Visit(func(item *flag.Flag) { provided[item.Name] = true })
	if *workers < 0 {
		return config, fmt.Errorf("--workers must be 0 or greater")
	}
	if *fileConcurrency < 1 {
		return config, fmt.Errorf("--file-concurrency must be 1 or greater")
	}
	if *renderScale < 1 {
		return config, fmt.Errorf("--render-scale must be 1 or greater")
	}
	if *sceneryMaxDimension < 0 {
		return config, fmt.Errorf("--scenery-max-dimension must be 0 or greater")
	}
	*sceneryFormat = strings.ToLower(strings.TrimSpace(*sceneryFormat))
	if *sceneryFormat != "auto" && *sceneryFormat != "hevc" && *sceneryFormat != "webp" {
		return config, fmt.Errorf("--scenery-format must be auto, hevc, or webp")
	}
	if *hevcQuality < 0 || *hevcQuality > 100 {
		return config, fmt.Errorf("--hevc-quality must be between 0 and 100")
	}
	if *webPQuality < 0 || *webPQuality > 100 {
		return config, fmt.Errorf("--webp-quality must be between 0 and 100")
	}
	if *webPMethod < 0 || *webPMethod > 6 {
		return config, fmt.Errorf("--webp-method must be between 0 and 6")
	}
	if *frameIndex < 0 {
		return config, fmt.Errorf("--frame must be 1 or greater")
	}
	if *frameIndex > 0 && (*firstFrame || *lastFrame) {
		return config, fmt.Errorf("--frame cannot be combined with --first-frame or --last-frame")
	}
	if *staticOnly && (*firstFrame || *lastFrame || *frameIndex > 0) {
		return config, fmt.Errorf("--static-only cannot be combined with --first-frame, --last-frame, or --frame")
	}
	if *profileTopN < 1 {
		return config, fmt.Errorf("--profile-top-n must be 1 or greater")
	}
	if *skipTinyThreshold < 0 {
		return config, fmt.Errorf("--skip-tiny-threshold must be 0 or greater")
	}

	config.outDir = *outDir
	config.workers = *workers
	config.deleteSource = *deleteSource
	config.deleteSctx = *deleteSctx
	config.viewerAddr = *viewerAddr
	config.fingerprint = *fingerprint
	config.opts = render.ExportOptions{
		RenderScale:             *renderScale,
		SceneryMaxDimension:     *sceneryMaxDimension,
		SceneryMaxDimensionSet:  provided["scenery-max-dimension"],
		SceneryFormat:           *sceneryFormat,
		HEVCQuality:             *hevcQuality,
		HEVCQualitySet:          provided["hevc-quality"],
		IncludePrefixes:         includePrefixes,
		AssetNames:              assetNames,
		AssetOutputPaths:        map[string]string(assetOutputs),
		AssetBaseNames:          map[string]string(baseAssets),
		BaseSCPath:              *baseSC,
		PreferWebP:              *preferWebP,
		WebPQuality:             *webPQuality,
		WebPQualitySet:          provided["webp-quality"],
		WebPMethod:              *webPMethod,
		DisableGPU:              *disableGPU,
		FirstFrameOnly:          *firstFrame,
		LastFrameOnly:           *lastFrame,
		FrameIndex:              *frameIndex,
		StaticOnly:              *staticOnly,
		PreferredFrameLabel:     *preferredFrameLabel,
		FileConcurrency:         *fileConcurrency,
		Profile:                 *profile,
		ProfileTopN:             *profileTopN,
		SkipTinyOutputThreshold: *skipTinyThreshold,
	}

	if command == "" {
		if *processImageRoot != "" && *processSCRoot != "" {
			return config, fmt.Errorf("--process-image-root and --process-sc-root cannot be combined")
		}
		switch {
		case *processImageRoot != "":
			command = commandTextureRoot
			config.input = *processImageRoot
		case *processSCRoot != "":
			command = commandSCRoot
			config.input = *processSCRoot
		default:
			command = commandExport
			if flags.NArg() != 1 {
				return config, fmt.Errorf("export requires exactly one input path")
			}
			config.input = flags.Arg(0)
		}
	} else if command == commandSC3DViewer {
		if flags.NArg() != 0 {
			return config, fmt.Errorf("sc3d-viewer does not accept an input path")
		}
	} else {
		if provided["process-image-root"] || provided["process-sc-root"] {
			return config, fmt.Errorf("legacy --process-*-root flags cannot be combined with a subcommand")
		}
		if flags.NArg() != 1 {
			return config, fmt.Errorf("%s requires exactly one input path", command)
		}
		config.input = flags.Arg(0)
	}
	config.command = command

	if command == commandExport && (provided["delete-source"] || provided["delete-sctx"] || provided["file-concurrency"] || provided["include-prefix"]) {
		return config, fmt.Errorf("root-processing flags cannot be used with export")
	}
	if command == commandTextureRoot && (provided["out"] || provided["file-concurrency"] || provided["include-prefix"] || provided["delete-sctx"] || provided["asset"] || provided["asset-output"] || provided["base-asset"] || provided["base-sc"] || provided["first-frame"] || provided["last-frame"] || provided["frame"] || provided["static-only"] || provided["prefer-frame-label"]) {
		return config, fmt.Errorf("SC export flags cannot be used with texture-root")
	}
	if command == commandSCRoot && provided["out"] {
		return config, fmt.Errorf("--out cannot be used with sc-root; each .sc file is replaced beside its source")
	}
	if command == commandSC3DViewer {
		allowed := map[string]bool{"addr": true, "fingerprint": true}
		for name := range provided {
			if !allowed[name] {
				return config, fmt.Errorf("--%s cannot be used with sc3d-viewer", name)
			}
		}
	}
	return config, nil
}

func flagsBeforePositionals(flags *flag.FlagSet, args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			positionals = append(positionals, args[index+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if before, _, hasValue := strings.Cut(name, "="); hasValue {
			name = before
			continue
		}
		item := flags.Lookup(name)
		if item == nil {
			continue
		}
		if boolean, ok := item.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
			continue
		}
		if index+1 < len(args) {
			index++
			flagArgs = append(flagArgs, args[index])
		}
	}
	return append(flagArgs, positionals...)
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  sc-export export [flags] INPUT")
	fmt.Fprintln(w, "  sc-export texture-root [flags] ROOT")
	fmt.Fprintln(w, "  sc-export sc-root [flags] ROOT")
	fmt.Fprintln(w, "  sc-export sc3d-viewer [--fingerprint SHA] [--addr HOST:PORT]")
	fmt.Fprintln(w, "\nRun sc-export <subcommand> --help to list flags.")
}

func executeCLI(config cliConfig) error {
	switch config.command {
	case commandTextureRoot:
		return render.ProcessImageRoot(config.input, config.workers, config.opts, config.deleteSource)
	case commandSCRoot:
		return render.ProcessSCRoot(config.input, config.workers, config.opts, config.deleteSource, config.deleteSctx)
	case commandSC3DViewer:
		return sc3d.ServeViewer(config.viewerAddr, config.fingerprint)
	default:
		return render.ExportPath(config.input, config.outDir, config.workers, config.opts)
	}
}

func main() {
	config, err := parseCLI(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		writeUsage(os.Stderr)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config.opts.Context = ctx
	if err := executeCLI(config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
