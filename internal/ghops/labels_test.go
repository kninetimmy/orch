package ghops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func validLabels() Labels {
	return Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard}
}

func TestLabelsValidate(t *testing.T) {
	tests := map[string]struct {
		labels    Labels
		forbidden []string
		wantErr   string // substring of the ErrBadLabels detail; empty = valid
	}{
		"valid minimal": {
			labels: validLabels(),
		},
		"valid with areas": {
			labels: Labels{Status: StatusAwaitingReview, Type: TypeBug, Role: RoleSpecialist, Risk: RiskCritical, Areas: []string{"core", "cli"}},
		},
		"missing status": {
			labels:  Labels{Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard},
			wantErr: `status ""`,
		},
		"bad status value": {
			labels:  Labels{Status: "done", Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard},
			wantErr: `status "done"`,
		},
		"bad type value": {
			labels:  Labels{Status: StatusReady, Type: "enhancement", Role: RoleImplementer, Risk: RiskStandard},
			wantErr: `type "enhancement"`,
		},
		"bad role value": {
			labels:  Labels{Status: StatusReady, Type: TypeFeature, Role: "reviewer", Risk: RiskStandard},
			wantErr: `role "reviewer"`,
		},
		"bad risk value": {
			labels:  Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: "high"},
			wantErr: `risk "high"`,
		},
		"empty area": {
			labels:  Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard, Areas: []string{""}},
			wantErr: "area label is empty",
		},
		"duplicate area case-insensitive": {
			labels:  Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard, Areas: []string{"Core", "core"}},
			wantErr: "duplicated",
		},
		"reserved area": {
			labels:  Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard, Areas: []string{"Blocked"}},
			wantErr: "reserved taxonomy label",
		},
		"forbidden area model name": {
			labels:    Labels{Status: StatusReady, Type: TypeFeature, Role: RoleImplementer, Risk: RiskStandard, Areas: []string{"Opus-4-8"}},
			forbidden: []string{"opus-4-8"},
			wantErr:   "models never become labels",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.labels.Validate(tt.forbidden...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrBadLabels) {
				t.Fatalf("err = %v, want ErrBadLabels", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

var labelListCall = execxtest.Call{
	Name: "gh",
	Args: []string{"label", "list", "--json", "name", "--limit", "1000"},
}

func labelJSON(names ...string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = `{"name":"` + n + `"}`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func allTaxonomyNames() []string {
	names := make([]string, len(taxonomy))
	for i, def := range taxonomy {
		names[i] = def.name
	}
	return names
}

func TestEnsureLabelTaxonomyAllPresent(t *testing.T) {
	root := tempRoot(t)
	list := labelListCall
	list.Dir = root
	list.Env = ghTestEnv
	list.Stdout = labelJSON(allTaxonomyNames()...)
	g, script := openScripted(t, root, list)
	created, err := g.EnsureLabelTaxonomy(context.Background())
	if err != nil {
		t.Fatalf("EnsureLabelTaxonomy: %v", err)
	}
	script.AssertExhausted()
	if len(created) != 0 {
		t.Errorf("created = %v, want none", created)
	}
}

func TestEnsureLabelTaxonomyCreatesMissing(t *testing.T) {
	root := tempRoot(t)
	// Everything present except needs-human and critical; existing
	// names match case-insensitively (GitHub labels are).
	var have []string
	for _, def := range taxonomy {
		if def.name == "needs-human" || def.name == "critical" {
			continue
		}
		have = append(have, strings.ToUpper(def.name[:1])+def.name[1:])
	}
	list := labelListCall
	list.Dir = root
	list.Env = ghTestEnv
	list.Stdout = labelJSON(have...)
	g, script := openScripted(t, root, list,
		execxtest.Call{
			Name: "gh",
			Args: []string{"label", "create", "needs-human", "--color", "1D76DB", "--description", "orch status label — exactly one per issue (PRD §13)"},
			Dir:  root,
			Env:  ghTestEnv,
		},
		execxtest.Call{
			Name: "gh",
			Args: []string{"label", "create", "critical", "--color", "B60205", "--description", "orch risk label — exactly one per issue (PRD §13)"},
			Dir:  root,
			Env:  ghTestEnv,
		},
	)
	created, err := g.EnsureLabelTaxonomy(context.Background())
	if err != nil {
		t.Fatalf("EnsureLabelTaxonomy: %v", err)
	}
	script.AssertExhausted()
	want := []string{"needs-human", "critical"}
	if len(created) != len(want) || created[0] != want[0] || created[1] != want[1] {
		t.Errorf("created = %v, want %v", created, want)
	}
}

func TestEnsureLabelTaxonomyCreateFails(t *testing.T) {
	root := tempRoot(t)
	list := labelListCall
	list.Dir = root
	list.Env = ghTestEnv
	list.Stdout = labelJSON(allTaxonomyNames()[1:]...) // "ready" missing
	g, script := openScripted(t, root, list, execxtest.Call{
		Name:   "gh",
		Args:   []string{"label", "create", "ready", "--color", "1D76DB", "--description", "orch status label — exactly one per issue (PRD §13)"},
		Dir:    root,
		Env:    ghTestEnv,
		Stderr: "HTTP 403: forbidden",
		Exit:   1,
	})
	created, err := g.EnsureLabelTaxonomy(context.Background())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Fatalf("err = %v, want create failure", err)
	}
	if len(created) != 0 {
		t.Errorf("created = %v, want none", created)
	}
}
