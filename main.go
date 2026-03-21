package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"sc2fla/internal/render"
)

func main() {
	outDir := flag.String("out", "", "output directory")
	workers := flag.Int("workers", runtime.NumCPU(), "number of export workers")
	renderScale := flag.Int("render-scale", 1, "final output canvas scale")
	processImageRoot := flag.String("process-image-root", "", "process all .sctx files under a root directory")
	processSCRoot := flag.String("process-sc-root", "", "process all top-level .sc files inside a root directory")
	deleteSource := flag.Bool("delete-source", false, "delete source files after successful processing")
	deleteSctx := flag.Bool("delete-sctx", false, "delete .sctx sidecars after SC root processing")
	flag.Parse()

	opts := render.ExportOptions{
		RenderScale: *renderScale,
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
			fmt.Fprintln(os.Stderr, "   or: sc-export --process-image-root DIR [--workers N] [--render-scale N] [--delete-source]")
			fmt.Fprintln(os.Stderr, "   or: sc-export --process-sc-root DIR [--workers N] [--render-scale N] [--delete-source] [--delete-sctx]")
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
