package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

// Prober checks memhub reachability. execProber is the production
// implementation; tests inject a fake so they never spawn a real
// memhub process. The interface is shaped so a future internal/memhub
// package can absorb it without changing this package's call sites.
type Prober interface {
	Probe(ctx context.Context) error
}

// execProber runs `memhub status` via the injected Runner with the
// primary checkout as its explicit working directory (PRD §20).
type execProber struct {
	runner execx.Runner
	dir    string
}

func (p execProber) Probe(ctx context.Context) error {
	res, err := p.runner.Run(ctx, execx.Cmd{Name: "memhub", Args: []string{"status"}, Dir: p.dir})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("memhub status exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
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
