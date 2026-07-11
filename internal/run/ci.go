package run

import (
	"os"
	"path/filepath"
	"strings"
)

// CIReport is the plan gate's honest PRD §16 statement about CI. It is
// a pre-PR heuristic only (F8): before a PR exists, required-check
// configuration is unobservable — the real signal is
// ghops.RequiredCI, available once a PR is open (PR B).
type CIReport struct {
	WorkflowsPresent bool   `json:"workflows_present"`
	Statement        string `json:"statement"`
}

// detectCI reports whether repoRoot has any GitHub Actions workflow
// file (*.yml or *.yaml) under .github/workflows.
func detectCI(repoRoot string) (CIReport, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, ".github", "workflows"))
	if err != nil {
		if os.IsNotExist(err) {
			return CIReport{Statement: ciStatement(false)}, nil
		}
		return CIReport{}, err
	}
	present := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			present = true
			break
		}
	}
	return CIReport{WorkflowsPresent: present, Statement: ciStatement(present)}, nil
}

func ciStatement(present bool) string {
	if present {
		return ".github/workflows files are present; this is a pre-PR heuristic only — required-check configuration is unobservable until a PR exists"
	}
	return "no .github/workflows files found; this is a pre-PR heuristic only — required-check configuration is unobservable until a PR exists"
}
