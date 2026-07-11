package routing

import (
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
)

func TestFailedModel(t *testing.T) {
	tests := map[string]struct {
		history History
		query   string
		want    bool
	}{
		"nil history": {nil, "impl-m", false},
		"empty history": {
			History{},
			"impl-m",
			false,
		},
		"failed model matches": {
			History{{Role: manifest.RoleImplementer, Selection: manifest.Selection{Model: "impl-m", Effort: "high"}, Failed: true}},
			"impl-m",
			true,
		},
		"other model does not match": {
			History{{Role: manifest.RoleImplementer, Selection: manifest.Selection{Model: "impl-m", Effort: "high"}, Failed: true}},
			"spec-m",
			false,
		},
		"non-failed attempt does not match": {
			History{{Role: manifest.RoleImplementer, Selection: manifest.Selection{Model: "impl-m", Effort: "high"}, Failed: false}},
			"impl-m",
			false,
		},
		"effort bump on failed model still matches on model string": {
			History{{Role: manifest.RoleImplementer, Selection: manifest.Selection{Model: "impl-m", Effort: "low"}, Failed: true}},
			"impl-m",
			true,
		},
		"matches among several attempts": {
			History{
				{Role: manifest.RoleScout, Selection: manifest.Selection{Model: "scout-m", Effort: "low"}, Failed: false},
				{Role: manifest.RoleImplementer, Selection: manifest.Selection{Model: "impl-m", Effort: "high"}, Failed: true},
			},
			"impl-m",
			true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.history.FailedModel(tt.query); got != tt.want {
				t.Errorf("FailedModel(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestDowngradeFactsEligible(t *testing.T) {
	all := DowngradeFacts{Mechanical: true, LowRisk: true, FullySpecified: true, Unsurprising: true}
	if !all.Eligible() {
		t.Error("all four facts should be Eligible")
	}
	if (DowngradeFacts{}).Eligible() {
		t.Error("zero value must not be Eligible (absence is never permission)")
	}
	// Every single-fact-missing combination is ineligible.
	for i := 0; i < 4; i++ {
		f := all
		switch i {
		case 0:
			f.Mechanical = false
		case 1:
			f.LowRisk = false
		case 2:
			f.FullySpecified = false
		case 3:
			f.Unsurprising = false
		}
		if f.Eligible() {
			t.Errorf("facts with field %d cleared should not be Eligible", i)
		}
	}
}
