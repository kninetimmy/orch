package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
)

func TestProtectedListStatusReports(t *testing.T) {
	entry := "\n[[protected.paths]]\npath = \"internal/grant\"\nreason = \"grant authorization\"\n"
	required := config.SurveyMethodologyVersion
	cases := []struct {
		name      string
		protected string
		want      string
		fresh     bool
	}{
		{
			name:  "absent",
			want:  fmt.Sprintf("absent; approved methodology: none; required methodology: %d", required),
			fresh: true,
		},
		{
			name:      "stale",
			protected: fmt.Sprintf("\n[protected]\nmethodology_version = %d\n", required-1) + entry,
			want:      fmt.Sprintf("present; repository-specific entries: 1; approved methodology: %d; required methodology: %d", required-1, required),
			fresh:     true,
		},
		{
			name:      "current nonempty",
			protected: fmt.Sprintf("\n[protected]\nmethodology_version = %d\n", required) + entry,
			want:      fmt.Sprintf("present; repository-specific entries: 1; approved methodology: %d; required methodology: %d", required, required),
		},
		{
			name:      "current empty",
			protected: fmt.Sprintf("\n[protected]\nmethodology_version = %d\n", required),
			want:      fmt.Sprintf("present; repository-specific entries: 0; approved methodology: %d; required methodology: %d", required, required),
		},
	}
	for _, tc := range cases {
		for _, command := range [][]string{{"status"}, {"configure"}, {"doctor"}, {"doctor", "--host", "codex"}} {
			t.Run(tc.name+"/"+strings.Join(command, " "), func(t *testing.T) {
				env, stdout, stderr := testEnv(t)
				writeConfig(t, env.RepoRoot, validCodexTOML+tc.protected)
				dir := filepath.Join(env.RepoRoot, ".orchestrator")
				before := readDir(t, dir)
				if code := Run(command, env); code != ExitOK {
					t.Fatalf("exit = %d, want %d; stderr = %s\n%s", code, ExitOK, stderr.String(), stdout.String())
				}
				want := tc.want + "; survey current"
				if tc.fresh {
					want = tc.want + "; fresh survey required; ask your agent to configure Orch using its setup workflow"
				}
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output missing %q:\n%s", want, stdout.String())
				}
				if got := readDir(t, dir); got != before {
					t.Errorf("report changed configuration or state:\nbefore %q\nafter %q", before, got)
				}
			})
		}
	}
}
