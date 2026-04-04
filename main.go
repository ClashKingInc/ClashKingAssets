package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"sc2fla/internal/render"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	outDir := flag.String("out", "", "output directory")
	workers := flag.Int("workers", runtime.NumCPU(), "number of export workers")
	fileConcurrency := flag.Int("file-concurrency", 1, "number of SC files to process concurrently for --process-sc-root")
	renderScale := flag.Int("render-scale", 1, "final output canvas scale")
	profile := flag.Bool("profile", false, "print compact bottleneck timing summaries")
	profileTopN := flag.Int("profile-top-n", 5, "number of slowest targets to include when --profile is enabled")
	skipTinyThreshold := flag.Int("skip-tiny-threshold", 0, "skip writing outputs whose width and height are both <= this threshold")
	processImageRoot := flag.String("process-image-root", "", "process all .sctx files under a root directory")
	processSCRoot := flag.String("process-sc-root", "", "process all top-level .sc files inside a root directory")
	deleteSource := flag.Bool("delete-source", false, "delete source files after successful processing")
	deleteSctx := flag.Bool("delete-sctx", false, "delete .sctx sidecars after SC root processing")
	var includePrefixes stringSliceFlag
	flag.Var(&includePrefixes, "include-prefix", "limit SC root processing to top-level basenames with this prefix; repeatable")
	flag.Parse()

	opts := render.ExportOptions{
		RenderScale:             *renderScale,
		IncludePrefixes:         includePrefixes,
		FileConcurrency:         *fileConcurrency,
		Profile:                 *profile,
		ProfileTopN:             *profileTopN,
		SkipTinyOutputThreshold: *skipTinyThreshold,
	}

	var err error
	switch {
	case *processImageRoot != "":
		err = render.ProcessImageRoot(*processImageRoot, *workers, opts, *deleteSource)
	case *processSCRoot != "":
		err = render.ProcessSCRoot(*processSCRoot, *workers, opts, *deleteSource, *deleteSctx)
	default:
		if flag.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: sc-export <file-or-dir> [--out DIR] [--workers N] [--render-scale N]")
			fmt.Fprintln(os.Stderr, "   or: sc-export --process-image-root DIR [--workers N] [--render-scale N] [--profile] [--profile-top-n N] [--delete-source]")
			fmt.Fprintln(os.Stderr, "   or: sc-export --process-sc-root DIR [--workers N] [--file-concurrency N] [--render-scale N] [--skip-tiny-threshold N] [--profile] [--profile-top-n N] [--include-prefix PREFIX] [--delete-source] [--delete-sctx]")
			os.Exit(2)
		}
		input := flag.Arg(0)
		err = render.ExportPath(input, *outDir, *workers, opts)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
