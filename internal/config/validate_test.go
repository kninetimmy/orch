package config

import (
	"fmt"
	"strings"
	"testing"
)

// singleHostTOML renders the smallest schema_version=1 document that
// enables host with all six roles pinned to effort, so Parse's real
// validation can be exercised against one effort value at a time.
func singleHostTOML(host, effort string) string {
	var b strings.Builder
	b.WriteString("schema_version  = 1\n")
	b.WriteString(`config_revision = "r1"` + "\n\n")
	b.WriteString("[memhub]\nmode = \"off\"\n")
	model := "gpt-5.6-sol"
	if host == "claude" {
		model = "claude-opus-5"
	}
	for _, role := range roleOrder {
		fmt.Fprintf(&b, "\n[hosts.%s.roles.%s]\nmodel  = %q\neffort = %q\n", host, role, model, effort)
	}
	return b.String()
}

// TestEffortDomainPerHost pins the effort levels each host's role
// profiles accept and reject (issue #122): codex gains xhigh, max, and
// ultra; claude gains max. "none" and "minimal" stay excluded from both
// hosts, and "ultra" stays excluded from claude.
func TestEffortDomainPerHost(t *testing.T) {
	tests := []struct {
		host    string
		effort  string
		wantErr bool
	}{
		{"codex", "low", false},
		{"codex", "medium", false},
		{"codex", "high", false},
		{"codex", "xhigh", false},
		{"codex", "max", false},
		{"codex", "ultra", false},
		{"codex", "minimal", true},
		{"codex", "none", true},
		{"claude", "low", false},
		{"claude", "medium", false},
		{"claude", "high", false},
		{"claude", "xhigh", false},
		{"claude", "max", false},
		{"claude", "ultra", true},
		{"claude", "minimal", true},
		{"claude", "none", true},
	}
	for _, tt := range tests {
		t.Run(tt.host+"/"+tt.effort, func(t *testing.T) {
			_, err := Parse([]byte(singleHostTOML(tt.host, tt.effort)))
			if tt.wantErr && err == nil {
				t.Fatalf("Parse(%s effort=%s) succeeded, want error", tt.host, tt.effort)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Parse(%s effort=%s): %v", tt.host, tt.effort, err)
			}
		})
	}
}

// TestEffortListNamesFullAcceptedDomain pins effortList's error text to
// name every value effortsByHost accepts for that host, so an
// out-of-domain effort's validation error always tells the caller the
// complete accepted list.
func TestEffortListNamesFullAcceptedDomain(t *testing.T) {
	for host, accepted := range effortsByHost {
		list := effortList(host)
		for effort := range accepted {
			if !strings.Contains(list, effort) {
				t.Errorf("effortList(%s) = %q, missing accepted value %q", host, list, effort)
			}
		}
	}
}
