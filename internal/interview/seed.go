package interview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// seedID builds host's seed question id. Unlike the ids materialize.go
// ingests, this one names no TOML key: nothing about the answer is
// committed to the configuration — it only chooses what the summary
// proposes for one file. `orch configure`'s own pick.hosts and
// pick.roles.<host> ids are the same kind of non-TOML question id.
func seedID(host string) string { return "seed.instructions." + host }

// siblingHost returns the other host's name.
func siblingHost(host string) string {
	if host == "claude" {
		return "codex"
	}
	return "claude"
}

// seedOffer names one host whose root instruction file this session
// proposes seeding from the sibling host's file, together with that
// sibling. repair distinguishes the two shapes seedOffers admits: false
// for a file this session creates (absent today), true for one that
// already exists holding nothing but the Orch managed block.
type seedOffer struct {
	host    string
	file    string // host's root instruction file
	sibling string // the other host's file, which carries conventions
	repair  bool   // file exists holding only the managed block
}

// seedScope licenses, per host, which of seedOffers' two offers a
// session may make. Both are keyed by host name, so a host absent from
// (or false in) a map is simply never offered that shape.
type seedScope struct {
	// created marks each host whose root instruction file this session
	// creates: the create offer applies when that file is absent.
	created map[string]bool
	// repaired marks each host whose already-present root instruction
	// file this session may rewrite: the repair offer applies when that
	// file holds nothing but the current managed block. `orch init`
	// leaves it nil (initSeedScope) — it runs before any host is
	// configured, and the repair offer is deliberately `orch configure`'s
	// alone, the command `orch doctor` names as this state's repair path.
	repaired map[string]bool
}

// initSeedScope is `orch init`'s scope: every enabled host's file is one
// init creates, and none is one init repairs. Both of init's seed call
// sites (buildSequence's question and nextAfterSequence's seeds) build
// it here, so the question asked and the answer honored can never
// disagree about which offers applied.
func initSeedScope(claudeEnabled, codexEnabled bool) seedScope {
	return seedScope{created: map[string]bool{"claude": claudeEnabled, "codex": codexEnabled}}
}

// seedOffers lists, in claude-then-codex order (applicableInstructionFiles'
// fixed order, so documents and summary rows stay deterministic), every
// host that scope licenses an offer for and whose sibling's file
// carries content outside its own Orch managed block. Two shapes
// qualify, and they are mutually exclusive per host — one requires an
// absent file, the other an existing one:
//
//   - create (scope.created, both interviews): the host's own file is
//     absent. Without the offer Orch creates a file holding nothing but
//     the managed block, leaving that host's agents with no project
//     conventions at all — the state `orch doctor` reports after the
//     fact.
//   - repair (scope.repaired, `orch configure` only): the host's own
//     file already exists and its entire content is the current managed
//     block (InstructionFileState.BlockOnly, deliberately narrower than
//     "exists and carries no conventions"). Before this offer existed
//     that state had no mechanical repair path at all — seeding fired
//     only for an absent file and planSeeded refused every path that
//     existed, so doctor reported it and the only fix was to edit the
//     file by hand; now doctor names `orch configure`, which offers
//     exactly this.
//
// The sibling need not itself be an enabled host. A repository that
// states its conventions in AGENTS.md and enables only Claude Code has
// exactly the same problem, and the same answer.
//
// seedOffers reads nothing: facts.Instructions is Detect's snapshot, so
// the question sequence stays a pure function of facts and answers.
func seedOffers(facts Facts, scope seedScope) []seedOffer {
	var offers []seedOffer
	for _, host := range []string{"claude", "codex"} {
		sibling := siblingHost(host)
		if !facts.Instructions[sibling].Conventions {
			continue
		}
		create := scope.created[host] && !facts.Instructions[host].Exists
		repair := scope.repaired[host] && facts.Instructions[host].BlockOnly
		if !create && !repair {
			continue
		}
		offers = append(offers, seedOffer{host: host, file: InstructionFile(host), sibling: InstructionFile(sibling), repair: repair})
	}
	return offers
}

// seedDocs turns every applicable offer into its own single-question
// document, appended last in both interviews' sequences — the file
// question sits closest to the summary that shows its result.
func seedDocs(facts Facts, scope seedScope) []docSpec {
	var docs []docSpec
	for _, o := range seedOffers(facts, scope) {
		docs = append(docs, docSpec{questions: []question.Question{seedQuestion(o)}})
	}
	return docs
}

// seedQuestion is o's yes/no question, naming both files and defaulting
// to yes: a repository that already states its conventions somewhere
// almost always wants this host's file to carry them too, and either
// answer's full proposed content is shown as the summary's file diff
// before anything is written. A repair offer says so — the file it names
// exists, and answering yes rewrites it — while an absent file's
// wording stays exactly what it was before repair existed.
//
// The seeded content is a verbatim copy rather than an import
// directive: an import is host-specific syntax, and Orch does not
// author a host's instruction dialect.
func seedQuestion(o seedOffer) question.Question {
	prompt := fmt.Sprintf("Seed %s from %s?", o.file, o.sibling)
	preamble := fmt.Sprintf(
		"%s already states this repository's conventions outside its own Orch managed block, while %s does not exist yet. Seeding copies that content verbatim into the new file, above a fresh managed block, so %s agents read the same conventions; answering no creates %s holding the managed block alone.",
		o.sibling, o.file, hostLabels[o.host], o.file)
	if o.repair {
		prompt = fmt.Sprintf("Repair %s from %s?", o.file, o.sibling)
		preamble = fmt.Sprintf(
			"%s holds the Orch managed block and nothing else, so %s agents see none of this repository's conventions, while %s states them outside its own block. Seeding copies that content verbatim above %s's managed block, and the whole proposed file is shown as a diff for approval first; answering no leaves %s exactly as it is.",
			o.file, hostLabels[o.host], o.sibling, o.file, o.file)
	}
	return question.Question{
		ID:       seedID(o.host),
		Header:   "Conventions",
		Prompt:   prompt,
		Preamble: preamble,
		Kind:     question.KindSelect,
		Default:  "yes",
		Options:  yesNoOptions("yes"),
	}
}

// seedFiles maps each proposed instruction file name to the sibling
// file name it is seeded from, for every offer this session's answers
// accepted — buildSummary's and buildConfigureSummary's seeds argument.
// An unanswered or declined offer is simply absent, which is what makes
// "no" identical to the behavior before seeding existed.
func seedFiles(facts Facts, scope seedScope, answers map[string]string) map[string]string {
	seeds := map[string]string{}
	for _, o := range seedOffers(facts, scope) {
		if answers[seedID(o.host)] == "yes" {
			seeds[o.file] = o.sibling
		}
	}
	return seeds
}

// seededPlanFile returns planInstructionFiles' plan function for a
// session carrying seeds: instructions.PlanFile for every file, except
// that a file seeds names is planned from the sibling's conventions
// (planSeeded) instead of from its own content or absence. With no
// seeds it is instructions.PlanFile itself, so every path that never
// offered seeding plans exactly as it did before.
func seededPlanFile(repoRoot string, seeds map[string]string) func(string) (instructions.Change, error) {
	if len(seeds) == 0 {
		return instructions.PlanFile
	}
	return func(path string) (instructions.Change, error) {
		sibling, ok := seeds[filepath.Base(path)]
		if !ok {
			return instructions.PlanFile(path)
		}
		return planSeeded(filepath.Join(repoRoot, sibling), path)
	}
}

// planSeeded proposes path's content as siblingPath's content outside
// its own managed block, followed by a fresh managed block — exactly
// one managed block by construction, since PlanRemove strips whatever
// block the sibling carried and Plan then appends the current one to a
// remainder that provably carries no markers (locate rejects a second
// pair as malformed).
//
// Whether path may be seeded at all is decided by seedable, from disk,
// here — not trusted from Detect's snapshot, and not from the answer,
// which only ever says yes or no. Before repair existed this guard was
// simply "the path must not exist"; it is now "the path must not exist,
// or must hold nothing but the current managed block". Every other
// existing path still falls back to the ordinary plan: seeding must
// never overwrite a file somebody already wrote, and a file that gained
// content between the question and the summary keeps it.
//
// Either way the change is displayed against nothing: Old is "" and the
// diff shows the whole proposed file, seeded lines included, rather
// than the block alone against borrowed context. For a repaired file
// that costs nothing in honesty — every line the diff shows as added is
// a line the approved file holds, and the only ones it re-shows are the
// managed block Orch wrote itself — while FileExisted keeps reporting
// what was actually found on disk, so the record bootstrap renders into
// the PR body never calls a repaired file newly created.
func planSeeded(siblingPath, path string) (instructions.Change, error) {
	existed, ok := seedable(path)
	if !ok {
		return instructions.PlanFile(path)
	}
	base, err := instructions.PlanRemoveFile(siblingPath)
	if err != nil {
		return instructions.Change{}, err
	}
	ch, err := instructions.Plan(base.New)
	if err != nil {
		return instructions.Change{}, err
	}
	ch.FileExisted = existed
	ch.Old = ""
	ch.Diff = instructions.UnifiedDiff("", ch.New)
	return ch, nil
}

// seedable reports whether path may be seeded over, and whether it
// exists at all. Exactly two states qualify: an absent path, where
// there is nothing to overwrite, and a path holding nothing but the
// current managed block (isBlockOnly). Everything else fails closed to
// ok == false, a path that cannot even be read included — the caller
// then plans it the ordinary way, which surfaces that read failure as
// an error instead of seeding over bytes this build never saw.
func seedable(path string) (existed, ok bool) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true
	}
	if err != nil {
		return true, false
	}
	return true, isBlockOnly(string(data))
}

// isBlockOnly reports whether content is nothing but the current Orch
// managed block: one unambiguous marker pair, a body matching this
// build's canonical render, and only whitespace around it. It is the
// single classification on which a seed may replace an existing file's
// bytes, so every other shape is false — any content outside the block,
// a drifted or newer-versioned or structurally broken block, and
// content carrying no block at all (an empty file included, which has
// no managed block to be "only").
//
// PlanRemove's DeleteWholeFile answers the "nothing but" half exactly:
// it is set only for a located marker pair whose remainder
// IsOtherwiseEmpty — the same reading `orch doctor`'s block-only
// advisory reports from. Inspect's StatusCurrent then adds what a
// repair needs beyond that advisory: PlanRemove is deliberately
// indifferent to a drifted or newer-versioned body, because removal
// needs only the marker lines, and replacing bytes this build cannot
// vouch for is exactly what must not happen here. So a drifted
// block-only file is reported by doctor and refused by seeding — `orch
// configure` names it as a blocker instead (isBlockingPlanError).
func isBlockOnly(content string) bool {
	ch, err := instructions.PlanRemove(content)
	if err != nil || !ch.DeleteWholeFile {
		return false
	}
	return instructions.Inspect(content).Status == instructions.StatusCurrent
}
