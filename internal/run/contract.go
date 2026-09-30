package run

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/routing"
	"github.com/kninetimmy/orch/internal/state"
)

// ContractVersion versions the meaning of approval independently of the
// submitted PlanDoc. Bump it when execution policy changes incompatibly.
const ContractVersion = 1

// ExecutionConfig is the effective configuration approved for this run.
// All six profiles are bound, including routes available to later escalation.
// Only the selected host participates; other hosts cannot execute this run.
type ExecutionConfig struct {
	Version              int             `json:"version"`
	Host                 string          `json:"host"`
	ConfigRevision       string          `json:"config_revision"`
	Profiles             routing.Profile `json:"profiles"`
	MaxSubagents         int             `json:"max_subagents"`
	MergeStrategy        string          `json:"merge_strategy"`
	MemhubMode           string          `json:"memhub_mode"`
	MetricsEnabled       bool            `json:"metrics_enabled"`
	BlastRadiusCriterion string          `json:"blast_radius_criterion"`
}

// Digest returns the canonical fingerprint persisted in the run's PlanRef.
func (c ExecutionConfig) Digest() (string, error) { return contentDigest(c) }

func executionConfig(cfg *config.Config, host string) (ExecutionConfig, error) {
	profile, err := hostProfile(cfg, host)
	if err != nil {
		return ExecutionConfig{}, err
	}
	return ExecutionConfig{
		Version: ContractVersion, Host: host, ConfigRevision: cfg.ConfigRevision,
		Profiles: profile, MaxSubagents: cfg.Concurrency.MaxSubagents,
		MergeStrategy: cfg.Merge.Strategy, MemhubMode: cfg.Memhub.Mode,
		MetricsEnabled: cfg.Metrics.Enabled, BlastRadiusCriterion: BlastRadiusCriterion,
	}, nil
}

// approvalContract binds submitted scope and the engine's effective result.
// Probe results and override provenance are observations, not execution policy.
type approvalContract struct {
	Plan      *PlanDoc        `json:"plan"`
	Execution ExecutionConfig `json:"execution"`
	Issues    []GateIssue     `json:"issues"`
}

func effectiveContract(plan *PlanDoc, cfg *config.Config) (*approvalContract, error) {
	execution, err := executionConfig(cfg, plan.Host)
	if err != nil {
		return nil, err
	}
	issues, err := gateIssues(plan, cfg, execution.Profiles)
	if err != nil {
		return nil, err
	}
	return &approvalContract{Plan: plan, Execution: execution, Issues: issues}, nil
}

// checkExecutionConfig is shared by every lifecycle verb and resume. Legacy
// records remain inspectable, but are never upgraded into a new authorization.
func checkExecutionConfig(cfg *config.Config, st *state.State) error {
	run := st.Run
	if st.SchemaVersion != state.SchemaVersion || run.Plan.ContractVersion != ContractVersion || run.Plan.ExecutionDigest == "" {
		return fmt.Errorf("%w: state schema %d / approval contract %d is incompatible with schema %d / contract %d; preserve this run and finish with its original engine, or run `orch abort` and re-plan for fresh approval", ErrBadApproval, st.SchemaVersion, run.Plan.ContractVersion, state.SchemaVersion, ContractVersion)
	}
	execution, err := executionConfig(cfg, run.Host)
	if err != nil {
		return err
	}
	digest, err := execution.Digest()
	if err != nil {
		return err
	}
	if digest != run.Plan.ExecutionDigest {
		return fmt.Errorf("%w: effective execution configuration no longer matches the approved run; restore its approved configuration, or run `orch abort` and re-plan for fresh approval", ErrConfigDrift)
	}
	return nil
}
