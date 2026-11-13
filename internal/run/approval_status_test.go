package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

func inspectApprovalStatus(t *testing.T, root string) (*ApprovalStatusDoc, error) {
	t.Helper()
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	before := readinessTree(t, root)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: canonical, Stdout: canonical},
		{Name: "git", Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: canonical, Stdout: filepath.Join(canonical, ".git")},
	}}
	doc, err := ApprovalStatus(t.Context(), Env{RepoRoot: root, Runner: script, ClaudeSession: architectSession, Now: fixedNow})
	script.AssertExhausted()
	if readinessTree(t, root) != before {
		t.Fatal("approval inspection changed repository files, locks, sessions or metrics")
	}
	return doc, err
}

func TestApprovalStatusEvidence(t *testing.T) {
	issue := fixtureIssue("a", 1, state.PhaseAwaitingMerge)
	issue.ApprovedHeadOID = readinessHead
	for _, name := range []string{"recorded approvals", "fresh pin", "missing lock", "orphan lock", "legacy state", "invalid configuration"} {
		t.Run(name, func(t *testing.T) {
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectApprovalStatus(t, root); err != nil {
				t.Fatal(err)
			}
			st := loadRun(t, root)
			switch name {
			case "fresh pin":
				st.Run.Issues[0].ApprovedHeadOID = readinessOldHead
				st.Run.Plan.ApprovedBy = "grant:recorded-source"
			case "missing lock":
				if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
					t.Fatal(err)
				}
			case "orphan lock":
				st.Mode, st.Run = state.ModeAssist, nil
				if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
					t.Fatal(err)
				}
				if err := lockfile.Acquire(root, lockfile.Owner{RunID: "orphan", Host: "claude"}); err != nil {
					t.Fatal(err)
				}
			case "legacy state":
				st.SchemaVersion = 4
			case "invalid configuration":
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte("invalid ["), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			want, err := Status(t.Context(), Env{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := inspectApprovalStatus(t, root)
			if err != nil || doc.SchemaVersion != 1 || doc.ActiveGrant != nil || !reflect.DeepEqual(doc.Status, want) {
				t.Fatalf("snapshot=%+v err=%v want status=%+v", doc, err, want)
			}
			if st.Run != nil && (doc.Status.Run.Plan != st.Run.Plan || doc.Status.Run.Issues[0].ApprovedHeadOID != st.Run.Issues[0].ApprovedHeadOID) {
				t.Fatal("lost recorded plan approval or merge pin")
			}
			encoded, err := json.Marshal(doc)
			if err != nil || !strings.Contains(string(encoded), `"active_grant":null`) || strings.Contains(string(encoded), `"authorized"`) {
				t.Fatalf("absence/authority encoding: %s, %v", encoded, err)
			}
			limits := strings.Join(doc.Limitations, " ")
			for _, text := range []string{"not authorization", "non-atomic", "not evaluated", "human approvals"} {
				if !strings.Contains(limits, text) {
					t.Fatalf("missing limitation %q: %v", text, doc.Limitations)
				}
			}
		})
	}
}

func TestApprovalStatusErrors(t *testing.T) {
	for _, name := range []string{"state", "lock", "Git", "grant"} {
		t.Run(name, func(t *testing.T) {
			root := setupRepo(t, metricsEnabledConfigTOML)
			common := filepath.Join(root, ".git")
			if err := os.Mkdir(common, 0o755); err != nil {
				t.Fatal(err)
			}
			want := "unreadable"
			switch name {
			case "state":
				want = "parse .orchestrator/state.json"
				if err := os.WriteFile(filepath.Join(root, state.Path), []byte("{broken"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "lock":
				want = "lock"
				if err := os.WriteFile(filepath.Join(root, lockfile.Path), []byte("{broken"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "grant":
				dir := filepath.Join(common, grant.DirName)
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{broken"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "grant" {
				if doc, err := inspectApprovalStatus(t, root); doc != nil || err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("snapshot=%+v err=%v", doc, err)
				}
				return
			}
			script := &execxtest.Script{T: t}
			if name == "Git" {
				want = "probe failed"
				script.Calls = []execxtest.Call{{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Exit: 1, Stderr: want}}
			}
			before := readinessTree(t, root)
			doc, err := ApprovalStatus(t.Context(), Env{RepoRoot: root, Runner: script, Now: fixedNow, ClaudeSession: architectSession})
			script.AssertExhausted()
			if doc != nil || err == nil || !strings.Contains(err.Error(), want) || readinessTree(t, root) != before {
				t.Fatalf("error/no-write: snapshot=%+v err=%v", doc, err)
			}
		})
	}
}
