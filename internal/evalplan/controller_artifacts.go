package evalplan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kninetimmy/orch/internal/codexnative"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/paths"
)

const maxAttemptArtifactBytes = 16 * 1024 * 1024

type caseSource struct {
	definition evalcorpus.Case
	public     map[string][]byte
	private    map[string][]byte
}

func credentialLocations() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("credential locations unavailable: %v", err)
	}
	names := []string{}
	for _, base := range []string{home, os.Getenv("HOME"), os.Getenv("USERPROFILE")} {
		if base == "" {
			continue
		}
		for _, name := range []string{".codex", ".claude", ".ssh", ".aws", ".azure", ".config/gh", ".config/opencode"} {
			names = append(names, filepath.Join(base, filepath.FromSlash(name)))
		}
	}
	if name := os.Getenv("CODEX_HOME"); name != "" {
		names = append(names, name)
	}
	if name := os.Getenv("APPDATA"); name != "" {
		names = append(names, filepath.Join(name, "GitHub CLI"))
	}
	// Duplicate environment aliases are expected; canonicalize and deduplicate
	// before the ordinary strict path-list validator sees them.
	unique := []string{}
	for _, name := range names {
		name, err := localPath("", name, true)
		if err != nil {
			return nil, err
		}
		name, err = directory(name, true)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(unique, func(previous string) bool { same, err := samePath(previous, name); return err == nil && same }) {
			unique = append(unique, name)
		}
	}
	return unique, nil
}

func protectedSources(p Plan) []string {
	protected := append(slices.Clone(p.RepositoryRoots), p.GitDirectories...)
	protected = append(protected, p.StorageRoot, filepath.Dir(p.Corpus.Path), p.Baseline.Profile.Path, p.DecisionRule.Path)
	if p.Candidate != nil {
		protected = append(protected, p.Candidate.Profile.Path)
	}
	if p.Instructions != nil {
		for _, artifact := range *p.Instructions {
			protected = append(protected, filepath.Dir(artifact.Path))
		}
	}
	if p.ProtectedRoots != nil {
		protected = append(protected, *p.ProtectedRoots...)
	}
	for _, artifact := range []*Artifact{p.Readiness.Exposure, p.Readiness.IndependentValidation, p.Readiness.NativeExecution} {
		if artifact != nil {
			protected = append(protected, artifact.Path)
		}
	}
	return protected
}

func overlaps(a, b string) (bool, error) {
	in, err := paths.Inside(a, b)
	if err != nil || in {
		return in, err
	}
	return paths.Inside(b, a)
}

func runtimeRoots(p Plan) ([]string, error) {
	if len(p.WorkerRoots) == 0 || len(p.ScratchRoots) == 0 {
		return nil, fmt.Errorf("controller preparation requires existing worker and scratch parents; preview alone permits absent scratch roots")
	}
	credentials, err := credentialLocations()
	if err != nil {
		return nil, err
	}
	protected := append(protectedSources(p), credentials...)
	allowed := append(slices.Clone(p.WorkerRoots), p.ScratchRoots...)
	for i, name := range allowed {
		g, err := openGuarded(name)
		if err != nil {
			return nil, fmt.Errorf("worker/scratch parent: %w", err)
		}
		g.close()
		for _, other := range append(slices.Clone(protected), allowed[:i]...) {
			if in, err := overlaps(name, other); err != nil || in {
				return nil, fmt.Errorf("worker/scratch parent overlaps protected or sibling root %s: %v", other, err)
			}
		}
	}
	return credentials, nil
}

func loadSources(ctx context.Context, repo string, p Plan) (map[string]caseSource, error) {
	credentials, err := runtimeRoots(p)
	if err != nil {
		return nil, err
	}
	_, data, err := readArtifact("", p.Corpus)
	if err != nil {
		return nil, err
	}
	var manifest evalcorpus.Manifest
	if err := strictJSON(data, &manifest); err != nil {
		return nil, err
	}
	manifest, err = evalcorpus.Load(data)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, c := range p.Cases {
		selected[c.ID] = true
	}
	result := map[string]caseSource{}
	total := 0
	for _, c := range manifest.Cases {
		if !selected[c.ID] {
			continue
		}
		s := caseSource{definition: c, public: map[string][]byte{}, private: map[string][]byte{}}
		for _, f := range c.Inputs {
			for _, name := range []string{f.Path, f.Source} {
				for _, component := range strings.Split(strings.ToLower(name), "/") {
					if slices.Contains([]string{".codex", ".claude", ".ssh", ".aws", ".azure", ".gnupg", ".netrc", ".npmrc", ".env", "auth.json", "credentials"}, component) {
						return nil, fmt.Errorf("credential material forbidden in public packet: %s", name)
					}
				}
			}
		}
		caseBytes, fileCount := 0, 0
		read := func(files []evalcorpus.File, prefix string, dest map[string][]byte) error {
			for _, f := range files {
				if err := ctx.Err(); err != nil {
					return err
				}
				fileCount++
				if fileCount > 256 {
					return fmt.Errorf("case %s exceeds 256 source artifacts", c.ID)
				}
				var bytes []byte
				var err error
				if f.Commit != "" {
					bytes, err = evalcorpus.ReadHistoricalFile(ctx, repo, f)
				} else {
					if err := relativeName(f.Source); err != nil {
						return err
					}
					name := filepath.Join(filepath.Dir(p.Corpus.Path), filepath.FromSlash(f.Source))
					for _, credential := range credentials {
						if in, err := paths.Inside(credential, name); err != nil || in {
							return fmt.Errorf("corpus source overlaps a credential location: %v", err)
						}
					}
					_, bytes, err = readArtifact(filepath.Dir(p.Corpus.Path), Artifact{Path: f.Source, SHA256: f.SHA256})
				}
				if err != nil {
					return fmt.Errorf("case %s source %s: %w", c.ID, f.Source, err)
				}
				caseBytes += len(bytes)
				total += len(bytes)
				if caseBytes > maxAttemptArtifactBytes || total > 64*1024*1024 {
					return fmt.Errorf("source artifact capacity exceeded for case %s", c.ID)
				}
				name := prefix + f.Path
				if err := relativeName(name); err != nil {
					return err
				}
				if _, exists := dest[name]; exists {
					return fmt.Errorf("conflicting source artifact %s", name)
				}
				dest[name] = bytes
			}
			return nil
		}
		if err := read(c.Inputs, "", s.public); err != nil {
			return nil, err
		}
		if err := read([]evalcorpus.File{c.Key}, "private/key/", s.private); err != nil {
			return nil, err
		}
		if err := read([]evalcorpus.File{c.Probe}, "private/probe/", s.private); err != nil {
			return nil, err
		}
		for i, control := range c.Controls {
			prefix := fmt.Sprintf("private/control-%03d/", i+1)
			if err := read(control.Files, prefix+"files/", s.private); err != nil {
				return nil, err
			}
			if control.Patch != nil {
				if err := read([]evalcorpus.File{*control.Patch}, prefix+"patch/", s.private); err != nil {
					return nil, err
				}
			}
		}
		result[c.ID] = s
	}
	if len(result) != len(p.Cases) {
		return nil, fmt.Errorf("selected source cases unavailable")
	}
	return result, nil
}

type preparedAttempt struct {
	controller    *guardedDir
	packet        *guardedDir
	scratch       *guardedDir
	workerParent  *guardedDir
	scratchParent *guardedDir
	name          string
}

func (a *preparedAttempt) close() {
	for _, g := range []*guardedDir{a.controller, a.packet, a.scratch, a.workerParent, a.scratchParent} {
		if g != nil {
			g.close()
		}
	}
}

func prepareAttempt(ctx context.Context, g *guardedDir, e *Evaluation, slot Slot, kind string, source caseSource) (a *preparedAttempt, record AttemptRecord, err error) {
	p := e.Preparation.Plan
	number := len(slot.Attempts)
	a = &preparedAttempt{name: e.ID + "-" + attemptName(slot.Ordinal, number)}
	record = AttemptRecord{SchemaVersion: 1, EvaluationID: e.ID, PlanDigest: e.Preparation.PlanDigest,
		Unit: slot.Unit, Number: number, Kind: kind, PacketSHA256: source.definition.PacketSHA256,
		CaseSHA256: storedDigest(source.definition),
		StartedAt:  now(), Outcome: "started", ExecutionSource: "not-started", Grade: "unknown", Verification: "not-performed",
		Initial: []DigestedFile{}, Artifacts: []DigestedFile{}, Cleanup: Cleanup{Status: "unknown", Detail: "No cleanup observation."}}
	if p.Version == 2 {
		record.SchemaVersion = 2
	}
	if err := ctx.Err(); err != nil {
		return a, record, err
	}
	a.controller, err = g.createDir(attemptName(slot.Ordinal, number))
	if err != nil {
		return a, record, err
	}
	a.workerParent, err = openGuarded(p.WorkerRoots[0])
	if err != nil {
		return a, record, err
	}
	a.scratchParent, err = openGuarded(p.ScratchRoots[0])
	if err != nil {
		return a, record, err
	}
	a.packet, err = a.workerParent.createDir(a.name)
	if err != nil {
		return a, record, err
	}
	record.Packet = a.packet.path
	a.scratch, err = a.scratchParent.createDir(a.name)
	if err != nil {
		return a, record, err
	}
	record.Scratch = a.scratch.path
	if err := a.controller.publish("begun.json", record); err != nil {
		return a, record, err
	}
	if err := a.controller.publish("case.json", source.definition); err != nil {
		return a, record, err
	}
	for _, name := range sortedKeys(source.public) {
		if err := ctx.Err(); err != nil {
			return a, record, err
		}
		bytes := source.public[name]
		if err := a.packet.writeFile(name, bytes); err != nil {
			return a, record, err
		}
		retained := "initial/" + name
		if err := a.controller.writeFile(retained, bytes); err != nil {
			return a, record, err
		}
		record.Initial = append(record.Initial, DigestedFile{retained, evalcorpus.Digest(bytes)})
	}
	for _, name := range sortedKeys(source.private) {
		if err := ctx.Err(); err != nil {
			return a, record, err
		}
		if err := a.controller.writeFile(name, source.private[name]); err != nil {
			return a, record, err
		}
		record.Initial = append(record.Initial, DigestedFile{name, evalcorpus.Digest(source.private[name])})
	}
	if p.Instructions != nil {
		for _, artifact := range *p.Instructions {
			_, bytes, err := readArtifact("", artifact)
			if err != nil {
				return a, record, err
			}
			name := "approved-instructions/" + filepath.Base(artifact.Path)
			if err := a.controller.writeFile(name, bytes); err != nil {
				return a, record, err
			}
			record.Initial = append(record.Initial, DigestedFile{name, artifact.SHA256})
		}
	}
	// Inspect runs over anchored bounded reads, rather than trusting Export's
	// local-mode hygiene as a runtime boundary. No source/Git pointer is copied.
	public, err := snapshot(ctx, a.packet)
	if err != nil || !equalFiles(public, source.public) {
		return a, record, fmt.Errorf("fresh packet does not match its declared public inputs: %v", err)
	}
	return a, record, nil
}

func sortedKeys[V any](items map[string]V) []string {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// snapshot reads worker bytes as data only. It never loads/executes a probe or
// any supplied worker code in the trusted controller process.
func snapshot(ctx context.Context, g *guardedDir) (map[string][]byte, error) {
	files := map[string][]byte{}
	total, count := 0, 0
	var walk func(*guardedDir, string, int) error
	walk = func(dir *guardedDir, prefix string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 16 {
			return fmt.Errorf("worker artifact directory depth exceeds 16")
		}
		entries, err := dir.entries()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > 256 {
				return fmt.Errorf("worker artifacts exceed 256 entries")
			}
			if err := relativeName(entry.Name()); err != nil {
				return err
			}
			name := prefix + entry.Name()
			info, err := dir.root.Lstat(entry.Name())
			if err != nil || reparse(info) {
				return fmt.Errorf("linked or unverifiable worker artifact %s: %v", name, err)
			}
			if info.IsDir() {
				child, err := dir.child(entry.Name())
				if err != nil {
					return err
				}
				before := len(files)
				err = walk(child, name+"/", depth+1)
				child.close()
				if err != nil {
					return err
				}
				if len(files) == before {
					return fmt.Errorf("undeclared empty worker directory: %s", name)
				}
			} else {
				bytes, err := dir.read(entry.Name(), MaxArtifactBytes)
				if err != nil {
					return err
				}
				total += len(bytes)
				if total > maxAttemptArtifactBytes {
					return fmt.Errorf("worker artifacts exceed %d-byte attempt capacity", maxAttemptArtifactBytes)
				}
				files[name] = bytes
			}
		}
		return nil
	}
	err := walk(g, "", 0)
	return files, err
}

func equalFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for name, bytes := range a {
		if other, ok := b[name]; !ok || evalcorpus.Digest(bytes) != evalcorpus.Digest(other) {
			return false
		}
	}
	return true
}

func (a *preparedAttempt) layout(e *Evaluation) (codexnative.IsolationPaths, error) {
	p := e.Preparation.Plan
	credentials, err := runtimeRoots(p)
	if err != nil {
		return codexnative.IsolationPaths{}, err
	}
	protected := protectedSources(p)
	protected = append(protected, p.WorkerRoots[1:]...)
	protected = append(protected, p.ScratchRoots[1:]...)
	for _, parent := range []*guardedDir{a.workerParent, a.scratchParent} {
		entries, err := parent.entries()
		if err != nil {
			return codexnative.IsolationPaths{}, err
		}
		for _, entry := range entries {
			if err := relativeName(entry.Name()); err != nil {
				return codexnative.IsolationPaths{}, err
			}
			if entry.Name() != a.name {
				protected = append(protected, filepath.Join(parent.path, entry.Name()))
			}
		}
	}
	return codexnative.IsolationPaths{Workspace: a.packet.path, Scratch: a.scratch.path,
		MainCheckout: e.Repository, ControllerState: p.StorageRoot,
		SiblingWorkspaces: protected, CredentialPaths: credentials}, nil
}

func retainSnapshot(ctx context.Context, controller *guardedDir, prefix string, files map[string][]byte) ([]DigestedFile, error) {
	result := []DigestedFile{}
	for _, name := range sortedKeys(files) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		retained := prefix + "/" + name
		if err := controller.writeFile(retained, files[name]); err != nil {
			return result, err
		}
		result = append(result, DigestedFile{retained, evalcorpus.Digest(files[name])})
	}
	return result, nil
}

func removeClean(ctx context.Context, g *guardedDir, files map[string][]byte) error {
	// Only exact, controller-created disposable bytes may be removed. A dirty,
	// aliased or inaccessible packet remains evidence rather than being swept.
	observed, err := snapshot(ctx, g)
	if err != nil || !equalFiles(observed, files) {
		return fmt.Errorf("disposable resource changed; preserved: %v", err)
	}
	directories := map[string]bool{}
	for _, name := range sortedKeys(files) {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent, base, err := g.parent(name, false)
		if err != nil {
			return err
		}
		if err := parent.check(); err != nil {
			if parent != g {
				parent.close()
			}
			return err
		}
		err = parent.root.Remove(base)
		if parent != g {
			parent.close()
		}
		if err != nil {
			return err
		}
		for dir := filepath.ToSlash(filepath.Dir(name)); dir != "."; dir = filepath.ToSlash(filepath.Dir(dir)) {
			directories[dir] = true
		}
	}
	names := sortedKeys(directories)
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(b, a) })
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent, base, err := g.parent(name, false)
		if err != nil {
			return err
		}
		if err := parent.check(); err != nil {
			if parent != g {
				parent.close()
			}
			return err
		}
		err = parent.root.Remove(base)
		if parent != g {
			parent.close()
		}
		if err != nil {
			return err
		}
	}
	return g.check()
}

func cleanupAttempt(ctx context.Context, a *preparedAttempt, source caseSource, returned bool, interrupted bool) Cleanup {
	observation := Cleanup{Status: "unknown", Detail: "Native cleanup acknowledgement is unavailable.", WorkerReturned: returned, InterruptionAsked: interrupted}
	if !returned {
		observation.Detail = "Worker did not return within the cleanup deadline; all disposable resources preserved. Native acknowledgement unavailable."
		return observation
	}
	public, packetErr := snapshot(ctx, a.packet)
	scratch, scratchErr := snapshot(ctx, a.scratch)
	if packetErr != nil || scratchErr != nil {
		observation.Status, observation.Detail = "failed", fmt.Sprintf("Disposable inspection failed; resources preserved: %v", errors.Join(packetErr, scratchErr))
		return observation
	}
	if !equalFiles(public, source.public) || len(scratch) != 0 {
		observation.Status, observation.Detail = "preserved-dirty", "Dirty worker packet or scratch retained; native acknowledgement unavailable."
		return observation
	}
	if err := removeClean(ctx, a.packet, source.public); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	if err := removeClean(ctx, a.scratch, map[string][]byte{}); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	// Close only our own handles before removing our verified empty names.
	if err := a.packet.check(); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	if err := a.scratch.check(); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	if err := a.workerParent.check(); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	if err := a.scratchParent.check(); err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	// Root.Remove removes these single directories only, never recursive paths.
	err := errors.Join(a.workerParent.root.Remove(a.name), a.scratchParent.root.Remove(a.name))
	if err != nil {
		observation.Status, observation.Detail = "failed", err.Error()
		return observation
	}
	observation.Status, observation.Detail = "removed-clean", "Controller-owned unchanged packet and empty scratch removed locally; native acknowledgement unavailable."
	return observation
}
