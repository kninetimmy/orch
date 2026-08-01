package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func TestDoctorAllChecksPass(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"note  orch version: dev", "ok    git on PATH", "ok    git repository", "ok    gh on PATH", "ok    gh authentication", "ok    gh repository: o/r", "ok    configuration"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorUnauthenticatedGh(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, authExit: 1}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  gh authentication") {
		t.Errorf("stdout missing auth failure:\n%s", out)
	}
	if strings.Contains(out, "gh repository") {
		t.Errorf("repository probe ran without authentication:\n%s", out)
	}
}

func TestDoctorNoGitHubRepoIsNote(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, repoExit: 1}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (Assist works without a remote)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  no GitHub repository resolved") {
		t.Errorf("stdout missing no-repo note:\n%s", stdout.String())
	}
}

func TestDoctorNotAGitRepo(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{gitExit: 128, gitStderr: "fatal: not a git repository"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  git repository") {
		t.Errorf("stdout missing repository failure:\n%s", stdout.String())
	}
}

func TestDoctorSkipsRepoCheckWithoutGit(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.LookPath = func(name string) (string, error) {
		if name == "git" {
			return "", errNotFound
		}
		return "/fake/" + name, nil
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  git on PATH") {
		t.Errorf("stdout missing git failure:\n%s", out)
	}
	if strings.Contains(out, "git repository") {
		t.Errorf("repository check ran without git on PATH:\n%s", out)
	}
}

func TestDoctorMissingTool(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "", errNotFound
		}
		return "/fake/" + name, nil
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  gh on PATH") {
		t.Errorf("stdout missing gh failure:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "gh authentication") {
		t.Errorf("authentication check ran without gh on PATH:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    git on PATH") {
		t.Errorf("doctor stopped at first failure instead of running all checks:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "one or more checks failed") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDoctorMissingConfig(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  configuration") {
		t.Errorf("stdout missing configuration failure:\n%s", stdout.String())
	}
}

func TestDoctorConfiguredAdaptersPassCurrentListings(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		executable string
	}{
		{"claude", validTOML, "claude"},
		{"codex", validCodexTOML, "codex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, _ := testEnv(t)
			writeConfig(t, env.RepoRoot, tc.config)
			if tc.executable == "codex" {
				if code := Run([]string{"render-agents"}, env); code != ExitOK {
					t.Fatalf("render-agents exit = %d, want %d\n%s", code, ExitOK, stdout.String())
				}
				stdout.Reset()
			}

			probed := false
			env.Runner = fakeRunner{
				toplevel: env.RepoRoot,
				beforeRun: func(cmd execx.Cmd) {
					if cmd.Name != tc.executable {
						return
					}
					probed = true
					if cmd.Dir != env.RepoRoot {
						t.Errorf("%s plugin list dir = %q, want %q", tc.executable, cmd.Dir, env.RepoRoot)
					}
				},
			}
			if code := Run([]string{"doctor"}, env); code != ExitOK {
				t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
			}
			if !probed {
				t.Fatalf("%s plugin listing was not probed", tc.executable)
			}
			if !strings.Contains(stdout.String(), "ok    "+tc.name+" adapter") {
				t.Errorf("stdout missing passing adapter check:\n%s", stdout.String())
			}
		})
	}
}

func TestDoctorAdapterFailures(t *testing.T) {
	claudeVersion := mustAdapterVersion(t, claudeAdapter)
	codexVersion := mustAdapterVersion(t, codexAdapter)
	tests := []struct {
		name        string
		config      string
		spec        adapterSpec
		missingCLI  bool
		configure   func(*fakeRunner)
		wantDetails []string
	}{
		{
			name:       "missing host CLI",
			config:     validTOML,
			spec:       claudeAdapter,
			missingCLI: true,
			wantDetails: []string{
				"claude not found on PATH",
				fmt.Sprintf(`expected adapter version %q`, claudeVersion),
			},
		},
		{
			name:   "listing command fails",
			config: validCodexTOML,
			spec:   codexAdapter,
			configure: func(r *fakeRunner) {
				r.codexPluginExit = 1
				r.codexPluginStderr = "registry unavailable"
			},
			wantDetails: []string{"`codex plugin list --json` exited 1", "registry unavailable"},
		},
		{
			name:   "listing cannot start",
			config: validTOML,
			spec:   claudeAdapter,
			configure: func(r *fakeRunner) {
				r.claudePluginErr = errors.New("spawn failed")
			},
			wantDetails: []string{"run `claude plugin list --json`", "spawn failed"},
		},
		{
			name:   "claude malformed JSON",
			config: validTOML,
			spec:   claudeAdapter,
			configure: func(r *fakeRunner) {
				r.claudePluginJSON = "{"
			},
			wantDetails: []string{"malformed `claude plugin list --json` output"},
		},
		{
			name:   "codex malformed shape",
			config: validCodexTOML,
			spec:   codexAdapter,
			configure: func(r *fakeRunner) {
				r.codexPluginJSON = "[]"
			},
			wantDetails: []string{"malformed `codex plugin list --json` output"},
		},
		{
			name:   "adapter absent",
			config: validTOML,
			spec:   claudeAdapter,
			configure: func(r *fakeRunner) {
				r.claudePluginJSON = fmt.Sprintf(`[{"id":"orch@orch","version":%q,"enabled":true}]`, claudeVersion)
			},
			wantDetails: []string{"orch-claude@orch is absent", fmt.Sprintf(`expected version %q`, claudeVersion)},
		},
		{
			name:   "adapter ambiguous",
			config: validCodexTOML,
			spec:   codexAdapter,
			configure: func(r *fakeRunner) {
				r.codexPluginJSON = fmt.Sprintf(
					`{"installed":[{"pluginId":"orch@orch","version":"0.5.0","installed":true,"enabled":true},{"pluginId":"orch@orch","version":%q,"installed":true,"enabled":true}]}`,
					codexVersion,
				)
			},
			wantDetails: []string{"orch@orch appears 2 times", "installed versions", "0.5.0", codexVersion, "exactly one is required"},
		},
		{
			name:   "adapter marked not installed",
			config: validCodexTOML,
			spec:   codexAdapter,
			configure: func(r *fakeRunner) {
				r.codexPluginJSON = fmt.Sprintf(
					`{"installed":[{"pluginId":"orch@orch","version":%q,"installed":false,"enabled":true}]}`,
					codexVersion,
				)
			},
			wantDetails: []string{"orch@orch is marked not installed", fmt.Sprintf(`installed %q, expected %q`, codexVersion, codexVersion)},
		},
		{
			name:   "adapter disabled",
			config: validTOML,
			spec:   claudeAdapter,
			configure: func(r *fakeRunner) {
				r.claudePluginJSON = fmt.Sprintf(
					`[{"id":"orch-claude@orch","version":%q,"enabled":false}]`,
					claudeVersion,
				)
			},
			wantDetails: []string{"orch-claude@orch is disabled", fmt.Sprintf(`installed %q, expected %q`, claudeVersion, claudeVersion)},
		},
		{
			name:   "adapter version missing",
			config: validTOML,
			spec:   claudeAdapter,
			configure: func(r *fakeRunner) {
				r.claudePluginJSON = `[{"id":"orch-claude@orch","enabled":true}]`
			},
			wantDetails: []string{"orch-claude@orch has no version", fmt.Sprintf(`expected %q`, claudeVersion)},
		},
		{
			name:   "adapter version mismatch",
			config: validCodexTOML,
			spec:   codexAdapter,
			configure: func(r *fakeRunner) {
				r.codexPluginJSON = `{"installed":[{"pluginId":"orch@orch","version":"0.5.0","installed":true,"enabled":true}]}`
			},
			wantDetails: []string{"orch@orch version mismatch", fmt.Sprintf(`installed "0.5.0", expected %q`, codexVersion)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, _ := testEnv(t)
			writeConfig(t, env.RepoRoot, tc.config)
			if tc.spec.host == "codex" {
				if code := Run([]string{"render-agents"}, env); code != ExitOK {
					t.Fatalf("render-agents exit = %d, want %d\n%s", code, ExitOK, stdout.String())
				}
				stdout.Reset()
			}

			runner := fakeRunner{toplevel: env.RepoRoot}
			if tc.configure != nil {
				tc.configure(&runner)
			}
			if tc.missingCLI {
				runner.beforeRun = func(cmd execx.Cmd) {
					if cmd.Name == tc.spec.executable {
						t.Fatalf("%s listing ran though the CLI was absent", tc.spec.executable)
					}
				}
				lookPath := env.LookPath
				env.LookPath = func(name string) (string, error) {
					if name == tc.spec.executable {
						return "", errNotFound
					}
					return lookPath(name)
				}
			}
			env.Runner = runner

			if code := Run([]string{"doctor"}, env); code != ExitError {
				t.Fatalf("exit = %d, want %d\n%s", code, ExitError, stdout.String())
			}
			out := stdout.String()
			if !strings.Contains(out, "FAIL  "+tc.spec.host+" adapter") {
				t.Errorf("stdout missing host adapter failure:\n%s", out)
			}
			for _, want := range tc.wantDetails {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q:\n%s", want, out)
				}
			}
			if !strings.Contains(out, tc.spec.repair) {
				t.Errorf("stdout missing executable update and restart remediation %q:\n%s", tc.spec.repair, out)
			}
		})
	}
}

func TestDoctorUnconfiguredHostIsNotProbed(t *testing.T) {
	tests := []struct {
		name         string
		config       string
		unconfigured string
	}{
		{"claude only", validTOML, "codex"},
		{"codex only", validCodexTOML, "claude"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, _ := testEnv(t)
			writeConfig(t, env.RepoRoot, tc.config)
			if tc.unconfigured == "claude" {
				if code := Run([]string{"render-agents"}, env); code != ExitOK {
					t.Fatalf("render-agents exit = %d, want %d\n%s", code, ExitOK, stdout.String())
				}
				stdout.Reset()
			}

			lookPath := env.LookPath
			env.LookPath = func(name string) (string, error) {
				if name == tc.unconfigured {
					t.Fatalf("LookPath probed unconfigured host %s", name)
				}
				return lookPath(name)
			}
			env.Runner = fakeRunner{
				toplevel: env.RepoRoot,
				beforeRun: func(cmd execx.Cmd) {
					if cmd.Name == tc.unconfigured {
						t.Fatalf("plugin listing probed unconfigured host %s", cmd.Name)
					}
				},
			}
			if code := Run([]string{"doctor"}, env); code != ExitOK {
				t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
			}
		})
	}
}

func TestDoctorAdapterVersionIsIndependentOfBinaryRelease(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })

	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, releaseTag: Version}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (binary-only release must not stale the unchanged adapter)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    claude adapter") {
		t.Errorf("stdout missing passing adapter check:\n%s", stdout.String())
	}
}

func mustAdapterVersion(t *testing.T, spec adapterSpec) string {
	t.Helper()
	version, err := adapterVersion(spec.manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestDoctorMemhubModeOffNeverProbes(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML) // memhub mode = "off"
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  memhub: skipped (mode off)") {
		t.Errorf("stdout missing memhub skip note:\n%s", stdout.String())
	}
}

func TestDoctorMemhubBestEffortHealthy(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "best-effort"`, 1))
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    memhub") {
		t.Errorf("stdout missing healthy memhub:\n%s", stdout.String())
	}
}

func TestDoctorMemhubBestEffortUnhealthyNonFatal(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "best-effort"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubStatusExit: 1, memhubStatusStderr: "down"}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (best-effort must never fail doctor)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  memhub:") {
		t.Errorf("stdout missing memhub note:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredHealthy(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    memhub") {
		t.Errorf("stdout missing healthy memhub:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredHealthFailureFailsDoctor(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubStatusExit: 1, memhubStatusStderr: "unreachable"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  memhub") {
		t.Errorf("stdout missing memhub failure:\n%s", stdout.String())
	}
}

func TestDoctorMemhubRequiredRecallFailureFailsDoctor(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, strings.Replace(validTOML, `mode = "off"`, `mode = "required"`, 1))
	env.Runner = fakeRunner{toplevel: env.RepoRoot, memhubRecallExit: 1, memhubRecallStderr: "wedged"}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  memhub") {
		t.Errorf("stdout missing memhub failure:\n%s", stdout.String())
	}
}

func TestDoctorConsistentDeliveryPasses(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if _, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues()); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"ok    state file", "ok    delivery lock", "ok    state/lock consistency"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorOrphanedLockFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	err := lockfile.Acquire(env.RepoRoot, lockfile.Owner{RunID: "run-orphan", Host: "codex", Hostname: "h", PID: 1, AcquiredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  state/lock consistency") {
		t.Errorf("output missing consistency failure:\n%s", out)
	}
	if !strings.Contains(out, "orch abort") {
		t.Errorf("output missing abort remediation:\n%s", out)
	}
}

func TestDoctorCorruptLockFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "delivery.lock"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "FAIL  delivery lock") {
		t.Errorf("output missing lock failure:\n%s", stdout.String())
	}
}

func TestDoctorNotesDeadAcquirer(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.EnterDelivery(env.RepoRoot, "claude", testPlanRef(), testIssues())
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the lock with an exited process's PID and this hostname.
	if err := lockfile.Release(env.RepoRoot); err != nil {
		t.Fatal(err)
	}
	err = lockfile.Acquire(env.RepoRoot, lockfile.Owner{
		RunID: st.Run.ID, Host: "claude", Hostname: hostname,
		PID: exitedPID(t), AcquiredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (a dead acquirer is normal between commands)\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "no longer running") {
		t.Errorf("output missing dead-acquirer note:\n%s", stdout.String())
	}
}

// exitedPID runs this test binary with no matching tests so it exits
// immediately, returning its (now dead) PID.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExists")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cannot start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process: %v", err)
	}
	return pid
}

func TestDoctorReleaseCheckSkippedOnDevVersion(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	// Version defaults to "dev" (no test in this package stamps it
	// beforehand), so the release check must not run at all.
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, "release") {
		t.Errorf("release check ran on a dev build:\n%s", out)
	}
}

func TestDoctorReleaseCheckUpToDate(t *testing.T) {
	old := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = old })

	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, releaseTag: "v1.2.3"}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    release: up to date (v1.2.3)") {
		t.Errorf("stdout missing up-to-date release note:\n%s", stdout.String())
	}
}

func TestDoctorReleaseCheckNewerAvailable(t *testing.T) {
	old := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = old })

	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, releaseTag: "v1.3.0"}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "note  newer release available: installed v1.2.3, latest v1.3.0; re-run the installer to update") {
		t.Errorf("stdout missing newer-release note:\n%s", out)
	}
}

func TestDoctorReleaseCheckQueryFailureDegradesToNote(t *testing.T) {
	old := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = old })

	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	env.Runner = fakeRunner{toplevel: env.RepoRoot, releaseExit: 1, releaseStderr: "HTTP 500: internal error"}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d (release check must never fail doctor)\n%s", code, ExitOK, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "note  release check:") {
		t.Errorf("stdout missing release-failure note:\n%s", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("release query failure produced a FAIL line:\n%s", out)
	}
}

func TestDoctorNotesLocalOverride(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "note  .orchestrator/config.local.toml present; no overrides set") {
		t.Errorf("stdout missing local-override note:\n%s", stdout.String())
	}
}

func TestDoctorNotesAppliedLocalOverride(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	local := "[concurrency]\nmax_subagents = 5\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "note  .orchestrator/config.local.toml applied; overrides: concurrency.max_subagents") {
		t.Errorf("stdout missing applied-override note:\n%s", stdout.String())
	}
}

// metricsEnabledTOML is validTOML with metrics turned on.
const metricsEnabledTOML = validTOML + "\n[metrics]\nenabled = true\n"

// TestDoctorMetricsDisabledSkipsCheck proves a metrics-disabled
// repository (validTOML's default) gets no "metrics .gitignore" check
// line at all, and does not fail on this account, whether or not
// .gitignore exists.
func TestDoctorMetricsDisabledSkipsCheck(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if strings.Contains(stdout.String(), "metrics .gitignore") {
		t.Errorf("stdout carries a metrics .gitignore check though metrics is disabled:\n%s", stdout.String())
	}
}

// TestDoctorMetricsEnabledMissingGitignoreLineFails proves doctor fails
// closed, naming metrics and the missing .gitignore line, when the
// effective configuration has metrics enabled and .gitignore does not
// carry the metrics ignore line.
func TestDoctorMetricsEnabledMissingGitignoreLineFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, metricsEnabledTOML)
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  metrics .gitignore") {
		t.Errorf("stdout missing the metrics .gitignore failure:\n%s", out)
	}
	if !strings.Contains(out, "metrics") || !strings.Contains(out, ".orchestrator/metrics/") {
		t.Errorf("stdout does not name metrics and the missing line:\n%s", out)
	}
}

// TestDoctorMetricsEnabledGitignoreLinePresentPasses proves doctor
// reports the metrics .gitignore check as passing once .gitignore
// already carries the line.
func TestDoctorMetricsEnabledGitignoreLinePresentPasses(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, metricsEnabledTOML)
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".gitignore"), []byte(".orchestrator/metrics/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    metrics .gitignore") {
		t.Errorf("stdout missing the passing metrics .gitignore check:\n%s", stdout.String())
	}
}

func TestDoctorPolicyViolatingLocalOverrideFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	local := "schema_version = 2\n"
	if err := os.WriteFile(filepath.Join(env.RepoRoot, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  configuration") {
		t.Errorf("stdout missing configuration failure:\n%s", out)
	}
	if !strings.Contains(out, "schema_version") {
		t.Errorf("stdout missing policy-violation detail:\n%s", out)
	}
}

func TestDoctorNoCodexHostSkipsAgentCheck(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validTOML)
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if strings.Contains(stdout.String(), "codex agent files") {
		t.Errorf("stdout carries a Codex-only check:\n%s", stdout.String())
	}
}

func TestDoctorBothHostsCodexAgentsAbsentFails(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validBothHostsTOML)
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL  codex agent files") {
		t.Errorf("stdout missing the agent-file failure:\n%s", out)
	}
	for _, name := range renderedAgentFiles {
		if !strings.Contains(out, agents.Dir+"/"+name+".toml") {
			t.Errorf("stdout does not name %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "orch render-agents") {
		t.Errorf("stdout does not name the repair:\n%s", out)
	}
}

func TestDoctorCodexAgentsCurrentThenAllBadStates(t *testing.T) {
	env, stdout, _ := testEnv(t)
	writeConfig(t, env.RepoRoot, validCodexTOML)
	if code := Run([]string{"render-agents"}, env); code != ExitOK {
		t.Fatalf("render-agents exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	stdout.Reset()
	if code := Run([]string{"doctor"}, env); code != ExitOK {
		t.Fatalf("doctor exit = %d, want %d\n%s", code, ExitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ok    codex agent files") {
		t.Errorf("stdout missing the passing agent-file check:\n%s", stdout.String())
	}

	dir := filepath.Join(env.RepoRoot, filepath.FromSlash(agents.Dir))
	if err := os.Remove(filepath.Join(dir, "orch-scout.toml")); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(dir, "orch-implementer.toml")
	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orch-reviewer.toml"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if code := Run([]string{"doctor"}, env); code != ExitError {
		t.Fatalf("doctor exit = %d, want %d\n%s", code, ExitError, stdout.String())
	}
	out := stdout.String()
	for _, name := range []string{"orch-scout.toml", "orch-implementer.toml", "orch-reviewer.toml"} {
		if !strings.Contains(out, agents.Dir+"/"+name) {
			t.Errorf("stdout does not name %s:\n%s", name, out)
		}
	}
	if strings.Contains(out, agents.Dir+"/orch-specialist.toml") {
		t.Errorf("stdout names a current file:\n%s", out)
	}
	if !strings.Contains(out, "orch render-agents") {
		t.Errorf("stdout does not name the repair:\n%s", out)
	}
}
