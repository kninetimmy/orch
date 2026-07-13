package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/metrics"
)

// cmdMetrics prints a read-only summary of every recorded metrics
// document (PRD §21, §22): whether metrics are currently enabled, then
// one block per Delivery run metrics.LoadAll finds. It never mutates
// anything and, critically, never creates .orchestrator/metrics —
// LoadAll guarantees that (PRD §23: disabled metrics create no
// storage), so running this command on a repository that has never
// enabled metrics leaves it exactly as it found it.
func cmdMetrics(env Env) error {
	fmt.Fprintf(env.Stdout, "orch:   %s\n", Version)

	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "metrics enabled: %t\n", cfg.Metrics.Enabled)

	docs, err := metrics.LoadAll(env.RepoRoot)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		// Metrics may be disabled right now with no history at all, or
		// disabled after an earlier enabled period left nothing behind
		// (LoadAll only ever reports what is actually on disk); either
		// way an empty history is not an error.
		fmt.Fprintln(env.Stdout, "no metrics recorded.")
		return nil
	}

	for i, doc := range docs {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		printRunSummary(env.Stdout, summarizeRun(doc))
	}
	return nil
}

// runSummary is the printable digest of one metrics.Document, computed
// once so printRunSummary stays pure formatting.
type runSummary struct {
	runID      string
	eventCount int
	firstAt    string
	lastAt     string

	issuesSeen int
	merged     int
	abandoned  int
	blocked    int
	// blockClasses counts block events by class (a re-block on the same
	// issue counts again — it is a fresh event, not a fresh issue).
	blockClasses map[string]int

	escalations int

	// reviewCycles is the total number of review events recorded (each
	// review call is one cycle by construction). reviewedIssues is the
	// number of distinct issues with at least one review event.
	// firstPassApprove counts issues whose first recorded review event
	// approved.
	reviewCycles     int
	reviewedIssues   int
	firstPassApprove int

	// ciByState counts issues by their last recorded ci event's state
	// (polling only ever overwrites, so "last" is "most recent
	// observation", not a first-vs-final judgement).
	ciByState map[string]int

	// usage sums every event that carries a usage payload; usageEvents
	// counts how many of eventCount did.
	usage       metrics.Usage
	usageEvents int
}

// summarizeRun computes runSummary from doc in one pass, trusting the
// document's event order (Append only ever appends, so it is
// insertion — and therefore chronological — order).
func summarizeRun(doc metrics.Document) runSummary {
	s := runSummary{
		runID:        doc.RunID,
		eventCount:   len(doc.Events),
		blockClasses: map[string]int{},
		ciByState:    map[string]int{},
	}
	if len(doc.Events) > 0 {
		s.firstAt = doc.Events[0].At
		s.lastAt = doc.Events[len(doc.Events)-1].At
	}

	issuesSeen := map[int]bool{}
	mergedIssues := map[int]bool{}
	abandonedIssues := map[int]bool{}
	firstReviewVerdict := map[int]string{}
	ciLastState := map[int]string{}

	for _, ev := range doc.Events {
		if ev.IssueNumber != 0 {
			issuesSeen[ev.IssueNumber] = true
		}
		switch ev.Verb {
		case "merge":
			mergedIssues[ev.IssueNumber] = true
		case "abandon":
			abandonedIssues[ev.IssueNumber] = true
		case "block":
			s.blocked++
			s.blockClasses[ev.BlockClass]++
		case "escalate":
			s.escalations++
		case "review":
			s.reviewCycles++
			if _, ok := firstReviewVerdict[ev.IssueNumber]; !ok {
				firstReviewVerdict[ev.IssueNumber] = ev.Verdict
			}
		case "ci":
			ciLastState[ev.IssueNumber] = ev.CIState
		}
		if ev.Usage != nil {
			s.usageEvents++
			s.usage.InputTokens += ev.Usage.InputTokens
			s.usage.OutputTokens += ev.Usage.OutputTokens
			s.usage.CacheReadTokens += ev.Usage.CacheReadTokens
			s.usage.CacheCreationTokens += ev.Usage.CacheCreationTokens
			s.usage.DurationMS += ev.Usage.DurationMS
		}
	}

	s.issuesSeen = len(issuesSeen)
	s.merged = len(mergedIssues)
	s.abandoned = len(abandonedIssues)
	s.reviewedIssues = len(firstReviewVerdict)
	for _, verdict := range firstReviewVerdict {
		if verdict == "approve" {
			s.firstPassApprove++
		}
	}
	for _, state := range ciLastState {
		s.ciByState[state]++
	}
	return s
}

// sortedCounts renders m as "key: value" pairs sorted by key, for
// deterministic output over map iteration.
func sortedCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}

// printRunSummary writes s in status.go's aligned label style.
func printRunSummary(w io.Writer, s runSummary) {
	fmt.Fprintf(w, "run:         %s\n", s.runID)
	fmt.Fprintf(w, "events:      %d (first %s, last %s)\n", s.eventCount, s.firstAt, s.lastAt)

	blockedDetail := ""
	if len(s.blockClasses) > 0 {
		blockedDetail = fmt.Sprintf(" (%s)", sortedCounts(s.blockClasses))
	}
	fmt.Fprintf(w, "issues:      %d seen; merged %d, abandoned %d, blocked %d%s\n", s.issuesSeen, s.merged, s.abandoned, s.blocked, blockedDetail)
	fmt.Fprintf(w, "escalations: %d\n", s.escalations)
	fmt.Fprintf(w, "reviews:     %d cycles; first-pass approve: %d of %d reviewed issues\n", s.reviewCycles, s.firstPassApprove, s.reviewedIssues)

	if len(s.ciByState) > 0 {
		fmt.Fprintf(w, "ci:          %s\n", sortedCounts(s.ciByState))
	} else {
		fmt.Fprintln(w, "ci:          none observed")
	}

	if s.usageEvents > 0 {
		fmt.Fprintf(w, "usage:       input %d, output %d, cache read %d, cache creation %d, duration %dms\n",
			s.usage.InputTokens, s.usage.OutputTokens, s.usage.CacheReadTokens, s.usage.CacheCreationTokens, s.usage.DurationMS)
		fmt.Fprintf(w, "             usage reported on %d of %d events\n", s.usageEvents, s.eventCount)
	}
}
