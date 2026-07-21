package render

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var exportSingleFunc = exportSingle
var exportTextureFileFunc = exportTextureFile

type scRootResult struct {
	source string
	err    error
}

func ProcessImageRoot(root string, workers int, opts ExportOptions, deleteSource bool) error {
	opts = normalizeExportOptions(opts)
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
	done := make(chan struct{})
	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(func() { close(done) })
	}
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				var path string
				var ok bool
				select {
				case <-done:
					return
				case path, ok = <-jobs:
					if !ok {
						return
					}
				}

				outputBase, outputPath := imageRootOutputPaths(path, opts)
				committed, err := committedOutputExists(outputPath)
				if err != nil {
					results <- fmt.Errorf("%s: %w", outputPath, err)
					cancel()
					return
				}
				if committed {
					if deleteSource {
						if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
							results <- fmt.Errorf("%s: %w", path, err)
							cancel()
							return
						}
					}
					results <- nil
					continue
				}
				if err := exportTextureToStaging(path, outputBase, outputPath, opts); err != nil {
					results <- fmt.Errorf("%s: %w", path, err)
					cancel()
					return
				}
				if err := validateCommittedOutput(outputPath); err != nil {
					results <- fmt.Errorf("%s did not produce %s: %w", path, outputPath, err)
					cancel()
					return
				}
				if deleteSource {
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						results <- fmt.Errorf("%s: %w", path, err)
						cancel()
						return
					}
				}
				results <- nil
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, path := range files {
			select {
			case <-done:
				return
			case jobs <- path:
			}
		}
	}()
	go func() {
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

func imageRootOutputPaths(inputPath string, opts ExportOptions) (outputBase, outputPath string) {
	stem := strings.TrimSuffix(inputPath, filepath.Ext(inputPath))
	if opts.PreferWebP {
		return stem, stem + ".webp"
	}
	return filepath.Dir(inputPath), stem + ".png"
}

func validateCommittedOutput(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("output is not a regular file")
	}
	if info.Size() == 0 {
		return fmt.Errorf("output is empty")
	}
	return nil
}

func committedOutputExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("output is not a regular file")
	}
	return info.Size() > 0, nil
}

func exportTextureToStaging(inputPath, outputBase, outputPath string, opts ExportOptions) error {
	stagingDir, err := os.MkdirTemp(filepath.Dir(outputPath), "."+filepath.Base(outputBase)+".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)

	stagingOutput := filepath.Join(stagingDir, filepath.Base(outputPath))
	stagingBase := stagingDir
	if opts.PreferWebP {
		stagingBase = strings.TrimSuffix(stagingOutput, filepath.Ext(stagingOutput))
	}
	if err := exportTextureFileFunc(inputPath, stagingBase, opts); err != nil {
		return err
	}
	if err := validateCommittedOutput(stagingOutput); err != nil {
		return fmt.Errorf("did not produce staged output %s: %w", stagingOutput, err)
	}
	if err := os.Rename(stagingOutput, outputPath); err != nil {
		return fmt.Errorf("commit %s: %w", outputPath, err)
	}
	return nil
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
	sort.Strings(files)

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if len(files) == 0 {
		if deleteSctx {
			return deleteSCRootSctx(root, opts.IncludePrefixes)
		}
		return nil
	}

	fileConcurrency := opts.FileConcurrency
	if fileConcurrency > workers {
		fileConcurrency = workers
	}
	if fileConcurrency > len(files) {
		fileConcurrency = len(files)
	}
	if fileConcurrency < 1 {
		fileConcurrency = 1
	}
	workerBudgets := make([]int, fileConcurrency)
	for i := range workerBudgets {
		workerBudgets[i] = workers / fileConcurrency
		if i < workers%fileConcurrency {
			workerBudgets[i]++
		}
	}
	fmt.Printf("Processing SC root %s\n", root)
	fmt.Printf("  Files:       %d\n", len(files))
	fmt.Printf("  Concurrency: %d files, %d total workers\n", fileConcurrency, workers)

	jobs := make(chan string)
	results := make(chan scRootResult, len(files))
	done := make(chan struct{})
	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(func() { close(done) })
	}
	var wg sync.WaitGroup

	for i := 0; i < fileConcurrency; i++ {
		fileWorkers := workerBudgets[i]
		wg.Add(1)
		go func(perFileWorkers int) {
			defer wg.Done()
			for {
				var source string
				var ok bool
				select {
				case <-done:
					return
				case source, ok = <-jobs:
					if !ok {
						return
					}
				}

				outputDir := strings.TrimSuffix(source, filepath.Ext(source))
				stagingDir, err := os.MkdirTemp(filepath.Dir(outputDir), "."+filepath.Base(outputDir)+".staging-")
				if err != nil {
					results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", outputDir, err)}
					cancel()
					return
				}
				stats, err := exportSingleFunc(source, stagingDir, perFileWorkers, opts)
				if err != nil {
					_ = os.RemoveAll(stagingDir)
					results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", source, err)}
					cancel()
					return
				}
				if err := replaceDirectory(stagingDir, outputDir); err != nil {
					_ = os.RemoveAll(stagingDir)
					results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", outputDir, err)}
					cancel()
					return
				}
				stats.AssetDir = outputDir
				stats.ExportsDir = outputDir
				stats.ManifestPath = filepath.Join(outputDir, "manifest.json")
				fmt.Printf("\n[SC] Done: %s\n", source)
				printAssetStats(stats)
				if deleteSource {
					if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
						results <- scRootResult{source: source, err: fmt.Errorf("%s: %w", source, err)}
						cancel()
						return
					}
				}
				results <- scRootResult{source: source}
			}
		}(fileWorkers)
	}

	go func() {
		defer close(jobs)
		for _, source := range files {
			select {
			case <-done:
				return
			case jobs <- source:
			}
		}
	}()
	go func() {
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

func replaceDirectory(stagingDir, outputDir string) error {
	backupDir := ""
	if _, err := os.Lstat(outputDir); err == nil {
		placeholder, err := os.MkdirTemp(filepath.Dir(outputDir), "."+filepath.Base(outputDir)+".backup-")
		if err != nil {
			return err
		}
		if err := os.Remove(placeholder); err != nil {
			return err
		}
		backupDir = placeholder
		if err := os.Rename(outputDir, backupDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(stagingDir, outputDir); err != nil {
		if backupDir != "" {
			if restoreErr := os.Rename(backupDir, outputDir); restoreErr != nil {
				return fmt.Errorf("commit failed: %v; restoring previous output failed: %w", err, restoreErr)
			}
		}
		return err
	}
	if backupDir != "" {
		if err := os.RemoveAll(backupDir); err != nil {
			return fmt.Errorf("replacement committed but old output cleanup failed: %w", err)
		}
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
