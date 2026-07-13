// Package orch_test validates the root cross-host plugin marketplace
// manifest (.claude-plugin/marketplace.json). One manifest serves both
// hosts — Claude Code and Codex CLI each read it and silently filter
// entries whose source lacks their own host manifest — so these tests
// pin the invariants that filtering relies on: every adapter directory
// is listed exactly once, each entry's source carries the right host
// manifest, and names/descriptions/versions stay consistent with the
// per-adapter plugin.json files. Ordinary Go tests so `go test ./...`
// catches drift; host-specific artifact checks live in each adapter's
// plugin_test.go, and cross-host behavioural invariants live in
// internal/adaptertest (which this root package deliberately does not
// import — its scope is the two adapter test files).
package orch_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const marketplacePath = ".claude-plugin/marketplace.json"

// marketplaceEntry is one `plugins` array element.
type marketplaceEntry struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// marketplaceManifest is the strict shape of .claude-plugin/marketplace.json.
type marketplaceManifest struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		Version     string `json:"version"`
	} `json:"metadata"`
	Plugins []marketplaceEntry `json:"plugins"`
}

// pluginManifest is the strict shape of each adapter's plugin.json.
type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
}

// hostManifests maps each marketplace entry name to the host manifest
// its source directory must carry. The entry names are load-bearing:
// they are the install handles documented in the READMEs
// (`claude plugin install orch-claude@orch`, `codex plugin add orch@orch`).
var hostManifests = map[string]string{
	"orch-claude": filepath.Join(".claude-plugin", "plugin.json"),
	"orch":        filepath.Join(".codex-plugin", "plugin.json"),
}

// decodeStrict parses path into v with DisallowUnknownFields, so an
// unexpected key in a manifest fails the test instead of silently
// passing through.
func decodeStrict(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func loadMarketplace(t *testing.T) marketplaceManifest {
	t.Helper()
	var m marketplaceManifest
	decodeStrict(t, marketplacePath, &m)
	return m
}

func TestMarketplaceManifestStrict(t *testing.T) {
	m := loadMarketplace(t)
	if m.Name != "orch" {
		t.Errorf("name = %q, want orch", m.Name)
	}
	if m.Owner.Name == "" {
		t.Error("owner.name is empty")
	}
	if m.Metadata.Description == "" {
		t.Error("metadata.description is empty")
	}
	if m.Metadata.Version == "" {
		t.Error("metadata.version is empty")
	}
}

func TestMarketplaceEntries(t *testing.T) {
	m := loadMarketplace(t)
	if len(m.Plugins) != len(hostManifests) {
		t.Fatalf("plugins has %d entries, want %d", len(m.Plugins), len(hostManifests))
	}
	var got, want []string
	for _, e := range m.Plugins {
		got = append(got, e.Name)
		// The string form matters, not just resolvability: hosts read
		// this file on every OS, so sources must stay ./-relative with
		// forward slashes.
		if !strings.HasPrefix(e.Source, "./") {
			t.Errorf("entry %q source = %q, want ./ prefix", e.Name, e.Source)
		}
		if strings.Contains(e.Source, `\`) {
			t.Errorf("entry %q source = %q contains a backslash", e.Name, e.Source)
		}
		if e.Description == "" {
			t.Errorf("entry %q has an empty description", e.Name)
		}
	}
	for name := range hostManifests {
		want = append(want, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry names = %v, want %v", got, want)
		}
	}
}

func TestMarketplaceSourcesResolve(t *testing.T) {
	m := loadMarketplace(t)
	for _, e := range m.Plugins {
		manifest, ok := hostManifests[e.Name]
		if !ok {
			t.Errorf("entry %q is not a known host entry", e.Name)
			continue
		}
		path := filepath.Join(filepath.FromSlash(e.Source), manifest)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("entry %q: host manifest %s: %v", e.Name, path, err)
		}
	}
}

func TestMarketplaceEntryConsistency(t *testing.T) {
	m := loadMarketplace(t)
	for _, e := range m.Plugins {
		manifest, ok := hostManifests[e.Name]
		if !ok {
			t.Errorf("entry %q is not a known host entry", e.Name)
			continue
		}
		var p pluginManifest
		decodeStrict(t, filepath.Join(filepath.FromSlash(e.Source), manifest), &p)
		if p.Name != "orch" {
			t.Errorf("entry %q: plugin.json name = %q, want orch", e.Name, p.Name)
		}
		if e.Description != p.Description {
			t.Errorf("entry %q description diverges from its plugin.json:\nmarketplace: %q\nplugin.json: %q", e.Name, e.Description, p.Description)
		}
		if p.Version != m.Metadata.Version {
			t.Errorf("entry %q: plugin.json version = %q, marketplace metadata.version = %q", e.Name, p.Version, m.Metadata.Version)
		}
	}
}

func TestMarketplaceCoversAllAdapters(t *testing.T) {
	m := loadMarketplace(t)
	sources := make(map[string]int)
	for _, e := range m.Plugins {
		sources[filepath.Clean(filepath.FromSlash(e.Source))]++
	}
	dirs, err := os.ReadDir("adapters")
	if err != nil {
		t.Fatalf("read adapters/: %v", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join("adapters", d.Name())
		if n := sources[dir]; n != 1 {
			t.Errorf("adapter %s appears in %d marketplace entries, want 1", dir, n)
		}
	}
}
