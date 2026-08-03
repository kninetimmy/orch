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
// creates, together with the sibling host's existing file the new one
// may be seeded from.
type seedOffer struct {
	host    string
	file    string // host's root instruction file, absent today
	sibling string // the other host's file, which carries conventions
}

// seedOffers lists, in claude-then-codex order (applicableInstructionFiles'
// fixed order, so documents and summary rows stay deterministic), every
// host newlyEnabled marks whose own root instruction file is absent
// while the other host's file carries content outside the Orch managed
// block. That is the exact condition the seed question is asked under:
// without it Orch creates a file holding nothing but the managed block,
// leaving that host's agents with no project conventions at all — the
// state `orch doctor` reports after the fact.
//
// The sibling need not itself be an enabled host. A repository that
// states its conventions in AGENTS.md and enables only Claude Code has
// exactly the same problem, and the same answer.
//
// seedOffers reads nothing: facts.Instructions is Detect's snapshot, so
// the question sequence stays a pure function of facts and answers.
func seedOffers(facts Facts, newlyEnabled map[string]bool) []seedOffer {
	var offers []seedOffer
	for _, host := range []string{"claude", "codex"} {
		sibling := siblingHost(host)
		if !newlyEnabled[host] || facts.Instructions[host].Exists || !facts.Instructions[sibling].Conventions {
			continue
		}
		offers = append(offers, seedOffer{host: host, file: InstructionFile(host), sibling: InstructionFile(sibling)})
	}
	return offers
}

// seedDocs turns every applicable offer into its own single-question
// document, appended last in both interviews' sequences — the file
// question sits closest to the summary that shows its result.
func seedDocs(facts Facts, newlyEnabled map[string]bool) []docSpec {
	var docs []docSpec
	for _, o := range seedOffers(facts, newlyEnabled) {
		docs = append(docs, docSpec{questions: []question.Question{seedQuestion(o)}})
	}
	return docs
}

// seedQuestion is o's yes/no question, naming both files and defaulting
// to yes: a repository that already states its conventions somewhere
// almost always wants the file being created to carry them too, and
// either answer's full proposed content is shown as the summary's file
// diff before anything is written.
//
// The seeded content is a verbatim copy rather than an import
// directive: an import is host-specific syntax, and Orch does not
// author a host's instruction dialect.
func seedQuestion(o seedOffer) question.Question {
	return question.Question{
		ID:     seedID(o.host),
		Header: "Conventions",
		Prompt: fmt.Sprintf("Seed %s from %s?", o.file, o.sibling),
		Preamble: fmt.Sprintf(
			"%s already states this repository's conventions outside its own Orch managed block, while %s does not exist yet. Seeding copies that content verbatim into the new file, above a fresh managed block, so %s agents read the same conventions; answering no creates %s holding the managed block alone.",
			o.sibling, o.file, hostLabels[o.host], o.file),
		Kind:    question.KindSelect,
		Default: "yes",
		Options: yesNoOptions("yes"),
	}
}

// seedFiles maps each proposed instruction file name to the sibling
// file name it is seeded from, for every offer this session's answers
// accepted — buildSummary's and buildConfigureSummary's seeds argument.
// An unanswered or declined offer is simply absent, which is what makes
// "no" identical to the behavior before seeding existed.
func seedFiles(facts Facts, newlyEnabled map[string]bool, answers map[string]string) map[string]string {
	seeds := map[string]string{}
	for _, o := range seedOffers(facts, newlyEnabled) {
		if answers[seedID(o.host)] == "yes" {
			seeds[o.file] = o.sibling
		}
	}
	return seeds
}

// seededPlanFile returns planInstructionFiles' plan function for a
// session carrying seeds: instructions.PlanFile for every file, except
// that a file seeds names is planned from the sibling's conventions
// (planSeeded) instead of from nothing. With no seeds it is
// instructions.PlanFile itself, so every path that never offered
// seeding plans exactly as it did before.
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
// The offer is only ever made for a path Detect found absent, so the
// change is an install against nothing: Old is "" and the diff shows
// the whole proposed file, seeded lines included, rather than the block
// alone against borrowed context. A path that exists after all falls
// back to the ordinary plan — seeding must never overwrite a file
// somebody already wrote.
func planSeeded(siblingPath, path string) (instructions.Change, error) {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
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
	ch.FileExisted = false
	ch.Old = ""
	ch.Diff = instructions.UnifiedDiff("", ch.New)
	return ch, nil
}
