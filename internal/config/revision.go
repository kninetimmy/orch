package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// revisionShadow is Config's underlying struct given its own defined
// type, so it can be passed to json.Marshal without adding json tags
// to the public, toml-tagged Config type. encoding/json falls back to
// each field's Go name when no json tag is present, which is
// deterministic and is all Revision needs: a stable, semantics-only
// hash input, never itself decoded back into a Config.
type revisionShadow Config

// Revision computes c's content-addressed revision identifier:
// "sha256:" followed by the first 12 hex characters of the SHA-256 of
// c's semantic content. It marshals a shadow copy of c with
// ConfigRevision and Overrides cleared first — the revision cannot
// depend on the value it is computing, and Overrides is the
// machine-local audit trail (PRD §17), orthogonal to the committed
// content this identifies — so Revision is insensitive to both and
// sensitive to every other field.
//
// This mirrors PlanDoc.Digest's precedent (internal/run/plandoc.go),
// restricted to a short, human-typeable prefix: the revision is an
// audit label surfaced in issues, PRs, and `orch status`, not a
// security boundary, so the shorter, collision-tolerant form is the
// right trade here. `orch configure` (task 16) recomputes it
// identically.
func Revision(c *Config) (string, error) {
	shadow := revisionShadow(*c)
	shadow.ConfigRevision = ""
	shadow.Overrides = nil

	var value any = shadow
	if c.Hosts.OpenCode == nil {
		// Preserve the established revision for every pre-OpenCode
		// configuration. A disabled additive host must not invalidate an
		// otherwise byte-identical Claude/Codex configuration.
		value = struct {
			SchemaVersion  int
			ConfigRevision string
			Concurrency    Concurrency
			Merge          Merge
			Memhub         Memhub
			Metrics        Metrics
			Hosts          struct {
				Codex  *Host
				Claude *Host
			}
			Overrides []string
		}{
			SchemaVersion: shadow.SchemaVersion,
			Concurrency:   shadow.Concurrency,
			Merge:         shadow.Merge,
			Memhub:        shadow.Memhub,
			Metrics:       shadow.Metrics,
			Hosts: struct {
				Codex  *Host
				Claude *Host
			}{shadow.Hosts.Codex, shadow.Hosts.Claude},
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode configuration for revision: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])[:12], nil
}
