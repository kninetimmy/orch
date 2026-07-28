package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// TestCompleteMetricsTrapNamesCause proves that when RequireClean fails
// on the primary checkout during run completion in a repository where
// metrics is enabled and .gitignore lacks the metrics ignore line,
// Complete's error names metrics as the cause and the remedy — not
// merely ErrNotClean. The dirty tree is a leftover untracked metrics
// document, exactly what an earlier enabled-metrics run would leave
// behind.
func TestCompleteMetricsTrapNamesCause(t *testing.T) {
	root := newActivateRepoWithConfigAndIgnore(t, testConfigTOMLMetricsEnabled, fullGitignore)

	enterDeliveryAt(t, root, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseCleaned)})
	if err := metrics.Append(root, "run-leftover", metrics.Event{At: "2026-07-11T12:00:00Z", Verb: "activate"}); err != nil {
		t.Fatal(err)
	}

	script := &execxtest.Script{T: t, Calls: []execxtest.Call{ghAuth(), ghRepoViewCall("main")}}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}

	_, err := Complete(context.Background(), env, []byte(`{"schema_version":1}`))
	if !errors.Is(err, gitops.ErrNotClean) {
		t.Fatalf("err = %v, want ErrNotClean", err)
	}
	if !strings.Contains(err.Error(), "metrics") {
		t.Errorf("err = %v, want it to name metrics as the cause", err)
	}
	if !strings.Contains(err.Error(), "orch configure") {
		t.Errorf("err = %v, want it to name the orch configure remedy", err)
	}
	script.AssertExhausted()
}
