package sc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSCVersion(t *testing.T) {
	t.Parallel()

	if got := DetectSCVersion([]byte{'S', 'C', 0, 0, 0, 3}); got != 3 {
		t.Fatalf("big-endian v3 detected as %d", got)
	}
	if got := DetectSCVersion([]byte{'S', 'C', 6, 0, 0, 0}); got != 6 {
		t.Fatalf("little-endian v6 detected as %d", got)
	}
	if got := DetectSCVersion([]byte("not-sc")); got != 0 {
		t.Fatalf("invalid header detected as version %d", got)
	}
}

func TestKnownAssetWrapperVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path    string
		version int
	}{
		{path: "../../sc/chr_dragon.sc", version: 6},
		{path: "../../sc/chr_archer.sc", version: 6},
		{path: "../../sc/chr_barbarian.sc", version: 6},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(tc.path); err != nil {
				if os.IsNotExist(err) {
					t.Skipf("fixture not present: %s", tc.path)
				}
				t.Fatalf("Stat(%s) failed: %v", tc.path, err)
			}

			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("ReadFile(%s) failed: %v", tc.path, err)
			}
			if got := DetectSCVersion(raw); got != tc.version {
				t.Fatalf("DetectSCVersion(%s) = %d, want %d", tc.path, got, tc.version)
			}
		})
	}
}

func TestBundleAssetsIncludesSidecars(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := []string{
		"chr_archer.sc",
		"chr_archer_0.sctx",
		"chr_archer_tex.sc",
		"chr_archer2.sc",
		"chr_archer_notes.txt",
		"other.sc",
	}
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", path, err)
		}
	}

	got, err := bundleAssets(filepath.Join(dir, "chr_archer.sc"))
	if err != nil {
		t.Fatalf("bundleAssets failed: %v", err)
	}

	want := []string{
		filepath.Join(dir, "chr_archer.sc"),
		filepath.Join(dir, "chr_archer_0.sctx"),
		filepath.Join(dir, "chr_archer_tex.sc"),
	}
	if len(got) != len(want) {
		t.Fatalf("bundleAssets count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bundleAssets[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestBundleAssetsExcludesPrefixNeighbors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := []string{
		"chr_archer.sc",
		"chr_archer_tex.sc",
		"chr_archer_0.sctx",
		"chr_archer2.sc",
		"chr_archer2_0.sctx",
	}
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", path, err)
		}
	}

	got, err := bundleAssets(filepath.Join(dir, "chr_archer.sc"))
	if err != nil {
		t.Fatalf("bundleAssets failed: %v", err)
	}

	want := []string{
		filepath.Join(dir, "chr_archer.sc"),
		filepath.Join(dir, "chr_archer_0.sctx"),
		filepath.Join(dir, "chr_archer_tex.sc"),
	}
	if len(got) != len(want) {
		t.Fatalf("bundleAssets count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bundleAssets[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestSampleParserParity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path        string
		resources   int
		movieclips  int
		exports     int
		namedBinds  int
		namedFrames int
	}{
		{path: "../../sc/chr_dragon.sc", resources: 251, movieclips: 80, exports: 39, namedBinds: 39, namedFrames: 0},
		{path: "../../sc/chr_battle_blimp.sc", resources: 340, movieclips: 195, exports: 145, namedBinds: 0, namedFrames: 0},
		{path: "../../sc/loading.sc", resources: 120, movieclips: 61, exports: 28, namedBinds: 78, namedFrames: 8},
		{path: "../../sc/ui.sc", resources: 14347, movieclips: 6046, exports: 2885, namedBinds: 15022, namedFrames: 1432},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(tc.path); err != nil {
				if os.IsNotExist(err) {
					t.Skipf("fixture not present: %s", tc.path)
				}
				t.Fatalf("Stat(%s) failed: %v", tc.path, err)
			}

			swf, err := Load(tc.path)
			if err != nil {
				t.Fatalf("Load(%s) failed: %v", tc.path, err)
			}

			movieclips := 0
			namedBinds := 0
			namedFrames := 0
			for _, resource := range swf.Resources {
				clip, ok := resource.(*MovieClip)
				if !ok {
					continue
				}
				movieclips++
				for _, bind := range clip.Binds {
					if bind.Name != "" {
						namedBinds++
					}
				}
				for _, frame := range clip.Frames {
					if frame.Name != "" {
						namedFrames++
					}
				}
			}

			if got := len(swf.Resources); got != tc.resources {
				t.Fatalf("resource count = %d, want %d", got, tc.resources)
			}
			if movieclips != tc.movieclips {
				t.Fatalf("movieclip count = %d, want %d", movieclips, tc.movieclips)
			}
			if got := len(swf.Exports); got != tc.exports {
				t.Fatalf("export count = %d, want %d", got, tc.exports)
			}
			if namedBinds != tc.namedBinds {
				t.Fatalf("named bind count = %d, want %d", namedBinds, tc.namedBinds)
			}
			if namedFrames != tc.namedFrames {
				t.Fatalf("named frame count = %d, want %d", namedFrames, tc.namedFrames)
			}
		})
	}
}
