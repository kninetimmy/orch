package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

func runMetrics(env Env, args []string) error {
	if len(args) == 0 {
		return cmdMetrics(env)
	}
	if len(args) != 1 || args[0] != "record" {
		return usageError(fmt.Sprintf("orch metrics: unexpected argument %q; usage: orch metrics [record (JSON on stdin)]", args[0]))
	}
	data, err := io.ReadAll(env.Stdin)
	if err != nil {
		return fmt.Errorf("read observation: %w", err)
	}
	o, err := metrics.ParseObservation(data)
	if err != nil {
		return err
	}
	return withDeliveryMutation(env.RepoRoot, func() error {
		cfg, err := config.Load(env.RepoRoot)
		if err != nil {
			return err
		}
		st, err := state.Load(env.RepoRoot)
		if err != nil {
			return err
		}
		owner, err := lockfile.Inspect(env.RepoRoot)
		if err != nil {
			return err
		}
		if err := state.CheckConsistent(st, owner); err != nil {
			return err
		}
		if st.Run == nil || st.Run.ID != o.RunID {
			return fmt.Errorf("observation run_id %q is not the current Delivery run", o.RunID)
		}
		if o.Host != "" && o.Host != st.Run.Host {
			return fmt.Errorf("observation host %q does not match run host %q", o.Host, st.Run.Host)
		}
		if o.IssueNumber != 0 {
			matches := 0
			for _, issue := range st.Run.Issues {
				if issue.Number == o.IssueNumber {
					matches++
				}
			}
			if matches != 1 {
				return fmt.Errorf("observation issue #%d does not identify one issue in run %s", o.IssueNumber, o.RunID)
			}
		}
		result := struct {
			SchemaVersion int  `json:"schema_version"`
			Enabled       bool `json:"enabled"`
			Recorded      bool `json:"recorded"`
		}{SchemaVersion: 1, Enabled: cfg.Metrics.Enabled}
		if cfg.Metrics.Enabled {
			result.Recorded, err = metrics.Record(env.RepoRoot, o)
			if err != nil {
				return err
			}
		}
		return json.NewEncoder(env.Stdout).Encode(result)
	})
}

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
		summary, err := summarizeRun(doc)
		if err != nil {
			return fmt.Errorf("summarize metrics for %s: %w", doc.RunID, err)
		}
		printRunSummary(env.Stdout, summary)
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

	legacyUsage []usageEventRow

	usageSamples             []usageSampleRow
	usageGroups              []usageGroup
	recordedUsageRoles       []string
	missingUsageRoles        []string
	usageSessions            []string
	missingUsageSessions     []string
	unknownRoleRecords       int
	incompleteSessionRecords int
	unavailable              []metrics.Observation

	timing           map[string]timingSummary
	reportedOutcomes map[string]int
	engineOutcomes   map[string]int
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

type usageSampleRow struct {
	observation metrics.Observation
	counters    metrics.Counters
}

type usageGroupKey struct {
	host   string
	source string
	stream string
}

type usageGroup struct {
	key      usageGroupKey
	samples  int
	totals   [6]int64
	measured [6]int
}

type timingSummary struct {
	intervals          int
	incompleteSessions int
	sessionEffort      time.Duration
	wallClock          time.Duration
}

type measuredRange struct {
	start time.Time
	end   time.Time
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
func summarizeRun(doc metrics.Document) (runSummary, error) {
	s := runSummary{
		runID:            doc.RunID,
		eventCount:       len(doc.Events),
		blockClasses:     map[string]int{},
		ciByState:        map[string]int{},
		timing:           map[string]timingSummary{},
		reportedOutcomes: map[string]int{},
		engineOutcomes:   map[string]int{},
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
	expectedUsageRoles := map[string]bool{"architect": true}
	recordedUsageRoles := map[string]bool{}
	knownSessions := map[string]bool{}
	usageSessions := map[string]bool{}

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
			s.engineOutcomes["escalation"]++
		case "review":
			// A review-verb event with no verdict is the executor's
			// fix-cycle usage sibling (PRD §21's executor_usage), not a
			// reviewed cycle: only the verdict-bearing event counts
			// toward cycles and first-pass approval, so the sibling
			// never double-counts a cycle it merely shares a number
			// with.
			if ev.Verdict != "" {
				s.reviewCycles++
				expectedUsageRoles["reviewer"] = true
				if _, ok := firstReviewVerdict[ev.IssueNumber]; !ok {
					firstReviewVerdict[ev.IssueNumber] = ev.Verdict
				}
				if ev.Verdict == "approve" {
					s.engineOutcomes["approval"]++
				}
			}
		case "ci":
			ciLastState[ev.IssueNumber] = ev.CIState
		}
		if ev.Usage != nil {
			role := eventRole(ev)
			if role != "" {
				expectedUsageRoles[role] = true
			}
			s.legacyUsage = append(s.legacyUsage, usageEventRow{
				index:       i + 1,
				verb:        ev.Verb,
				issueNumber: ev.IssueNumber,
				role:        role,
				reviewCycle: eventReviewCycle(ev),
				usage:       *ev.Usage,
			})
		}
		if ev.Verb == "dispatch" && ev.Role != "" {
			expectedUsageRoles[ev.Role] = true
		}
	}

	contributions, err := metrics.CounterContributions(doc.Observations)
	if err != nil {
		return s, err
	}
	groups := map[usageGroupKey]*usageGroup{}
	timingRanges := map[string][]measuredRange{}
	sessionRanges := map[string]map[string][]measuredRange{}
	for i, o := range doc.Observations {
		if o.Role == "" {
			s.unknownRoleRecords++
		} else {
			expectedUsageRoles[o.Role] = true
		}
		if o.Host == "" || o.Session == "" {
			s.incompleteSessionRecords++
		} else {
			knownSessions[nativeSession(o)] = true
		}
		if o.Unavailable != nil {
			s.unavailable = append(s.unavailable, o)
		}
		if o.Outcome != "" {
			s.reportedOutcomes[o.Outcome]++
		}
		if o.Sample != nil {
			s.usageSamples = append(s.usageSamples, usageSampleRow{observation: o, counters: contributions[i]})
			usageSessions[nativeSession(o)] = true
			if o.Role != "" {
				recordedUsageRoles[o.Role] = true
			}
			key := usageGroupKey{host: o.Host, source: o.Source, stream: o.Sample.Stream}
			group := groups[key]
			if group == nil {
				group = &usageGroup{key: key}
				groups[key] = group
			}
			group.samples++
			for n, value := range counterValues(contributions[i]) {
				if value == nil {
					continue
				}
				if *value > math.MaxInt64-group.totals[n] {
					return s, fmt.Errorf("compatible usage total overflow for %s/%s/%s", key.host, key.source, key.stream)
				}
				group.totals[n] += *value
				group.measured[n]++
			}
		}
		if o.Interval != nil {
			start, err := time.Parse(time.RFC3339Nano, o.Interval.Start)
			if err != nil {
				return s, err
			}
			end, err := time.Parse(time.RFC3339Nano, o.Interval.End)
			if err != nil {
				return s, err
			}
			r := measuredRange{start: start, end: end}
			kind := o.Interval.Kind
			timingRanges[kind] = append(timingRanges[kind], r)
			if o.Host != "" && o.Session != "" {
				if sessionRanges[kind] == nil {
					sessionRanges[kind] = map[string][]measuredRange{}
				}
				sessionRanges[kind][o.Host+"\x00"+o.Session] = append(sessionRanges[kind][o.Host+"\x00"+o.Session], r)
			}
		}
	}

	for _, group := range groups {
		s.usageGroups = append(s.usageGroups, *group)
	}
	sort.Slice(s.usageGroups, func(i, j int) bool {
		a, b := s.usageGroups[i].key, s.usageGroups[j].key
		return a.host < b.host || a.host == b.host && (a.source < b.source || a.source == b.source && a.stream < b.stream)
	})
	s.recordedUsageRoles = sortedSet(recordedUsageRoles)
	for role := range expectedUsageRoles {
		if !recordedUsageRoles[role] {
			s.missingUsageRoles = append(s.missingUsageRoles, role)
		}
	}
	sort.Strings(s.missingUsageRoles)
	s.usageSessions = sortedSet(usageSessions)
	for session := range knownSessions {
		if !usageSessions[session] {
			s.missingUsageSessions = append(s.missingUsageSessions, session)
		}
	}
	sort.Strings(s.missingUsageSessions)
	for _, kind := range intervalKinds {
		if len(timingRanges[kind]) == 0 {
			continue
		}
		wall, err := unionDuration(timingRanges[kind])
		if err != nil {
			return s, fmt.Errorf("%s wall-clock duration: %w", kind, err)
		}
		t := timingSummary{intervals: len(timingRanges[kind]), wallClock: wall}
		for _, ranges := range sessionRanges[kind] {
			d, err := unionDuration(ranges)
			if err != nil || d > time.Duration(math.MaxInt64)-t.sessionEffort {
				return s, fmt.Errorf("%s session-summed duration overflow", kind)
			}
			t.sessionEffort += d
		}
		t.incompleteSessions = t.intervals
		for _, ranges := range sessionRanges[kind] {
			t.incompleteSessions -= len(ranges)
		}
		s.timing[kind] = t
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
	return s, nil
}

var intervalKinds = []string{"active-agent", "verification", "ci-waiting", "human-waiting"}

var counterLabels = [6]string{"input", "output", "cache read", "cache creation", "total", "reasoning output"}

func counterValues(c metrics.Counters) [6]*int64 {
	return [6]*int64{c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheCreationTokens, c.TotalTokens, c.ReasoningOutputTokens}
}

func nativeSession(o metrics.Observation) string {
	if o.Session == "" {
		return "unknown"
	}
	host := o.Host
	if host == "" {
		host = "unknown-host"
	}
	return host + "/" + o.Session
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func unionDuration(ranges []measuredRange) (time.Duration, error) {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start.Before(ranges[j].start) })
	start, end := ranges[0].start, ranges[0].end
	var total time.Duration
	add := func(d time.Duration) error {
		if d > time.Duration(math.MaxInt64)-total {
			return fmt.Errorf("overflow")
		}
		total += d
		return nil
	}
	duration := func(start, end time.Time) (time.Duration, error) {
		d := end.Sub(start)
		if !start.Add(d).Equal(end) {
			return 0, fmt.Errorf("overflow")
		}
		return d, nil
	}
	for _, r := range ranges[1:] {
		if !r.start.After(end) {
			if r.end.After(end) {
				end = r.end
			}
			continue
		}
		d, err := duration(start, end)
		if err != nil {
			return 0, err
		}
		if err := add(d); err != nil {
			return 0, err
		}
		start, end = r.start, r.end
	}
	d, err := duration(start, end)
	if err != nil {
		return 0, err
	}
	if err := add(d); err != nil {
		return 0, err
	}
	return total, nil
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
	if s.eventCount == 0 {
		fmt.Fprintln(w, "events:      0")
	} else {
		fmt.Fprintf(w, "events:      %d (first %s, last %s)\n", s.eventCount, s.firstAt, s.lastAt)
	}

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

	printLegacyUsage(w, s.legacyUsage)
	printObservedUsage(w, s)
	printTiming(w, s.timing)
	printOutcomes(w, s.reportedOutcomes, s.engineOutcomes)
}

func printLegacyUsage(w io.Writer, rows []usageEventRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "legacy usage: none recorded")
		return
	}
	fmt.Fprintln(w, "legacy usage by event (host/source/session unavailable; no combined total):")
	for _, u := range rows {
		role := u.role
		if role == "" {
			role = "unattributed"
		}
		cycle := ""
		if u.reviewCycle > 0 {
			cycle = fmt.Sprintf("; review cycle %d", u.reviewCycle)
		}
		fmt.Fprintf(w, "  %d. %s; issue #%d; role %s%s; %s; unclassified reported duration %s\n",
			u.index, u.verb, u.issueNumber, role, cycle, formatCounters(u.usage.Counters()), formatInt64(u.usage.ReportedDurationMS(), "ms"))
	}
}

func printObservedUsage(w io.Writer, s runSummary) {
	if len(s.usageSamples) == 0 {
		fmt.Fprintln(w, "observed usage: none recorded; native counters unknown")
	} else {
		fmt.Fprintln(w, "observed usage by sample:")
		for _, row := range s.usageSamples {
			o := row.observation
			fmt.Fprintf(w, "  %s; %s; source %s; requested [%s]; observed [%s]; %s\n",
				o.ID, observationAttribution(o), o.Source, formatProfile(o.Requested), formatProfile(o.Observed), formatCounters(row.counters))
		}
		fmt.Fprintln(w, "observed usage totals by compatible host/source/stream:")
		for _, group := range s.usageGroups {
			parts := make([]string, len(counterLabels))
			for i, label := range counterLabels {
				if group.measured[i] == 0 {
					parts[i] = label + " unknown (0/" + fmt.Sprint(group.samples) + " measured)"
				} else {
					parts[i] = fmt.Sprintf("%s %d (%d/%d measured)", label, group.totals[i], group.measured[i], group.samples)
				}
			}
			fmt.Fprintf(w, "  host %s; source %s; stream %s; %s\n", group.key.host, group.key.source, group.key.stream, strings.Join(parts, ", "))
		}
	}
	fmt.Fprintf(w, "coverage: observed usage roles recorded: %s\n", listOrNone(s.recordedUsageRoles))
	fmt.Fprintf(w, "          roles without recorded counters: %s\n", listOrNone(s.missingUsageRoles))
	fmt.Fprintf(w, "          native sessions with counters: %s\n", listOrNone(s.usageSessions))
	fmt.Fprintf(w, "          known native sessions without counters: %s; complete native session count unknown\n", listOrNone(s.missingUsageSessions))
	fmt.Fprintf(w, "          observations with unknown role: %d; incomplete host/session: %d\n", s.unknownRoleRecords, s.incompleteSessionRecords)
	if len(s.unavailable) > 0 {
		fmt.Fprintln(w, "unavailable evidence:")
		for _, o := range s.unavailable {
			fmt.Fprintf(w, "  %s; %s; reason %s\n", o.ID, observationAttribution(o), o.Unavailable.Reason)
		}
	}
}

func printTiming(w io.Writer, timing map[string]timingSummary) {
	fmt.Fprintln(w, "measured timing:")
	for _, kind := range intervalKinds {
		t, ok := timing[kind]
		if !ok {
			fmt.Fprintf(w, "  %s: unknown (no measured intervals)\n", kind)
			continue
		}
		sessionMeasurement := "session-summed " + t.sessionEffort.String()
		if t.incompleteSessions == t.intervals {
			sessionMeasurement = "session-summed unknown (no complete host/session intervals)"
		} else if t.incompleteSessions > 0 {
			sessionMeasurement = "known-session subtotal " + t.sessionEffort.String()
		}
		fmt.Fprintf(w, "  %s: %s; wall-clock %s; %d intervals; %d incomplete host/session intervals excluded from session sum\n",
			kind, sessionMeasurement, t.wallClock, t.intervals, t.incompleteSessions)
	}
}

func printOutcomes(w io.Writer, reported, engine map[string]int) {
	if len(reported) == 0 {
		fmt.Fprintln(w, "reported outcomes: none recorded")
	} else {
		fmt.Fprintf(w, "reported outcomes: %s\n", sortedCounts(reported))
	}
	if len(engine) == 0 {
		fmt.Fprintln(w, "engine outcomes: none recorded")
	} else {
		fmt.Fprintf(w, "engine outcomes: %s\n", sortedCounts(engine))
	}
	fmt.Fprintln(w, "other historical events remain unclassified outcomes")
}

func observationAttribution(o metrics.Observation) string {
	issue := "run-level"
	if o.IssueNumber != 0 {
		issue = fmt.Sprintf("issue #%d", o.IssueNumber)
	}
	role := o.Role
	if role == "" {
		role = "unknown"
	}
	attempt := o.Attempt
	if attempt == "" {
		attempt = "unknown"
	}
	cycle := "unknown"
	if o.ReviewCycle != 0 {
		cycle = fmt.Sprint(o.ReviewCycle)
	}
	return fmt.Sprintf("%s; role %s; native session %s; attempt %s; review cycle %s", issue, role, nativeSession(o), attempt, cycle)
}

func formatProfile(p *metrics.Profile) string {
	if p == nil {
		return "unknown"
	}
	model := p.Model
	if model == "" {
		model = "unknown"
	}
	profile := "execution profile unknown"
	switch {
	case p.Effort != "":
		profile = "effort " + p.Effort
	case p.Variant != nil && *p.Variant == "":
		profile = "variant none"
	case p.Variant != nil:
		profile = "variant " + *p.Variant
	}
	return "model " + model + ", " + profile
}

func formatCounters(c metrics.Counters) string {
	values := counterValues(c)
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = counterLabels[i] + " " + formatInt64(value, "")
	}
	return strings.Join(parts, ", ")
}

func formatInt64(value *int64, suffix string) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d%s", *value, suffix)
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
