package run

import (
	"context"
	"fmt"
)

// Prober checks memhub reachability. internal/memhub's Client is the
// production implementation; tests inject a fake so they never spawn
// a real memhub process.
type Prober interface {
	Probe(ctx context.Context) error
}

// MemhubReport records a plan/gate document's memhub probe outcome.
type MemhubReport struct {
	Mode   string `json:"mode"`
	Probe  string `json:"probe"` // "healthy" | "unhealthy" | "skipped"
	Detail string `json:"detail,omitempty"`
}

// memhubGate applies config.Memhub.Mode's policy to a probe (PRD §20):
// "off" never calls the prober; "required" fails closed with
// ErrMemhubRequired on any probe failure (exit non-zero or a spawn
// error, both mean the same thing here — memhub cannot be reached);
// "best-effort" records the failure without stopping the caller.
func memhubGate(ctx context.Context, mode string, p Prober) (MemhubReport, error) {
	if mode == "off" {
		return MemhubReport{Mode: mode, Probe: "skipped"}, nil
	}
	err := p.Probe(ctx)
	if err == nil {
		return MemhubReport{Mode: mode, Probe: "healthy"}, nil
	}
	if mode == "required" {
		return MemhubReport{}, fmt.Errorf("%w: %v", ErrMemhubRequired, err)
	}
	return MemhubReport{Mode: mode, Probe: "unhealthy", Detail: err.Error()}, nil
}
