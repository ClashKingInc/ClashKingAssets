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
		files = append(files, filepath.Join(root, name))
	}
	sort.Strings(files)

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if len(files) == 0 {
		if deleteSctx {
			return deleteSCRootSctx(root)
		}
		return nil
	}

	fileConcurrency := workers
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
	results := make(chan error, len(files))
	var wg sync.WaitGroup

	for i := 0; i < fileConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for source := range jobs {
				outputDir := strings.TrimSuffix(source, filepath.Ext(source))
				manifestPath := filepath.Join(outputDir, "manifest.json")
				if _, err := os.Stat(manifestPath); err == nil {
					if deleteSource {
						_ = os.Remove(source)
					}
					results <- nil
					continue
				}
				if _, err := os.Stat(outputDir); err == nil {
					if err := os.RemoveAll(outputDir); err != nil {
						results <- fmt.Errorf("%s: %w", outputDir, err)
						continue
					}
				}
				stats, err := exportSingle(source, outputDir, perFileWorkers, opts)
				if err != nil {
					results <- fmt.Errorf("%s: %w", source, err)
					continue
				}
				printAssetStats(stats)
				if deleteSource {
					if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
						results <- fmt.Errorf("%s: %w", source, err)
						continue
					}
				}
				results <- nil
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
	for err := range results {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		processed++
		if processed == len(files) || processed%10 == 0 {
			fmt.Printf("  Progress: %d/%d\n", processed, len(files))
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if deleteSctx {
		return deleteSCRootSctx(root)
	}
	return nil
}

func deleteSCRootSctx(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".sctx") {
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
