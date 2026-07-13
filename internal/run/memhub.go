package run

import (
	"context"
	"fmt"
)

// Prober checks memhub reachability and the live recall path.
// internal/memhub's Client is the production implementation; tests
// inject a fake so they never spawn a real memhub process.
type Prober interface {
	Probe(ctx context.Context) error
	Recall(ctx context.Context) error
}

// MemhubReport records a plan/gate document's memhub probe outcome.
type MemhubReport struct {
	Mode   string `json:"mode"`
	Probe  string `json:"probe"`  // "healthy" | "unhealthy" | "skipped"
	Recall string `json:"recall"` // "healthy" | "unhealthy" | "skipped"
	Detail string `json:"detail,omitempty"`
}

// memhubGate applies config.Memhub.Mode's policy to health and recall
// probes (PRD §20): "off" never calls the prober; "required" fails
// closed with ErrMemhubRequired on any probe failure (exit non-zero or
// a spawn error, both mean the same thing here — memhub cannot be
// reached, or cannot actually serve recall); "best-effort" records the
// failure without stopping the caller. Health runs first; recall only
// runs once health has succeeded, since attempting recall against a
// memhub whose status already failed tells us nothing new.
func memhubGate(ctx context.Context, mode string, p Prober) (MemhubReport, error) {
	if mode == "off" {
		return MemhubReport{Mode: mode, Probe: "skipped", Recall: "skipped"}, nil
	}
	if err := p.Probe(ctx); err != nil {
		if mode == "required" {
			return MemhubReport{}, fmt.Errorf("%w: %v", ErrMemhubRequired, err)
		}
		return MemhubReport{Mode: mode, Probe: "unhealthy", Recall: "skipped", Detail: err.Error()}, nil
	}
	if err := p.Recall(ctx); err != nil {
		if mode == "required" {
			return MemhubReport{}, fmt.Errorf("%w: %v", ErrMemhubRequired, err)
		}
		return MemhubReport{Mode: mode, Probe: "healthy", Recall: "unhealthy", Detail: err.Error()}, nil
	}
	return MemhubReport{Mode: mode, Probe: "healthy", Recall: "healthy"}, nil
}
