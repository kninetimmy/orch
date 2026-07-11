package execx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit skips the test when git is unavailable; CI has it on all
// three OSes, so a skip only happens on unusual developer machines.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

func TestRunSuccess(t *testing.T) {
	requireGit(t)
	res, err := Local{}.Run(context.Background(), Cmd{
		Name: "git",
		Args: []string{"--version"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %s)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "git version") {
		t.Errorf("stdout = %q, want git version banner", res.Stdout)
	}
}

func TestRunNonZeroExitIsData(t *testing.T) {
	requireGit(t)
	res, err := Local{}.Run(context.Background(), Cmd{
		Name: "git",
		Args: []string{"definitely-not-a-subcommand"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v (non-zero exit must not be an error)", err)
	}
	if res.ExitCode == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if res.Stderr == "" {
		t.Error("stderr empty, want git's complaint")
	}
}

func TestRunMissingExecutable(t *testing.T) {
	_, err := Local{}.Run(context.Background(), Cmd{
		Name: "orch-no-such-binary",
		Dir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("Run succeeded, want error for missing executable")
	}
	if !strings.Contains(err.Error(), "orch-no-such-binary") {
		t.Errorf("error %q does not name the executable", err)
	}
}

func TestRunEmptyDirFailsClosed(t *testing.T) {
	_, err := Local{}.Run(context.Background(), Cmd{Name: "git", Args: []string{"--version"}})
	if err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("Run with empty Dir: err = %v, want working-directory error", err)
	}
}

func TestRunCanceledContext(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Local{}.Run(ctx, Cmd{Name: "git", Args: []string{"--version"}, Dir: t.TempDir()})
	if err == nil {
		t.Fatal("Run with canceled context succeeded, want error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v does not wrap context.Canceled", err)
	}
}

func TestRunLookPathInjection(t *testing.T) {
	sentinel := errors.New("lookup refused")
	_, err := Local{LookPath: func(string) (string, error) { return "", sentinel }}.Run(
		context.Background(), Cmd{Name: "git", Dir: t.TempDir()})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped injected LookPath error", err)
	}
}

func TestRunEnvAndDirPropagate(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[orch]\n\tprobe = value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Local{}.Run(context.Background(), Cmd{
		Name: "git",
		Args: []string{"config", "--global", "--get", "orch.probe"},
		Dir:  dir,
		Env:  []string{"GIT_CONFIG_GLOBAL=" + cfg, "GIT_CONFIG_NOSYSTEM=1"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "value" {
		t.Errorf("stdout = %q, want %q (env not propagated?)", res.Stdout, "value")
	}

	// Dir propagation: rev-parse resolves the toplevel of the repo
	// that contains the working directory.
	if _, err := (Local{}).Run(context.Background(), Cmd{
		Name: "git", Args: []string{"init"}, Dir: dir,
	}); err != nil {
		t.Fatalf("git init: %v", err)
	}
	res, err = Local{}.Run(context.Background(), Cmd{
		Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: dir,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("rev-parse: err=%v exit=%d stderr=%s", err, res.ExitCode, res.Stderr)
	}
	got, err := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(res.Stdout)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Errorf("toplevel = %q, want %q (dir not propagated?)", got, want)
	}
}
