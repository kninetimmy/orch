package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func evalDriveAlias(t *testing.T, target string) string {
	t.Helper()
	for _, letter := range "ZYXWVUTSRQPONMLKJIHGFED" {
		drive := string(letter) + ":"
		if _, err := os.Stat(drive + `\`); !os.IsNotExist(err) {
			continue
		}
		cmd := exec.CommandContext(t.Context(), "subst", drive, target)
		if _, err := cmd.CombinedOutput(); err != nil {
			continue // SUBST refuses occupied drives; never replace a mapping.
		}
		t.Cleanup(func() {
			aliasInfo, aliasErr := os.Stat(drive + `\`)
			targetInfo, targetErr := os.Stat(target)
			if aliasErr != nil || targetErr != nil || !os.SameFile(aliasInfo, targetInfo) {
				t.Errorf("refuse to remove changed/unverifiable test drive mapping %s", drive)
				return
			}
			if output, err := exec.Command("subst", drive, "/d").CombinedOutput(); err != nil {
				t.Errorf("remove owned test drive mapping %s: %v: %s", drive, err, output)
			}
		})
		return drive + `\`
	}
	t.Fatal("no unused local drive letter available for SUBST regression")
	return ""
}

// Real drive mappings must be refused on either side of a placement exclusion,
// including future locations whose final components do not exist yet.
func TestEvalPreviewRejectsWindowsDriveAlias(t *testing.T) {
	for _, scenario := range []string{"storage-root", "storage-descendant", "worker-exclusion", "scratch-exclusion", "absent-worker-descendant", "profile-artifact"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEvalFixture(t)
			workers, storage := f.plan.WorkerRoots[0], f.plan.StorageRoot
			alias := evalDriveAlias(t, workers)
			switch scenario {
			case "storage-root":
				f.plan.StorageRoot = alias
			case "storage-descendant":
				if err := os.Mkdir(filepath.Join(workers, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				f.plan.StorageRoot = filepath.Join(alias, "nested")
			case "worker-exclusion":
				f.plan.StorageRoot, f.plan.WorkerRoots = workers, []string{alias}
			case "scratch-exclusion":
				f.plan.StorageRoot, f.plan.WorkerRoots, f.plan.ScratchRoots = workers, f.plan.ScratchRoots, []string{alias}
			case "absent-worker-descendant":
				f.plan.StorageRoot, f.plan.WorkerRoots = workers, []string{filepath.Join(alias, "future", "worker")}
			case "profile-artifact":
				data, err := os.ReadFile(filepath.Join(filepath.Dir(f.file), f.plan.Baseline.Profile.Path))
				if err != nil {
					t.Fatal(err)
				}
				evalWrite(t, filepath.Join(workers, "profile.toml"), data)
				f.plan.Baseline.Profile.Path = filepath.Join(alias, "profile.toml")
			}
			f.write(t)
			code, output, failure := f.run(t, "--json")
			if code != ExitError || output != "" || !strings.Contains(failure, "drive alias") {
				t.Fatalf("drive alias accepted or wrong refusal: code=%d, failure=%s", code, failure)
			}
			for _, root := range []string{workers, storage} {
				if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !entry.IsDir() && (strings.HasPrefix(entry.Name(), ".pending-") || strings.HasSuffix(entry.Name(), ".json")) {
						t.Errorf("refused preview published a file: %s", path)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%s: preview exit 1; drive alias refused; no saved record or pending write", scenario)
		})
	}
}

// A real NTFS junction needs no developer-mode/admin symlink privilege. The
// command only prepares generated test paths; preview never executes it.
func TestEvalPreviewRejectsWindowsJunction(t *testing.T) {
	f := newEvalFixture(t)
	alias := filepath.Join(filepath.Dir(f.repo), "junction")
	cmd := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", alias, f.plan.StorageRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v: %s", err, output)
	}
	f.plan.StorageRoot = alias
	f.write(t)
	if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
		t.Fatalf("junction storage accepted: %d %s", code, failure)
	}
	f.plan.StorageRoot = filepath.Join(filepath.Dir(f.repo), "storage")
	f.plan.WorkerRoots = []string{alias}
	f.write(t)
	if code, _, failure := f.run(t); code != ExitError || !strings.Contains(failure, "linked/reparse") {
		t.Fatalf("junction worker exclusion accepted: %d %s", code, failure)
	}
}
