package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// leafClass classifies a single dotted schema key as either
// policy-bearing (fixed by the committed configuration; may never be
// set in config.local.toml) or preference (safe for a machine-local
// override), per PRD §17.
type leafClass int

const (
	classPolicy leafClass = iota
	classPreference
)

// roleNames lists the PRD §9 roles, plus the safe review downgrade,
// in the same set validate.go checks for every enabled host.
var roleNames = []string{"architect", "scout", "implementer", "specialist", "reviewer", "review_downgrade"}

// leafClasses is the closed classification table over every leaf key
// in the schema. A dotted key absent from this map is a table header
// (an intermediate key group, e.g. "hosts.claude.roles.architect"),
// not a leaf; since Load already rejects unknown keys before this
// table is consulted, everything else defined in a TOML file must be
// one or the other.
var leafClasses = newLeafClasses()

func newLeafClasses() map[string]leafClass {
	m := map[string]leafClass{
		"schema_version":            classPolicy,
		"config_revision":           classPolicy,
		"merge.strategy":            classPolicy,
		"memhub.mode":               classPolicy,
		"concurrency.max_subagents": classPreference,
		"metrics.enabled":           classPreference,
	}
	for _, host := range []string{"codex", "claude"} {
		for _, role := range roleNames {
			prefix := "hosts." + host + ".roles." + role
			m[prefix+".model"] = classPreference
			m[prefix+".effort"] = classPreference
		}
	}
	return m
}

// applyLocalOverride reads the machine-local override file under
// repoRoot, if present, and merges its permitted preference keys onto
// committed. committed has already been fully validated on its own
// and is never mutated in place: the returned Config is a distinct
// value, and any Host it modifies is a fresh copy (PRD §17 — local
// overrides never weaken mandatory workflow policy, and the committed
// file's own meaning must not change underfoot).
func applyLocalOverride(repoRoot string, committed *Config) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(LocalOverridePath)))
	if errors.Is(err, fs.ErrNotExist) {
		return committed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", LocalOverridePath, err)
	}

	var local Config
	md, err := toml.Decode(string(data), &local)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", LocalOverridePath, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", LocalOverridePath, strings.Join(keys, ", "))
	}

	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	flaggedHosts := map[string]bool{}
	var toApply []string

	for _, k := range md.Keys() {
		key := k.String()
		class, ok := leafClasses[key]
		if !ok {
			continue // table header, not a leaf key
		}
		if class == classPolicy {
			fail("%s is policy-bearing and cannot be set in %s; committed %s changes go through a Delivery PR", key, LocalOverridePath, Path)
			continue
		}
		if host, isHostLeaf := hostOfLeaf(key); isHostLeaf && hostPtr(committed, host) == nil {
			if !flaggedHosts[host] {
				flaggedHosts[host] = true
				fail("hosts.%s is not enabled in committed %s; enabling a host requires a Delivery PR (PRD §18)", host, Path)
			}
			continue
		}
		toApply = append(toApply, key)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%s: invalid overrides:\n  - %s", LocalOverridePath, strings.Join(problems, "\n  - "))
	}

	sort.Strings(toApply)
	merged := mergeOverride(committed, &local, toApply)
	if err := merged.validate(); err != nil {
		return nil, fmt.Errorf("%s: invalid configuration after applying overrides: %w", LocalOverridePath, err)
	}
	return merged, nil
}

// hostOfLeaf reports the host name for a "hosts.<host>...." leaf key,
// and false for any other key.
func hostOfLeaf(key string) (string, bool) {
	const prefix = "hosts."
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := key[len(prefix):]
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		return rest[:i], true
	}
	return rest, true
}

// mergeOverride returns a new Config combining committed with the
// preference values named by keys (already confirmed permitted),
// taken from local. keys is recorded, as given, on the result's
// Overrides field, so callers must pass it sorted.
func mergeOverride(committed, local *Config, keys []string) *Config {
	merged := *committed
	clonedHosts := map[string]bool{}
	for _, key := range keys {
		applyOverrideLeaf(&merged, local, key, clonedHosts)
	}
	merged.Overrides = keys
	return &merged
}

// applyOverrideLeaf copies the single value named by key from local
// onto merged. Before writing through a hosts.<host>.* key for the
// first time, it replaces merged's *Host for that host with a fresh
// copy, so committed's *Host is never mutated.
func applyOverrideLeaf(merged, local *Config, key string, clonedHosts map[string]bool) {
	switch key {
	case "concurrency.max_subagents":
		merged.Concurrency.MaxSubagents = local.Concurrency.MaxSubagents
		return
	case "metrics.enabled":
		merged.Metrics.Enabled = local.Metrics.Enabled
		return
	}

	// key is hosts.<host>.roles.<role>.<field>.
	parts := strings.Split(key, ".")
	host, role, field := parts[1], parts[3], parts[4]
	if !clonedHosts[host] {
		clone := *hostPtr(merged, host)
		setHostPtr(merged, host, &clone)
		clonedHosts[host] = true
	}
	mp := roleProfilePtr(&hostPtr(merged, host).Roles, role)
	lp := roleProfilePtr(&hostPtr(local, host).Roles, role)
	switch field {
	case "model":
		mp.Model = lp.Model
	case "effort":
		mp.Effort = lp.Effort
	}
}

// hostPtr returns cfg's *Host for the named host, or nil if that host
// is not configured.
func hostPtr(cfg *Config, name string) *Host {
	switch name {
	case "codex":
		return cfg.Hosts.Codex
	case "claude":
		return cfg.Hosts.Claude
	default:
		return nil
	}
}

// setHostPtr sets cfg's *Host for the named host.
func setHostPtr(cfg *Config, name string, h *Host) {
	switch name {
	case "codex":
		cfg.Hosts.Codex = h
	case "claude":
		cfg.Hosts.Claude = h
	}
}

// roleProfilePtr returns a pointer to the named role's RoleProfile
// within r, so its Model/Effort fields can be read or written in
// place.
func roleProfilePtr(r *Roles, role string) *RoleProfile {
	switch role {
	case "architect":
		return &r.Architect
	case "scout":
		return &r.Scout
	case "implementer":
		return &r.Implementer
	case "specialist":
		return &r.Specialist
	case "reviewer":
		return &r.Reviewer
	case "review_downgrade":
		return &r.ReviewDowngrade
	default:
		return nil
	}
}
