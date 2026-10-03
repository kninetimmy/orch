// Package evalcorpus prepares maintainer-owned reference packets. It does not
// run workers, grade prose, or enforce a model/operating-system boundary.
package evalcorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// File pins either a historical Git blob or a corpus-authored artifact.
// Commit is empty for artifacts relative to the corpus directory.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Commit string `json:"commit,omitempty"`
	SHA256 string `json:"sha256"`
}

type Control struct {
	Name             string   `json:"name"`
	Purpose          string   `json:"purpose"`
	Files            []File   `json:"files"`
	Patch            *File    `json:"patch,omitempty"`
	ExpectedFailures []string `json:"expected_failures"`
}

type Case struct {
	ID             string    `json:"id"`
	Version        int       `json:"version"`
	Role           string    `json:"role"`
	Partition      string    `json:"partition"`
	Lineage        string    `json:"lineage"`
	Difficulty     string    `json:"difficulty"`
	Classification string    `json:"classification,omitempty"`
	SourceCommit   string    `json:"source_commit"`
	Upstream       []string  `json:"upstream"`
	Selection      string    `json:"selection"`
	Inputs         []File    `json:"inputs"`
	PacketSHA256   string    `json:"packet_sha256"`
	Key            File      `json:"key"`
	Probe          File      `json:"probe"`
	Command        []string  `json:"command"`
	CheckSeconds   int       `json:"check_seconds"`
	Alternative    string    `json:"alternative"`
	Controls       []Control `json:"controls"`
}

type Manifest struct {
	Version       int      `json:"version"`
	Author        string   `json:"author"`
	PreparedDate  string   `json:"prepared_date"`
	Prerequisites []string `json:"prerequisites"`
	Cases         []Case   `json:"cases"`
}

var oidPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func decode(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var tail any
	if err := dec.Decode(&tail); err != io.EOF {
		return fmt.Errorf("trailing data (want EOF): %v", err)
	}
	return nil
}

// Load validates the versioned structure without consulting historical objects.
func Load(data []byte) (Manifest, error) {
	var m Manifest
	if err := decode(data, &m); err != nil {
		return m, fmt.Errorf("corpus manifest: %w", err)
	}
	if m.Version != 1 || m.Author == "" || m.PreparedDate == "" || len(m.Prerequisites) == 0 || len(m.Cases) != 12 {
		return m, fmt.Errorf("corpus manifest: require version 1, author, date, prerequisites and exactly twelve cases")
	}
	ids, lineages := map[string]bool{}, map[string]string{}
	counts, reviews, difficulties := map[string]int{}, map[string]int{}, map[string]bool{}
	for _, c := range m.Cases {
		if ids[c.ID] || safePath(c.ID, false) != nil || strings.Contains(c.ID, "/") || c.Version < 1 {
			return m, fmt.Errorf("case %q: invalid or duplicate identity/version", c.ID)
		}
		ids[c.ID] = true
		if !slices.Contains([]string{"scout", "implementation", "review"}, c.Role) || !slices.Contains([]string{"development", "held-out"}, c.Partition) {
			return m, fmt.Errorf("case %s: invalid role/partition", c.ID)
		}
		if c.Lineage == "" || (lineages[c.Lineage] != "" && lineages[c.Lineage] != c.Partition) {
			return m, fmt.Errorf("case %s: missing lineage or lineage crosses partitions", c.ID)
		}
		lineages[c.Lineage] = c.Partition
		counts[c.Partition+"/"+c.Role]++
		if c.Role == "review" {
			if c.Classification != "clean" && c.Classification != "defective" {
				return m, fmt.Errorf("case %s: review classification required", c.ID)
			}
			reviews[c.Partition+"/"+c.Classification]++
		} else if c.Classification != "" {
			return m, fmt.Errorf("case %s: review classification on non-review", c.ID)
		}
		if c.Role == "implementation" {
			difficulties[c.Difficulty] = true
		}
		if !oidPattern.MatchString(c.SourceCommit) || len(c.Upstream) == 0 || c.Selection == "" || c.Difficulty == "" || c.Alternative == "" {
			return m, fmt.Errorf("case %s: incomplete provenance/selection/alternative", c.ID)
		}
		if c.CheckSeconds < 1 || c.CheckSeconds > 120 || !slices.Equal(c.Command, []string{"go", "test", "-json", "-count=1", "-timeout=30s", "-run=^TestCorpus$", "./..."}) {
			return m, fmt.Errorf("case %s: control command/finite limit differs from v1 contract", c.ID)
		}
		if err := validateFiles(c.Inputs, true); err != nil {
			return m, fmt.Errorf("case %s inputs: %w", c.ID, err)
		}
		public := map[string]string{"TASK.md": "cases/" + c.ID + "/task.md", "ROLE.md": "roles/" + c.Role + ".md", "CONTEXT.md": "public/context.md", "go.mod": "public/go.mod.txt"}
		for _, f := range c.Inputs {
			if f.Commit == "" && public[f.Path] != f.Source {
				return m, fmt.Errorf("case %s: undeclared supplied artifact %s", c.ID, f.Source)
			}
			if f.Commit != "" && f.Commit != c.SourceCommit {
				return m, fmt.Errorf("case %s: worker history differs from source commit", c.ID)
			}
		}
		if c.PacketSHA256 != PacketDigest(c.Inputs) {
			return m, fmt.Errorf("case %s: packet digest mismatch", c.ID)
		}
		for _, required := range []string{"TASK.md", "ROLE.md", "CONTEXT.md"} {
			if !slices.ContainsFunc(c.Inputs, func(f File) bool { return f.Path == required && f.Commit == "" }) {
				return m, fmt.Errorf("case %s: missing supplied instruction %s", c.ID, required)
			}
		}
		if err := validateFiles([]File{c.Key, c.Probe}, false); err != nil {
			return m, fmt.Errorf("case %s external artifacts: %w", c.ID, err)
		}
		if len(c.Controls) < 2 || c.Controls[0].Name != "reference" {
			return m, fmt.Errorf("case %s: reference and bad controls required", c.ID)
		}
		names, bad := map[string]bool{}, false
		for _, control := range c.Controls {
			if names[control.Name] || control.Purpose == "" || safePath(control.Name, false) != nil || strings.Contains(control.Name, "/") {
				return m, fmt.Errorf("case %s: invalid/duplicate control", c.ID)
			}
			names[control.Name] = true
			bad = bad || len(control.ExpectedFailures) > 0
			if err := validateFiles(control.Files, false); err != nil {
				return m, fmt.Errorf("case %s control %s: %w", c.ID, control.Name, err)
			}
			if control.Patch != nil {
				if err := validateFiles([]File{*control.Patch}, false); err != nil {
					return m, err
				}
			}
			for _, failure := range control.ExpectedFailures {
				if !strings.HasPrefix(failure, "TestCorpus/") {
					return m, fmt.Errorf("case %s: failure must name a behavioral subtest", c.ID)
				}
			}
		}
		if !bad {
			return m, fmt.Errorf("case %s: no known-bad behavioral control", c.ID)
		}
	}
	for _, partition := range []string{"development", "held-out"} {
		for _, role := range []string{"scout", "implementation", "review"} {
			if counts[partition+"/"+role] != 2 {
				return m, fmt.Errorf("corpus manifest: want two %s cases in %s", role, partition)
			}
		}
		for _, class := range []string{"clean", "defective"} {
			if reviews[partition+"/"+class] != 1 {
				return m, fmt.Errorf("corpus manifest: want one %s review in %s", class, partition)
			}
		}
	}
	for _, difficulty := range []string{"mechanical", "ordinary", "demanding"} {
		if !difficulties[difficulty] {
			return m, fmt.Errorf("corpus manifest: missing %s implementation", difficulty)
		}
	}
	return m, nil
}

func safePath(p string, worker bool) error {
	if !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\:*?[]\x00\r\n") {
		return fmt.Errorf("invalid relative path %q", p)
	}
	for _, segment := range strings.Split(p, "/") {
		lower := strings.ToLower(segment)
		device := strings.Split(lower, ".")[0]
		if strings.TrimRight(segment, " .") != segment || slices.Contains([]string{"con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9"}, device) {
			return fmt.Errorf("nonportable path %q", p)
		}
		if lower == ".git" || worker && slices.Contains([]string{".memhub", ".orchestrator", "evaluation"}, lower) {
			return fmt.Errorf("forbidden worker path %q", p)
		}
	}
	return nil
}

func validateFiles(files []File, worker bool) error {
	if len(files) == 0 || len(files) > 64 {
		return fmt.Errorf("file list must contain 1..64 entries")
	}
	seen := map[string]bool{}
	for _, f := range files {
		for _, p := range []string{f.Path, f.Source} {
			if err := safePath(p, worker && f.Commit != ""); err != nil {
				return err
			}
		}
		if worker {
			if err := safePath(f.Path, true); err != nil {
				return err
			}
		}
		name := strings.ToLower(f.Path)
		if seen[name] || !digestPattern.MatchString(f.SHA256) || (f.Commit != "" && !oidPattern.MatchString(f.Commit)) {
			return fmt.Errorf("invalid digest/commit or duplicate path %q", f.Path)
		}
		seen[name] = true
		for other := range seen {
			if other != name && (strings.HasPrefix(name, other+"/") || strings.HasPrefix(other, name+"/")) {
				return fmt.Errorf("file/directory path collision %q", f.Path)
			}
		}
	}
	return nil
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PacketDigest hashes sorted "path NUL byte-digest LF" records. It includes
// TASK.md, ROLE.md and CONTEXT.md, and does not include filesystem metadata.
func PacketDigest(files []File) string {
	files = slices.Clone(files)
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "%s\x00%s\n", path.Clean(f.Path), f.SHA256)
	}
	return Digest([]byte(b.String()))
}
