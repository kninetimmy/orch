package nativehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalPathRetainsVerifiedWindowsJunctionResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
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
	for _, name := range []string{"", "existing.txt", "missing.txt"} {
		got, err := CanonicalPath(filepath.Join(link, name), name == "")
		if err != nil {
			t.Fatal(err)
		}
		want, err := CanonicalPath(filepath.Join(target, name), name == "")
		if err != nil || got != want {
			t.Fatalf("native junction path = %q, want %q: %v", got, want, err)
		}
	}
	for _, forbidden := range []string{`\\server\share\path`, `\\?\C:\path`, target + ":stream", target + ".", target + " "} {
		if _, err := CanonicalPath(forbidden, false); err == nil {
			t.Fatalf("native namespace/stream/trailing alias accepted: %q", forbidden)
		}
	}
}
