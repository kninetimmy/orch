package config

import (
	"reflect"
	"strings"
	"testing"
)

// Keep the methodology-1 fixture readable without treating it as re-approved.
const protectedTOML = `
[protected]
methodology_version = 1

[[protected.paths]]
path   = "internal/grant"
reason = "the grant engine"

[[protected.paths]]
path   = 'adapters\claude\skills\'
reason = "skill text the Architect obeys"
`

func withProtected() *Protected {
	return &Protected{MethodologyVersion: 1, Paths: []ProtectedPath{
		{Path: "internal/grant", Reason: "the grant engine"},
		{Path: `adapters\claude\skills\`, Reason: "skill text the Architect obeys"},
	}}
}

func TestParseProtectedSection(t *testing.T) {
	cfg, err := Parse([]byte(singleHostTOML("claude", "high") + protectedTOML))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Protected, withProtected()) {
		t.Errorf("Protected = %+v", cfg.Protected)
	}
	if err := cfg.CheckGrantProtected(); err == nil || !strings.Contains(err.Error(), "/orch:configure") {
		t.Errorf("a methodology-1 list must be re-surveyed: %v", err)
	}
	if !reflect.DeepEqual(cfg.Protected, withProtected()) {
		t.Errorf("readiness check rewrote the legacy list: %+v", cfg.Protected)
	}
	cfg, err = Parse([]byte(singleHostTOML("claude", "high") + "\n[protected]\nmethodology_version = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Protected == nil || len(cfg.Protected.Paths) != 0 || cfg.CheckGrantProtected() != nil {
		t.Errorf("an empty protected section = %+v, check %v", cfg.Protected, cfg.CheckGrantProtected())
	}
}

func TestParseRejectsUnverifiableProtectedPaths(t *testing.T) {
	for path, want := range map[string]string{
		"/etc":                 "is absolute",
		`\\server\share\x`:     "", // a volume on Windows, absolute elsewhere
		`\x`:                   "is absolute",
		"C:/repo/x":            "drive or volume",
		`c:x`:                  "drive or volume",
		"../outside":           "escapes the repository",
		`docs\..\..\outside`:   "escapes the repository",
		"..":                   "escapes the repository",
		"":                     "names no path",
		"./":                   "names no path",
		"docs/../internal/x":   "-",
		"internal/grant/x.go ": "-",
	} {
		toml := singleHostTOML("claude", "high") + "\n[protected]\nmethodology_version = 1\n\n[[protected.paths]]\npath = '" + path + "'\nreason = \"r\"\n"
		_, err := Parse([]byte(toml))
		if want == "-" {
			if err != nil {
				t.Errorf("%q rejected: %v", path, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "protected.paths[0].path") || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", path, err, want)
		}
	}
	_, err := Parse([]byte(singleHostTOML("claude", "high") + "\n[protected]\nmethodology_version = 1\n\n[[protected.paths]]\npath = \"x\"\n"))
	if err == nil || !strings.Contains(err.Error(), "protected.paths[0].reason") {
		t.Errorf("an entry without a reason: err = %v", err)
	}
	if _, err := Parse([]byte(singleHostTOML("claude", "high") + "\n[protected]\nmethodology_version = 1\nextra = 1\n")); err == nil {
		t.Error("an unknown protected key was accepted")
	}
}

func TestCheckGrantProtected(t *testing.T) {
	c := singleHostConfig()
	for name, p := range map[string]*Protected{
		"missing":       nil,
		"older version": {MethodologyVersion: SurveyMethodologyVersion - 1},
		"newer version": {MethodologyVersion: SurveyMethodologyVersion + 1, Paths: withProtected().Paths},
	} {
		c.Protected = p
		if err := c.CheckGrantProtected(); err == nil || !strings.Contains(err.Error(), "/orch:configure") {
			t.Errorf("%s: err = %v, want a refusal naming /orch:configure", name, err)
		}
	}
	c.Protected = &Protected{MethodologyVersion: SurveyMethodologyVersion}
	if err := c.CheckGrantProtected(); err != nil {
		t.Errorf("an approved empty list: %v", err)
	}
}

func TestLocalOverrideCannotSetProtected(t *testing.T) {
	committed, err := Parse([]byte(singleHostTOML("claude", "high") + protectedTOML))
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{
		"[protected]\n",
		"[protected]\nmethodology_version = 1\n",
		"[[protected.paths]]\npath = \"x\"\nreason = \"r\"\n",
		"[[protected.paths]]\nreason = \"r\"\n",
		"protected = {}\n",
	} {
		if _, err := MergeLocal(committed, []byte(local)); err == nil || !strings.Contains(err.Error(), "policy-bearing") {
			t.Errorf("%q: err = %v, want a policy-bearing rejection", local, err)
		}
	}
}

func TestRevisionWithProtected(t *testing.T) {
	legacy := bothHostsConfig()
	openCode := bothHostsConfig()
	oc := *openCode.Hosts.Codex
	openCode.Hosts.OpenCode = &oc
	// The revisions these configs had before #397 added [protected].
	for c, want := range map[*Config]string{legacy: "sha256:1c1313cb7a2f", openCode: "sha256:de17d3e331e2"} {
		base, err := Revision(c)
		if err != nil {
			t.Fatal(err)
		}
		if base != want {
			t.Errorf("Revision without [protected] = %q, want the pre-#397 %q", base, want)
		}
		seen := map[string]string{base: "absent"}
		for name, p := range map[string]*Protected{
			"empty":          {MethodologyVersion: 1},
			"version":        {MethodologyVersion: 2},
			"one entry":      withProtected(),
			"entry removed":  {MethodologyVersion: 1, Paths: withProtected().Paths[:1]},
			"path changed":   {MethodologyVersion: 1, Paths: []ProtectedPath{{Path: "internal/run", Reason: "the grant engine"}, withProtected().Paths[1]}},
			"reason changed": {MethodologyVersion: 1, Paths: []ProtectedPath{{Path: "internal/grant", Reason: "other"}, withProtected().Paths[1]}},
		} {
			c.Protected = p
			got, err := Revision(c)
			if err != nil {
				t.Fatal(err)
			}
			if prev, dup := seen[got]; dup {
				t.Errorf("Revision with %s equals the one with %s (opencode %v)", name, prev, c.Hosts.OpenCode != nil)
			}
			seen[got] = name
		}
		c.Protected = nil
	}
}

func TestRenderParseRoundTripProtected(t *testing.T) {
	for _, p := range []*Protected{withProtected(), {MethodologyVersion: 1}} {
		c := bothHostsConfig()
		c.Protected = p
		data, err := Render(c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse(Render(c)): %v\n%s", err, data)
		}
		if !reflect.DeepEqual(got.Protected, p) {
			t.Errorf("round-tripped Protected = %+v, want %+v\n%s", got.Protected, p, data)
		}
	}
}
