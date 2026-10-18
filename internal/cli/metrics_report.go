package cli

import (
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
)

const unknownValue = "unknown"

// cmdMetricsReport prints one read-only rollup across every recorded run's
// metrics document. Like cmdMetrics it never creates or rewrites storage.
func cmdMetricsReport(env Env) error {
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
		if cfg.Metrics.Enabled {
			fmt.Fprintln(env.Stdout, "no metrics recorded; no report to print.")
		} else {
			fmt.Fprintln(env.Stdout, "metrics are disabled and none are recorded; no report to print.")
		}
		return nil
	}
	report, err := buildMetricsReport(docs)
	if err != nil {
		return err
	}
	printMetricsReport(env.Stdout, report)
	return nil
}

// routeKey is the tuple issue outcomes are grouped by. A dimension a run did
// not record holds unknownValue, which is its own group value.
type routeKey struct {
	role       string
	executor   string
	reviewer   string
	downgraded string
	approval   string
}

// outcomes tallies issue outcomes for one group of issues.
type outcomes struct {
	issues        int
	merged        int
	abandoned     int
	blocked       int
	escalations   int
	reviewed      int
	firstApproved int
	reviewCycles  int
	mergeApproval map[string]int
	// fixCycles counts request-changes reviews that blocked nothing;
	// reviewBlocks counts reviews that blocked their issue, by cause;
	// causeUnrecorded counts request-changes reviews from runs that predate
	// the cause, which may have been either.
	fixCycles       int
	reviewBlocks    map[string]int
	causeUnrecorded int
	// escapedDefects counts distinct merged PRs of these issues that a
	// later plan issue declared it fixes.
	escapedDefects int
}

type routeGroup struct {
	key routeKey
	outcomes
}

// dimension is one value issue outcomes are also broken down by. A run that
// did not record it is grouped under unknownValue.
type dimension struct {
	title string
	value func(*issueFacts) string
}

var reportDimensions = []dimension{
	{"host", func(f *issueFacts) string { return f.host }},
	{"any risk domain declared", func(f *issueFacts) string { return f.riskDomain }},
	{"unusually difficult", func(f *issueFacts) string { return f.difficult }},
	{"downgrade fact mechanical", func(f *issueFacts) string { return f.mechanical }},
	{"downgrade fact low risk", func(f *issueFacts) string { return f.lowRisk }},
	{"downgrade fact fully specified", func(f *issueFacts) string { return f.fullySpecified }},
	{"downgrade fact unsurprising", func(f *issueFacts) string { return f.unsurprising }},
}

type dimensionGroup struct {
	value string
	outcomes
}

type dimensionReport struct {
	title  string
	groups []dimensionGroup
}

func (o *outcomes) add(f *issueFacts) {
	if o.mergeApproval == nil {
		o.mergeApproval = map[string]int{}
		o.reviewBlocks = map[string]int{}
	}
	o.issues++
	if f.merged {
		o.merged++
		o.mergeApproval[f.mergeSource]++
	}
	if f.abandoned {
		o.abandoned++
	}
	if f.blocked {
		o.blocked++
	}
	o.escalations += f.escalations
	if f.firstVerdict != "" {
		o.reviewed++
		o.reviewCycles += f.cycles
		if f.firstVerdict == "approve" {
			o.firstApproved++
		}
	}
	o.fixCycles += f.fixCycles
	o.causeUnrecorded += f.causeUnrecorded
	for cause, n := range f.reviewBlocks {
		o.reviewBlocks[cause] += n
	}
}

// print writes o's outcome lines under an already printed group header.
func (o outcomes) print(w io.Writer) {
	merged := fmt.Sprint(o.merged)
	if o.merged > 0 {
		merged += " (by merge approval: " + sortedCounts(o.mergeApproval) + ")"
	}
	cycles := "unknown (no reviewed issues)"
	if o.reviewed > 0 {
		cycles = fmt.Sprintf("%.2f", float64(o.reviewCycles)/float64(o.reviewed))
	}
	fmt.Fprintf(w, "    issues %d; merged %s; abandoned %d; blocked %d; escalations %d; first-review approvals %d of %d reviewed; review cycles per reviewed issue %s\n",
		o.issues, merged, o.abandoned, o.blocked, o.escalations, o.firstApproved, o.reviewed, cycles)
	wrong, limit := o.reviewBlocks[metrics.BlockCauseWrongCriterion], o.reviewBlocks[metrics.BlockCauseFixCycleLimit]
	fmt.Fprintf(w, "    request-changes fix cycles %d; reviews that blocked their issue %d (wrong criterion %d, fix-cycle limit %d); request-changes reviews with block cause not recorded %d\n",
		o.fixCycles, wrong+limit, wrong, limit, o.causeUnrecorded)
	fmt.Fprintf(w, "    escaped defects attributed to these issues %d\n", o.escapedDefects)
}

// reportUsageKey separates usage recorded on lifecycle events (no host,
// source or stream was ever recorded there) from observed counter samples.
type reportUsageKey struct {
	usageGroupKey
	lifecycle bool
}

type reportUsageGroup struct {
	key        reportUsageKey
	samples    int
	runs       int
	mergedRuns int // merged issues in the runs that contributed samples
	totals     [6]int64
	measured   [6]int
	archTotals [6]int64
	archSeen   [6]int
}

type runElapsed struct {
	runID    string
	elapsed  time.Duration
	measured bool
}

type metricsReport struct {
	runs               int
	routes             []routeGroup
	dimensions         []dimensionReport
	usage              []reportUsageGroup
	runsWithoutArchUse int
	elapsed            []runElapsed
	escapes            []escapedDefect
	// unattributed holds the fixed PRs no recorded merge event names.
	unattributed []int
}

// escapedDefect is one fixed PR attributed to the issue whose merge event
// carries its number.
type escapedDefect struct {
	pr           int
	fixedByRun   string
	fixedByIssue int
	originRun    string
	originIssue  int
	origin       *issueFacts
}

// issueFacts is everything one run recorded about one issue.
type issueFacts struct {
	role, executor, reviewer, downgraded, approval, mergeSource string
	// The run host and the activate event's routing facts, each unknownValue
	// when the run did not record it.
	host, riskDomain, difficult, mechanical, lowRisk, fullySpecified, unsurprising string

	// Request-changes reviews that blocked nothing, reviews that blocked the
	// issue by cause, and request-changes reviews of a run that recorded no
	// cause.
	fixCycles, causeUnrecorded int
	reviewBlocks               map[string]int

	merged, abandoned, blocked bool
	escalations, cycles        int
	firstVerdict               string

	// prNumber is the PR number the merge event recorded (0 when none), and
	// fixes the merged PR numbers the activate event declared this issue fixes.
	prNumber int
	fixes    []int
}

func selectionLabel(s *manifest.Selection) string {
	if s == nil {
		return unknownValue
	}
	p := &metrics.Profile{Model: s.Model, Effort: s.Effort}
	switch {
	case s.NoVariant:
		empty := ""
		p.Variant = &empty
	case s.Variant != "":
		p.Variant = &s.Variant
	}
	return formatProfile(p)
}

func orUnknown(s string) string {
	if s == "" {
		return unknownValue
	}
	return s
}

// buildMetricsReport rolls docs up without touching storage: a pure function
// over already-loaded documents.
func buildMetricsReport(docs []metrics.Document) (metricsReport, error) {
	r := metricsReport{runs: len(docs)}
	routes := map[routeKey]*routeGroup{}
	usage := map[reportUsageKey]*reportUsageGroup{}
	byDimension := make([]map[string]*outcomes, len(reportDimensions))
	for i := range byDimension {
		byDimension[i] = map[string]*outcomes{}
	}

	type issueRef struct {
		run    string
		number int
		facts  *issueFacts
	}
	mergedByPR := map[int]issueRef{}
	var fixRefs []escapedDefect // pr, fixedByRun and fixedByIssue only

	for _, doc := range docs {
		issues := map[int]*issueFacts{}
		for _, ev := range doc.Events {
			if ev.IssueNumber == 0 {
				continue
			}
			f := issues[ev.IssueNumber]
			if f == nil {
				f = &issueFacts{role: unknownValue, executor: unknownValue, reviewer: unknownValue,
					downgraded: unknownValue, approval: unknownValue, mergeSource: unknownValue,
					host: orUnknown(doc.Host), riskDomain: unknownValue, difficult: unknownValue, mechanical: unknownValue,
					lowRisk: unknownValue, fullySpecified: unknownValue, unsurprising: unknownValue,
					reviewBlocks: map[string]int{}}
				issues[ev.IssueNumber] = f
			}
			switch ev.Verb {
			case "activate", "dispatch":
				if ev.Role != "" {
					f.role = ev.Role
				}
				if ev.Executor != nil {
					f.executor = selectionLabel(ev.Executor)
				}
				if ev.Reviewer != nil {
					f.reviewer = selectionLabel(ev.Reviewer)
					f.downgraded = "no"
					if ev.ReviewerDowngraded {
						f.downgraded = "yes"
					}
				}
				if ev.Verb == "activate" {
					f.approval = orUnknown(ev.ApprovalSource)
					f.fixes = ev.Fixes
					if rf := ev.RoutingFacts; rf != nil {
						f.riskDomain = yesNo(len(rf.RiskDomains) > 0)
						f.difficult = yesNo(rf.UnusuallyDifficult)
						f.mechanical = yesNo(rf.Mechanical)
						f.lowRisk = yesNo(rf.LowRisk)
						f.fullySpecified = yesNo(rf.FullySpecified)
						f.unsurprising = yesNo(rf.Unsurprising)
					}
				}
			case "escalate":
				f.escalations++
				// A reroute replaces the route but records no downgrade flag.
				if ev.Executor != nil || ev.Reviewer != nil {
					f.executor, f.reviewer = selectionLabel(ev.Executor), selectionLabel(ev.Reviewer)
					f.downgraded = unknownValue
				}
			case "merge":
				f.merged = true
				f.mergeSource = orUnknown(ev.ApprovalSource)
				if ev.PRNumber > 0 {
					f.prNumber = ev.PRNumber
				}
			case "abandon":
				f.abandoned = true
			case "block":
				f.blocked = true
			case "review":
				if ev.Verdict != "" {
					f.cycles++
					if f.firstVerdict == "" {
						f.firstVerdict = ev.Verdict
					}
					switch {
					case ev.ReviewBlockCause != "":
						f.reviewBlocks[ev.ReviewBlockCause]++
					case ev.Verdict != "request-changes":
					case doc.Host == "": // the run predates the cause
						f.causeUnrecorded++
					default:
						f.fixCycles++
					}
				}
			}
		}

		merged := 0
		for number, f := range issues {
			if f.merged {
				merged++
			}
			if f.prNumber > 0 {
				if _, dup := mergedByPR[f.prNumber]; !dup {
					mergedByPR[f.prNumber] = issueRef{doc.RunID, number, f}
				}
			}
			for _, pr := range f.fixes {
				fixRefs = append(fixRefs, escapedDefect{pr: pr, fixedByRun: doc.RunID, fixedByIssue: number})
			}
			key := routeKey{f.role, f.executor, f.reviewer, f.downgraded, f.approval}
			g := routes[key]
			if g == nil {
				g = &routeGroup{key: key}
				routes[key] = g
			}
			g.add(f)
			for i, d := range reportDimensions {
				v := d.value(f)
				if byDimension[i][v] == nil {
					byDimension[i][v] = &outcomes{}
				}
				byDimension[i][v].add(f)
			}
		}

		touched := map[reportUsageKey]bool{}
		hasArchitect := false
		add := func(key reportUsageKey, role string, c metrics.Counters) error {
			g := usage[key]
			if g == nil {
				g = &reportUsageGroup{key: key}
				usage[key] = g
			}
			if !touched[key] {
				touched[key] = true
				g.runs++
				g.mergedRuns += merged
			}
			g.samples++
			for n, value := range counterValues(c) {
				if value == nil {
					continue
				}
				if *value > math.MaxInt64-g.totals[n] {
					return fmt.Errorf("compatible usage total overflow for %s/%s/%s", key.host, key.source, key.stream)
				}
				g.totals[n] += *value
				g.measured[n]++
				if role == "architect" {
					g.archTotals[n] += *value // within the group total, so cannot overflow
					g.archSeen[n]++
					hasArchitect = true
				}
			}
			return nil
		}
		for _, ev := range doc.Events {
			if ev.Usage == nil {
				continue
			}
			key := reportUsageKey{lifecycle: true}
			if err := add(key, eventRole(ev), ev.Usage.Counters()); err != nil {
				return r, fmt.Errorf("report metrics for %s: %w", doc.RunID, err)
			}
		}
		contributions, err := metrics.CounterContributions(doc.Observations)
		if err != nil {
			return r, fmt.Errorf("report metrics for %s: %w", doc.RunID, err)
		}
		for i, o := range doc.Observations {
			if o.Sample == nil {
				continue
			}
			key := reportUsageKey{usageGroupKey: usageGroupKey{host: o.Host, source: o.Source, stream: o.Sample.Stream}}
			if err := add(key, o.Role, contributions[i]); err != nil {
				return r, fmt.Errorf("report metrics for %s: %w", doc.RunID, err)
			}
		}
		if !hasArchitect {
			r.runsWithoutArchUse++
		}

		e := runElapsed{runID: doc.RunID}
		if len(doc.Events) > 0 {
			first, ferr := time.Parse(time.RFC3339Nano, doc.Events[0].At)
			last, lerr := time.Parse(time.RFC3339Nano, doc.Events[len(doc.Events)-1].At)
			if ferr == nil && lerr == nil && !last.Before(first) {
				e.elapsed, e.measured = last.Sub(first), true
			}
		}
		r.elapsed = append(r.elapsed, e)
	}

	// A PR counts once however many issues declare they fix it; the
	// first declaration in run and issue order names the fixer.
	sort.SliceStable(fixRefs, func(i, j int) bool {
		a, b := fixRefs[i], fixRefs[j]
		if a.fixedByRun != b.fixedByRun {
			return a.fixedByRun < b.fixedByRun
		}
		return a.fixedByIssue < b.fixedByIssue
	})
	seenPR := map[int]bool{}
	for _, fx := range fixRefs {
		if seenPR[fx.pr] {
			continue
		}
		seenPR[fx.pr] = true
		origin, ok := mergedByPR[fx.pr]
		if !ok {
			r.unattributed = append(r.unattributed, fx.pr)
			continue
		}
		f := origin.facts
		fx.originRun, fx.originIssue, fx.origin = origin.run, origin.number, f
		r.escapes = append(r.escapes, fx)
		routes[routeKey{f.role, f.executor, f.reviewer, f.downgraded, f.approval}].escapedDefects++
		for i, d := range reportDimensions {
			byDimension[i][d.value(f)].escapedDefects++
		}
	}
	sort.Ints(r.unattributed)
	sort.Slice(r.escapes, func(i, j int) bool { return r.escapes[i].pr < r.escapes[j].pr })

	for _, g := range routes {
		r.routes = append(r.routes, *g)
	}
	sort.Slice(r.routes, func(i, j int) bool {
		a, b := r.routes[i].key, r.routes[j].key
		return fmt.Sprint(a) < fmt.Sprint(b)
	})
	for i, d := range reportDimensions {
		dr := dimensionReport{title: d.title}
		for v, o := range byDimension[i] {
			dr.groups = append(dr.groups, dimensionGroup{value: v, outcomes: *o})
		}
		sort.Slice(dr.groups, func(a, b int) bool { return dr.groups[a].value < dr.groups[b].value })
		r.dimensions = append(r.dimensions, dr)
	}
	for _, g := range usage {
		r.usage = append(r.usage, *g)
	}
	sort.Slice(r.usage, func(i, j int) bool {
		a, b := r.usage[i].key, r.usage[j].key
		if a.lifecycle != b.lifecycle {
			return !a.lifecycle
		}
		return fmt.Sprint(a) < fmt.Sprint(b)
	})
	return r, nil
}

func printMetricsReport(w io.Writer, r metricsReport) {
	fmt.Fprintf(w, "runs recorded: %d\n", r.runs)
	fmt.Fprintln(w, "\nissue outcomes by role, executor, reviewer, reviewer downgraded and approval at activation:")
	for _, g := range r.routes {
		k := g.key
		fmt.Fprintf(w, "  role %s; executor [%s]; reviewer [%s]; reviewer downgraded %s; approval at activation %s\n",
			k.role, k.executor, k.reviewer, k.downgraded, k.approval)
		g.print(w)
	}
	for _, d := range r.dimensions {
		fmt.Fprintf(w, "\nissue outcomes by %s:\n", d.title)
		for _, g := range d.groups {
			fmt.Fprintf(w, "  %s %s\n", d.title, g.value)
			g.print(w)
		}
	}

	fmt.Fprintln(w, "\nescaped defects (merged PRs a later plan issue declares it fixes):")
	for _, e := range r.escapes {
		f := e.origin
		verdict, cycles := orUnknown(f.firstVerdict), unknownValue
		if f.firstVerdict != "" {
			cycles = fmt.Sprint(f.cycles)
		}
		fmt.Fprintf(w, "  PR #%d, fixed by run %s issue #%d; original run %s issue #%d; role %s; executor [%s]; reviewer [%s]; reviewer downgraded %s; first review verdict %s; review cycles %s; approval at activation %s; approval at merge %s\n",
			e.pr, e.fixedByRun, e.fixedByIssue, e.originRun, e.originIssue, f.role, f.executor, f.reviewer, f.downgraded, verdict, cycles, f.approval, f.mergeSource)
	}
	if len(r.escapes)+len(r.unattributed) == 0 {
		fmt.Fprintln(w, "  none declared")
	} else {
		fmt.Fprintf(w, "  unattributed (no recorded merge event names the PR): %d %v\n", len(r.unattributed), r.unattributed)
	}

	fmt.Fprintln(w, "\ntoken counters by host, source and stream (no total across groups):")
	for _, g := range r.usage {
		if g.key.lifecycle {
			fmt.Fprintf(w, "  lifecycle-verb usage (host, source and stream not recorded); %d runs; %d samples; %d merged issues\n", g.runs, g.samples, g.mergedRuns)
		} else {
			fmt.Fprintf(w, "  host %s; source %s; stream %s; %d runs; %d samples; %d merged issues\n",
				orUnknown(g.key.host), g.key.source, g.key.stream, g.runs, g.samples, g.mergedRuns)
		}
		for i, label := range counterLabels {
			if g.measured[i] == 0 {
				fmt.Fprintf(w, "    %s: unknown (0/%d measured)\n", label, g.samples)
				continue
			}
			perMerged := "per merged issue unknown (no merged issues)"
			if g.mergedRuns > 0 {
				perMerged = fmt.Sprintf("%.1f per merged issue", float64(g.totals[i])/float64(g.mergedRuns))
			}
			line := fmt.Sprintf("    %s: %d (%d/%d measured); %s", label, g.totals[i], g.measured[i], g.samples, perMerged)
			if g.archSeen[i] > 0 {
				share := "n/a"
				if g.totals[i] > 0 {
					share = fmt.Sprintf("%.1f%%", 100*float64(g.archTotals[i])/float64(g.totals[i]))
				}
				line += fmt.Sprintf("; architect share %s (%d of %d)", share, g.archTotals[i], g.totals[i])
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(r.usage) == 0 {
		fmt.Fprintln(w, "  none recorded; counters unknown")
	}
	if r.runsWithoutArchUse > 0 {
		fmt.Fprintf(w, "architect usage unavailable: no architect-role counters in %d of %d runs\n", r.runsWithoutArchUse, r.runs)
	} else {
		fmt.Fprintf(w, "architect usage: architect-role counters recorded in all %d runs\n", r.runs)
	}

	fmt.Fprintln(w, "\nrun elapsed time (wall-clock from first to last recorded event, not active time):")
	var total time.Duration
	measured := 0
	for _, e := range r.elapsed {
		if !e.measured {
			fmt.Fprintf(w, "  %s: unknown (no parseable first and last event time)\n", e.runID)
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", e.runID, e.elapsed)
		total += e.elapsed
		measured++
	}
	if measured == 0 {
		fmt.Fprintf(w, "  sum of wall-clock times: unknown (0 of %d runs measured)\n", r.runs)
	} else {
		fmt.Fprintf(w, "  sum of wall-clock times: %s (%d of %d runs measured)\n", total, measured, r.runs)
	}
}
