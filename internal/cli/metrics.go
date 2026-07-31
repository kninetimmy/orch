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

	// usageDetail is one row per event that carries a usage payload, in
	// document order — the per-event, role- and cycle-attributed detail
	// that the run-level usage totals above erase. A summed total hides
	// exactly the signal this exists to show: a per-dispatch or
	// per-cycle cost that rises while the work it is buying shrinks.
	usageDetail []usageEventRow
}

// usageEventRow is one printable per-event usage line: which event
// (its 1-based position in the document) carried the usage, whose
// cost it is (role, when attributable), and — for a review event —
// which review cycle it belongs to.
type usageEventRow struct {
	index       int
	verb        string
	issueNumber int
	role        string
	reviewCycle int // 0 when the event is not a review-verb event
	usage       metrics.Usage
}

// eventRole reports the role display label for ev's usage: its own
// Role field when set (dispatch, activate, pr-open, and a review
// event's executor fix-cycle sibling all carry one — PRD §21's
// attribution), and otherwise "reviewer" for a plain review event's
// own usage — the only other usage-carrying verb, whose cost is
// unambiguously the reviewer's by construction (including on documents
// recorded before this field existed on pr-open, which still report
// "" rather than a guess).
func eventRole(ev metrics.Event) string {
	if ev.Role != "" {
		return ev.Role
	}
	if ev.Verb == "review" {
		return "reviewer"
	}
	return ""
}

// eventReviewCycle reports ev.ReviewCycles for a review-verb event
// (both the verdict-bearing event and its executor-usage sibling carry
// the same cycle number) and 0 for every other verb.
func eventReviewCycle(ev metrics.Event) int {
	if ev.Verb != "review" {
		return 0
	}
	return ev.ReviewCycles
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

	for i, ev := range doc.Events {
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
			// A review-verb event with no verdict is the executor's
			// fix-cycle usage sibling (PRD §21's executor_usage), not a
			// reviewed cycle: only the verdict-bearing event counts
			// toward cycles and first-pass approval, so the sibling
			// never double-counts a cycle it merely shares a number
			// with.
			if ev.Verdict != "" {
				s.reviewCycles++
				if _, ok := firstReviewVerdict[ev.IssueNumber]; !ok {
					firstReviewVerdict[ev.IssueNumber] = ev.Verdict
				}
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
			s.usage.TotalTokens += ev.Usage.TotalTokens
			s.usage.DurationMS += ev.Usage.DurationMS
			s.usageDetail = append(s.usageDetail, usageEventRow{
				index:       i + 1,
				verb:        ev.Verb,
				issueNumber: ev.IssueNumber,
				role:        eventRole(ev),
				reviewCycle: eventReviewCycle(ev),
				usage:       *ev.Usage,
			})
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
		fmt.Fprintf(w, "usage:       input %d, output %d, cache read %d, cache creation %d, total %d, duration %dms\n",
			s.usage.InputTokens, s.usage.OutputTokens, s.usage.CacheReadTokens, s.usage.CacheCreationTokens, s.usage.TotalTokens, s.usage.DurationMS)
		fmt.Fprintf(w, "             usage reported on %d of %d events\n", s.usageEvents, s.eventCount)
		printUsageDetail(w, s.usageDetail)
	}
}

// printUsageDetail writes one line per event that carried usage, in
// document order, attributed by role and — for a review event — by
// review cycle: the per-dispatch/per-cycle detail a run-level total
// erases. The role column reads "unattributed" only for a usage-
// carrying event recorded before this build started attributing it
// (an old pr-open event, for instance) — never a guess at whose cost
// it was.
func printUsageDetail(w io.Writer, rows []usageEventRow) {
	fmt.Fprintln(w, "usage by event:")
	for _, u := range rows {
		role := u.role
		if role == "" {
			role = "unattributed"
		}
		cycle := ""
		if u.reviewCycle > 0 {
			cycle = fmt.Sprintf(" cycle %d", u.reviewCycle)
		}
		fmt.Fprintf(w, "  %3d. %-9s issue #%-5d role %-13s%-9s input %d, output %d, cache read %d, cache creation %d, total %d, duration %dms\n",
			u.index, u.verb, u.issueNumber, role, cycle,
			u.usage.InputTokens, u.usage.OutputTokens, u.usage.CacheReadTokens, u.usage.CacheCreationTokens, u.usage.TotalTokens, u.usage.DurationMS)
	}
}
