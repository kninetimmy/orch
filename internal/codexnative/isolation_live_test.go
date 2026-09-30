//go:build codex_live

package codexnative

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestCodexIsolationSmoke is opt-in and never starts a model turn. An unsupported
// host/platform fails with a limitation, rather than skipping as validation.
func TestCodexIsolationSmoke(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Fatal("isolation validation limitation: requires native Windows and an already configured elevated Codex sandbox")
	}
	installed, err := exec.LookPath("codex.exe")
	if err != nil {
		installed = filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OpenAI", "Codex", "bin", "codex.exe")
		if info, statErr := os.Stat(installed); statErr != nil || !info.Mode().IsRegular() {
			t.Fatal("isolation validation limitation: native codex.exe is unavailable")
		}
	}
	payload, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	layout := isolationLayout(t)
	outside := filepath.Join(filepath.Dir(layout.Workspace), "unapproved")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	protected := []string{layout.MainCheckout, layout.ControllerState, layout.SiblingWorkspaces[0], layout.CredentialPaths[0]}
	for _, dir := range protected {
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := filepath.Join(layout.MainCheckout, ".git", "worktrees", "synthetic-worker")
	if err := os.MkdirAll(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "sentinel"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Workspace, ".git"), []byte("gitdir: "+metadata+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	protected = append(protected, metadata)
	alias := filepath.Join(layout.Workspace, "protected-alias")
	linkIsolationDirectory(t, alias, layout.MainCheckout)
	t.Setenv("ORCH_CONTROLLER_CREDENTIAL", "synthetic-controller-credential")
	t.Setenv("OPENAI_API_KEY", "synthetic-api-key")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	commands := 0
	var profiles []string
	for _, reviewer := range []bool{false, true} {
		b, err := prepareIsolation(layout, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, b.profile())
		func() {
			c, capabilities, err := openIsolation(ctx, Options{Executable: installed, Dir: layout.Workspace, ClientVersion: "isolation-smoke"}, b)
			if err != nil {
				t.Fatalf("isolation validation limitation: native sandbox/profile unavailable: %v", err)
			}
			defer func() {
				if err := c.close(); err != nil {
					t.Errorf("native app-server cleanup: %v", err)
				}
			}()
			t.Logf("native Windows host=%s role=%s readiness=%v profileAllowed=%v; these are prerequisites only", capabilities.HostVersion, b.profile(), capabilities.SandboxReady, capabilities.ProfileAllowed)
			check := func(operation, path string, want int) {
				t.Helper()
				code, err := c.diagnosticCommand(b, []string{payload, "orch-isolation-fixture", operation, path}, false)
				commands++
				if err != nil || code != want {
					t.Fatalf("synthetic %s under %s: exit=%d want=%d error=%v", operation, b.profile(), code, want, err)
				}
			}
			workspaceFile := filepath.Join(layout.Workspace, "allowed-write")
			if reviewer {
				check("read", workspaceFile, 0)
				check("write", workspaceFile, 13)
				check("write", filepath.Join(layout.Workspace, "reviewer-new-file"), 13)
			} else {
				check("write", workspaceFile, 0)
			}
			check("write", filepath.Join(layout.Scratch, b.profile()), 0)
			check("environment", b.scratch, 0)
			for _, dir := range protected {
				check("read", filepath.Join(dir, "sentinel"), 13)
				check("write", filepath.Join(dir, "sentinel"), 13)
				check("write", filepath.Join(dir, "denied-new-file"), 13)
			}
			check("read", filepath.Join(alias, "sentinel"), 13)
			check("write", filepath.Join(alias, "denied-alias-file"), 13)
			check("write", filepath.Join(layout.Workspace, "..", "unapproved", "denied-escape"), 13)
			marker := filepath.Join(layout.Scratch, b.profile()+"-canceled")
			started := time.Now()
			code, err := c.diagnosticCommand(b, []string{payload, "orch-isolation-fixture", "cancel", marker}, true)
			commands++
			var rejection *rpcRejection
			canceled := (err == nil && code != 0) || (errors.As(err, &rejection) && rejection.method == "command/exec" && rejection.code == -32603)
			if !canceled || time.Since(started) >= 5*time.Second {
				t.Fatalf("bounded native command cancellation: exit=%d error=%v duration=%s", code, err, time.Since(started))
			}
			t.Logf("%s timeoutMs=1000: exit=%d rpcError=%v elapsed=%s; verifying payload and descendant markers from parent", b.profile(), code, err, time.Since(started))
			for _, path := range []string{marker + ".started", marker + ".child.started"} {
				if data, err := os.ReadFile(path); err != nil || string(data) != "started" {
					t.Fatal("cancellation payload/descendant did not actually run")
				}
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("canceled payload wrote its delayed marker")
			}
			if err := modelToolBoundary(capabilities.HostVersion); !errors.Is(err, ErrIsolationUnavailable) {
				t.Fatal("command proof incorrectly enabled model tools")
			}
			t.Logf("%s synthetic checks: allowed access, explicit denied reads/writes, outside write denial, environment and native timeout cancellation verified", b.profile())
		}()
	}
	if commands != 46 {
		t.Fatalf("smoke did not actually exercise every command: %d, want 46", commands)
	}
	// Wait beyond the descendant's delayed write. Immediate absence alone could
	// incorrectly pass while an orphaned child was still running.
	time.Sleep(5 * time.Second)
	for _, profile := range profiles {
		marker := filepath.Join(layout.Scratch, profile+"-canceled")
		for _, path := range []string{marker, marker + ".child"} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("parent verification: canceled payload/descendant wrote a delayed marker")
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(layout.Workspace, "allowed-write")); err != nil || string(data) != "written" {
		t.Fatal("parent verification: allowed workspace write missing or reviewer changed it")
	}
	for _, profile := range profiles {
		if data, err := os.ReadFile(filepath.Join(layout.Scratch, profile)); err != nil || string(data) != "written" {
			t.Fatalf("parent verification: %s scratch write missing", profile)
		}
	}
	for _, dir := range protected {
		if data, err := os.ReadFile(filepath.Join(dir, "sentinel")); err != nil || string(data) != "preserve" {
			t.Fatal("parent verification: protected synthetic sentinel changed")
		}
		if _, err := os.Stat(filepath.Join(dir, "denied-new-file")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("parent verification: protected synthetic file was created")
		}
	}
	for _, path := range []string{filepath.Join(layout.Workspace, "reviewer-new-file"), filepath.Join(layout.MainCheckout, "denied-alias-file"), filepath.Join(outside, "denied-escape")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("parent verification: forbidden synthetic output exists")
		}
	}
	t.Logf("parent verified %d native synthetic commands; zero model turns; model-tool execution remains unavailable", commands)
}
