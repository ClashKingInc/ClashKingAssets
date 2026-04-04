package render

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

var exportSingleFunc = exportSingle

type scRootResult struct {
	source string
	err    error
}

func ProcessImageRoot(root string, workers int, opts ExportOptions, deleteSource bool) error {
	files := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".sctx") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return err
	}

	sort.Strings(files)
	if len(files) == 0 {
		return nil
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(files) {
		workers = len(files)
	}
	fmt.Printf("Processing image root %s\n", root)
	fmt.Printf("  Files:   %d\n", len(files))
	fmt.Printf("  Workers: %d\n", workers)

	jobs := make(chan string)
	results := make(chan error, len(files))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				outputPath := filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".png")
				if _, err := os.Stat(outputPath); err == nil {
					if deleteSource {
						_ = os.Remove(path)
					}
					results <- nil
					continue
				}
				if err := exportTextureFile(path, filepath.Dir(path), opts); err != nil {
					results <- fmt.Errorf("%s: %w", path, err)
					continue
				}
				if deleteSource {
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						results <- fmt.Errorf("%s: %w", path, err)
						continue
					}
				}
				results <- nil
			}
		}()
	}

	go func() {
		for _, path := range files {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var firstErr error
	processed := 0
	for err := range results {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		processed++
		if processed == len(files) || processed%50 == 0 {
			fmt.Printf("  Progress: %d/%d\n", processed, len(files))
		}
	}
	return firstErr
}

func ProcessSCRoot(root string, workers int, opts ExportOptions, deleteSource, deleteSctx bool) error {
	opts = normalizeExportOptions(opts)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sc") || strings.HasSuffix(name, "_tex.sc") {
			continue
		}
		if !matchesIncludePrefix(name, opts.IncludePrefixes) {
			continue
		}
		files = append(files, filepath.Join(root, name))
	}
	sort.Slice(files, func(i, j int) bool {
		leftBase := filepath.Base(files[i])
		rightBase := filepath.Base(files[j])
		if leftBase == "ui.sc" && rightBase != "ui.sc" {
			return true
		}
		if rightBase == "ui.sc" && leftBase != "ui.sc" {
			return false
		}
		return files[i] < files[j]
	})

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if len(files) == 0 {
		if deleteSctx {
			return deleteSCRootSctx(root, opts.IncludePrefixes)
		}
		return nil
	}

	fileConcurrency := opts.FileConcurrency
	if fileConcurrency > len(files) {
		fileConcurrency = len(files)
	}
	if fileConcurrency < 1 {
		fileConcurrency = 1
	}
	perFileWorkers := workers / fileConcurrency
	if perFileWorkers < 1 {
		perFileWorkers = 1
	}
	fmt.Printf("Processing SC root %s\n", root)
	fmt.Printf("  Files:       %d\n", len(files))
	fmt.Printf("  Concurrency: %d files x %d workers\n", fileConcurrency, perFileWorkers)

	jobs := make(chan string)
	results := make(chan scRootResult, len(files))
	var wg sync.WaitGroup

	for i := 0; i < fileConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for source := range jobs {
				outputDir := strings.TrimSuffix(source, filepath.Ext(source))
				if _, err := os.Stat(outputDir); err == nil {
					if err := os.RemoveAll(outputDir); err != nil {
						results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", outputDir, err)}
						continue
					}
				}
				stats, err := exportSingleFunc(source, outputDir, perFileWorkers, opts)
				if err != nil {
					results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", source, err)}
					continue
				}
				fmt.Printf("\n[SC] Done: %s\n", source)
				printAssetStats(stats)
				runtime.GC()
				debug.FreeOSMemory()
				if deleteSource {
					if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
						results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", source, err)}
						continue
					}
				}
				results <- scRootResult{source: source}
			}
		}()
	}

	go func() {
		for _, source := range files {
			jobs <- source
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var firstErr error
	processed := 0
	successes := 0
	failures := 0
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			failures++
			fmt.Printf("\n[SC] Failed: %s\n  Error: %v\n", result.source, result.err)
		} else {
			successes++
		}
		processed++
		if processed == len(files) || processed%10 == 0 {
			fmt.Printf("  Progress: %d/%d (ok=%d failed=%d)\n", processed, len(files), successes, failures)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if deleteSctx {
		return deleteSCRootSctx(root, opts.IncludePrefixes)
	}
	return nil
}

func deleteSCRootSctx(root string, includePrefixes []string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.EqualFold(filepath.Ext(name), ".sctx") && matchesIncludePrefix(name, includePrefixes) {
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func matchesIncludePrefix(name string, includePrefixes []string) bool {
	if len(includePrefixes) == 0 {
		return true
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for _, prefix := range includePrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
