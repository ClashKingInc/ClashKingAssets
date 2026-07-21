package render

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessImageRootMapsAndVerifiesEveryWebPBeforeDeletingSources(t *testing.T) {
	root := t.TempDir()
	inputs := []string{
		filepath.Join(root, "one.sctx"),
		filepath.Join(root, "two.sctx"),
	}
	for _, input := range inputs {
		if err := os.WriteFile(input, []byte("texture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	original := exportTextureFileFunc
	defer func() { exportTextureFileFunc = original }()
	var mu sync.Mutex
	outputs := make([]string, 0, len(inputs))
	exportTextureFileFunc = func(inputPath, outputBase string, opts ExportOptions) error {
		mu.Lock()
		outputs = append(outputs, outputBase+".webp")
		mu.Unlock()
		return os.WriteFile(outputBase+".webp", []byte("WEBP"), 0o644)
	}

	if err := ProcessImageRoot(root, 2, ExportOptions{PreferWebP: true}, true); err != nil {
		t.Fatalf("ProcessImageRoot failed: %v", err)
	}
	for _, input := range inputs {
		if _, err := os.Stat(input); !os.IsNotExist(err) {
			t.Fatalf("source should be deleted after its WebP exists: %s (err=%v)", input, err)
		}
		if _, err := os.Stat(input[:len(input)-len(filepath.Ext(input))] + ".webp"); err != nil {
			t.Fatalf("expected per-input WebP: %v", err)
		}
	}
	if len(outputs) != len(inputs) {
		t.Fatalf("got %d outputs, want %d", len(outputs), len(inputs))
	}
}

func TestProcessImageRootKeepsSourceWhenExporterDoesNotCommitOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "missing.sctx")
	if err := os.WriteFile(input, []byte("texture"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := exportTextureFileFunc
	defer func() { exportTextureFileFunc = original }()
	exportTextureFileFunc = func(string, string, ExportOptions) error { return nil }

	err := ProcessImageRoot(root, 1, ExportOptions{PreferWebP: true}, true)
	if err == nil {
		t.Fatal("expected missing committed output error")
	}
	if _, statErr := os.Stat(input); statErr != nil {
		t.Fatalf("source must remain when output is missing: %v", statErr)
	}
}

func TestProcessImageRootReplacesAnEmptyOutputInsteadOfTrustingIt(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "empty.sctx")
	output := filepath.Join(root, "empty.webp")
	if err := os.WriteFile(input, []byte("texture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	original := exportTextureFileFunc
	defer func() { exportTextureFileFunc = original }()
	calls := 0
	exportTextureFileFunc = func(inputPath, outputBase string, opts ExportOptions) error {
		calls++
		return os.WriteFile(outputBase+".webp", []byte("WEBP"), 0o644)
	}

	if err := ProcessImageRoot(root, 1, ExportOptions{PreferWebP: true}, false); err != nil {
		t.Fatalf("ProcessImageRoot failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("exporter calls = %d, want 1", calls)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		t.Fatalf("empty output was not replaced: info=%v err=%v", info, err)
	}
}

func TestProcessImageRootDoesNotCommitPartialOutputOnExporterFailure(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "partial.sctx")
	output := filepath.Join(root, "partial.webp")
	if err := os.WriteFile(input, []byte("texture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	original := exportTextureFileFunc
	defer func() { exportTextureFileFunc = original }()
	exportTextureFileFunc = func(inputPath, outputBase string, opts ExportOptions) error {
		if err := os.WriteFile(outputBase+".webp", []byte("partial"), 0o644); err != nil {
			return err
		}
		return errors.New("encode failed")
	}

	err := ProcessImageRoot(root, 1, ExportOptions{PreferWebP: true}, false)
	if err == nil {
		t.Fatal("expected encoder error")
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("read previous output: %v", readErr)
	}
	if len(data) != 0 {
		t.Fatalf("failed export committed %q", data)
	}
}

func TestProcessSCRootPreservesExistingOutputWhenReplacementFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ui.sc")
	outputDir := filepath.Join(root, "ui")
	if err := os.WriteFile(source, []byte("sc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldOutput := filepath.Join(outputDir, "known-good.txt")
	if err := os.WriteFile(oldOutput, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := exportSingleFunc
	defer func() { exportSingleFunc = original }()
	exportSingleFunc = func(source, output string, workers int, opts ExportOptions) (AssetStats, error) {
		return AssetStats{}, errors.New("render failed")
	}

	if err := ProcessSCRoot(root, 1, ExportOptions{}, false, false); err == nil {
		t.Fatal("expected render error")
	}
	data, err := os.ReadFile(oldOutput)
	if err != nil {
		t.Fatalf("known-good output was removed: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("known-good output changed to %q", data)
	}
}

func TestProcessSCRootCommitsCompleteReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ui.sc")
	outputDir := filepath.Join(root, "ui")
	if err := os.WriteFile(source, []byte("sc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := exportSingleFunc
	defer func() { exportSingleFunc = original }()
	exportSingleFunc = func(source, output string, workers int, opts ExportOptions) (AssetStats, error) {
		if err := os.WriteFile(filepath.Join(output, "new.txt"), []byte("new"), 0o644); err != nil {
			return AssetStats{}, err
		}
		return AssetStats{}, nil
	}

	if err := ProcessSCRoot(root, 1, ExportOptions{}, false, false); err != nil {
		t.Fatalf("ProcessSCRoot failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "new.txt")); err != nil {
		t.Fatalf("replacement output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old output should be removed only after commit, err=%v", err)
	}
}

func TestProcessSCRootStopsSchedulingAfterFailure(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.sc", "b.sc", "c.sc"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	original := exportSingleFunc
	defer func() { exportSingleFunc = original }()
	calls := 0
	exportSingleFunc = func(source, output string, workers int, opts ExportOptions) (AssetStats, error) {
		calls++
		return AssetStats{}, errors.New("render failed")
	}

	if err := ProcessSCRoot(root, 1, ExportOptions{FileConcurrency: 1}, false, false); err == nil {
		t.Fatal("expected render error")
	}
	if calls != 1 {
		t.Fatalf("scheduled %d files after the first failure, want 1", calls)
	}
}

func TestProcessSCRootTreatsWorkersAsGlobalBudget(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.sc", "b.sc", "c.sc", "d.sc"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	original := exportSingleFunc
	defer func() { exportSingleFunc = original }()
	var active atomic.Int32
	var maximum atomic.Int32
	exportSingleFunc = func(source, output string, workers int, opts ExportOptions) (AssetStats, error) {
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		if err := os.WriteFile(filepath.Join(output, "result.txt"), []byte("new"), 0o644); err != nil {
			return AssetStats{}, err
		}
		return AssetStats{}, nil
	}

	if err := ProcessSCRoot(root, 2, ExportOptions{FileConcurrency: 4}, false, false); err != nil {
		t.Fatalf("ProcessSCRoot failed: %v", err)
	}
	if got := maximum.Load(); got > 2 {
		t.Fatalf("started %d concurrent files with a global worker budget of 2", got)
	}
}

func TestProcessSCRootUsesRemainderOfGlobalWorkerBudget(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.sc", "b.sc"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	original := exportSingleFunc
	defer func() { exportSingleFunc = original }()
	budgets := make(chan int, 2)
	exportSingleFunc = func(source, output string, workers int, opts ExportOptions) (AssetStats, error) {
		budgets <- workers
		if err := os.WriteFile(filepath.Join(output, "result.txt"), []byte("new"), 0o644); err != nil {
			return AssetStats{}, err
		}
		return AssetStats{}, nil
	}

	if err := ProcessSCRoot(root, 5, ExportOptions{FileConcurrency: 2}, false, false); err != nil {
		t.Fatalf("ProcessSCRoot failed: %v", err)
	}
	close(budgets)
	got := make([]int, 0, 2)
	for budget := range budgets {
		got = append(got, budget)
	}
	sort.Ints(got)
	if got[0] != 2 || got[1] != 3 {
		t.Fatalf("per-file worker budgets = %v, want [2 3]", got)
	}
}
