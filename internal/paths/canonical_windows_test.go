package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCanonicalRejectsUnresolvedWindowsJunction(t *testing.T) {
	root := t.TempDir()
	writable := filepath.Join(root, "writable")
	if err := os.Mkdir(filepath.Join(root, OrchestratorDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{writable, filepath.Join(root, "primary"), filepath.Join(root, "sibling")} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"primary", "sibling"} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(root, name)
			if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("preserve\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(writable, "escape-"+name)
			// Test paths are environment values, never shell expressions.
			cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
			cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+link, "ORCH_TEST_TARGET="+target)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("required junction fixture: %v: %s", err, output)
			}
			t.Cleanup(func() {
				if err := os.Remove(link); err != nil {
					t.Error(err)
				}
			})
			for _, path := range []string{link, filepath.Join(link, "tracked.txt"), filepath.Join(link, "future", "new.txt")} {
				got, err := Canonical(path)
				if err != nil {
					if !strings.Contains(err.Error(), "redirecting reparse") {
						t.Fatal(err)
					}
				} else {
					relative, err := filepath.Rel(link, path)
					if err != nil || got != canonical(t, filepath.Join(target, relative)) {
						t.Fatalf("Canonical trusted the unresolved alias: %q: %v", got, err)
					}
				}
				if in, err := Inside(writable, path); err == nil && in {
					t.Fatal("Inside trusted an unresolved junction")
				}
			}
			for _, find := range []func(string) (string, error){FindRoot, FindOutermostRoot} {
				if found, err := find(link); (err == nil && found != canonical(t, root)) || (err != nil && !strings.Contains(err.Error(), "redirecting reparse")) {
					t.Fatalf("root discovery trusted an unresolved junction: %q: %v", found, err)
				}
			}
		})
	}
}

func TestWindowsReparseTagClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  uint32
		want bool
	}{
		{"mount point", 0xa0000003, true},
		{"symbolic link", syscall.IO_REPARSE_TAG_SYMLINK, true},
		{"cloud placeholder", 0x9000001a, false},
		{"deduplicated file", 0x80000013, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redirectingReparse(syscall.FILE_ATTRIBUTE_REPARSE_POINT, tc.tag); got != tc.want {
				t.Fatalf("redirectingReparse = %v, want %v", got, tc.want)
			}
			if redirectingReparse(0, tc.tag) {
				t.Fatal("ordinary attributes were classified as a reparse point")
			}
		})
	}
}
