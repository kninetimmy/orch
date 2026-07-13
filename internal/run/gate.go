package run

import (
	"context"
	"fmt"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/state"
)

// GateSchemaVersion is the gate-document schema this build emits.
const GateSchemaVersion = 1

// GateDoc is the human decision gate for a plan (PRD §8, covering
// every §8 bullet): the adapter renders it natively and owns the four
// §8 choices (approve and enter Delivery, adjust agent routing, revise
// scope, or cancel and remain read-only).
type GateDoc struct {
	SchemaVersion   int          `json:"schema_version"`
	PlanDigest      string       `json:"plan_digest"`
	PlanTitle       string       `json:"plan_title"`
	Host            string       `json:"host"`
	ConfigRevision  string       `json:"config_revision"`
	ConfigOverrides []string     `json:"config_overrides,omitempty"`
	MergeStrategy   string       `json:"merge_strategy"`
	Memhub          MemhubReport `json:"memhub"`
	CI              CIReport     `json:"ci"`
	Issues          []GateIssue  `json:"issues"`
}

// GateIssue is one plan issue's gate view: what it will do plus the
// routing the engine derived for it. Executor and Reviewer reuse
// manifest.Selection — the same exact model/effort pairing the audit
// record carries.
type GateIssue struct {
	ID                 string             `json:"id"`
	Title              string             `json:"title"`
	Objective          string             `json:"objective"`
	AcceptanceCriteria []string           `json:"acceptance_criteria"`
	Role               manifest.Role      `json:"role"`
	Executor           manifest.Selection `json:"executor"`
	Reviewer           manifest.Selection `json:"reviewer"`
	ReviewerDowngraded bool               `json:"reviewer_downgraded"`
	RoutingRationale   string             `json:"routing_rationale"`
	DependsOn          []string           `json:"depends_on,omitempty"`
	Wave               int                `json:"wave"`
	RequiredTests      []string           `json:"required_tests"`
	Risk               string             `json:"risk"`
	UsageClass         string             `json:"usage_class"`
	Labels             []string           `json:"labels"`
}

// Plan validates a plan document and reports the human decision gate
// for it. Every step is read-only except the memhub probe (PRD §20).
func Plan(ctx context.Context, env Env, planJSON []byte) (*GateDoc, error) {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if err := requireAssistNoLock(env.RepoRoot); err != nil {
		return nil, err
	}

	plan, err := DecodePlan(planJSON)
	if err != nil {
		return nil, err
	}
	if err := plan.Validate(cfg); err != nil {
		return nil, err
	}
	digest, err := plan.Digest()
	if err != nil {
		return nil, err
	}

	profile, err := hostProfile(cfg, plan.Host)
	if err != nil {
		return nil, err
	}
	denylist := modelDenylist(cfg)

	ci, err := detectCI(env.RepoRoot)
	if err != nil {
		return nil, err
	}

	mh, err := memhubGate(ctx, cfg.Memhub.Mode, memhub.New(env.Runner, env.RepoRoot))
	if err != nil {
		return nil, err
	}

	issues := make([]GateIssue, len(plan.Issues))
	for idx, i := range plan.Issues {
		d, err := decideIssue(profile, i)
		if err != nil {
			return nil, err
		}
		labels := issueLabels(i, d)
		if err := labels.Validate(denylist...); err != nil {
			return nil, err
		}
		issues[idx] = GateIssue{
			ID:                 i.ID,
			Title:              i.Title,
			Objective:          i.Objective,
			AcceptanceCriteria: i.AcceptanceCriteria,
			Role:               d.Role,
			Executor:           d.Executor,
			Reviewer:           d.Reviewer,
			ReviewerDowngraded: d.ReviewerDowngraded,
			RoutingRationale:   d.Rationale,
			DependsOn:          i.DependsOn,
			Wave:               i.Wave,
			RequiredTests:      i.RequiredTests,
			Risk:               string(deriveRisk(i)),
			UsageClass:         i.UsageClass,
			Labels:             flattenLabels(labels),
		}
	}

	return &GateDoc{
		SchemaVersion:   GateSchemaVersion,
		PlanDigest:      digest,
		PlanTitle:       plan.Title,
		Host:            plan.Host,
		ConfigRevision:  cfg.ConfigRevision,
		ConfigOverrides: cfg.Overrides,
		MergeStrategy:   cfg.Merge.Strategy,
		Memhub:          mh,
		CI:              ci,
		Issues:          issues,
	}, nil
}

// requireAssistNoLock enforces the precondition Plan and Activate
// share: the repository must be in Assist with no Delivery lock held.
// An existing run must be resolved (`orch abort`, or a future
// `orch resume`) before a new plan is gated or activated.
func requireAssistNoLock(repoRoot string) error {
	st, err := state.Load(repoRoot)
	if err != nil {
		return err
	}
	owner, err := lockfile.Inspect(repoRoot)
	if err != nil {
		return err
	}
	if err := state.CheckConsistent(st, owner); err != nil {
		return err
	}
	if st.Mode != state.ModeAssist {
		return fmt.Errorf("%w: run %s is active (host %s, started %s); run `orch abort` first", ErrDeliveryActive, st.Run.ID, st.Run.Host, st.Run.StartedAt.Format(time.RFC3339))
	}
	return nil
}
