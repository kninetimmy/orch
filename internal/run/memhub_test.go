package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestExecProberProbe(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo",
	}}}
	p := execProber{runner: script, dir: "/repo"}
	if err := p.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	script.AssertExhausted()
}

func TestExecProberNonZeroExit(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1, Stderr: "unreachable",
	}}}
	p := execProber{runner: script, dir: "/repo"}
	if err := p.Probe(context.Background()); err == nil {
		t.Fatal("Probe succeeded, want error")
	}
	script.AssertExhausted()
}

func TestMemhubGateRequiredHealthy(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{Name: "memhub", Args: []string{"status"}, Dir: "/repo"}}}
	rep, err := memhubGate(context.Background(), "required", execProber{runner: script, dir: "/repo"})
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "healthy" || rep.Mode != "required" {
		t.Errorf("report = %+v", rep)
	}
	script.AssertExhausted()
}

func TestMemhubGateRequiredExitOneFailsClosed(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1,
	}}}
	_, err := memhubGate(context.Background(), "required", execProber{runner: script, dir: "/repo"})
	if !errors.Is(err, ErrMemhubRequired) {
		t.Fatalf("err = %v, want ErrMemhubRequired", err)
	}
	script.AssertExhausted()
}

func TestMemhubGateRequiredSpawnErrorFailsClosed(t *testing.T) {
	sentinel := errors.New("exec: \"memhub\": executable file not found in $PATH")
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Err: sentinel,
	}}}
	_, err := memhubGate(context.Background(), "required", execProber{runner: script, dir: "/repo"})
	if !errors.Is(err, ErrMemhubRequired) {
		t.Fatalf("err = %v, want ErrMemhubRequired", err)
	}
	if !strings.Contains(err.Error(), sentinel.Error()) {
		t.Errorf("err = %v, want to mention the spawn error", err)
	}
	script.AssertExhausted()
}

func TestMemhubGateBestEffortRecordsNotFatal(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1, Stderr: "down",
	}}}
	rep, err := memhubGate(context.Background(), "best-effort", execProber{runner: script, dir: "/repo"})
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "unhealthy" || rep.Detail == "" {
		t.Errorf("report = %+v, want unhealthy with detail", rep)
	}
	script.AssertExhausted()
}

func TestMemhubGateOffSkipsProbe(t *testing.T) {
	script := &execxtest.Script{T: t} // no calls scripted
	rep, err := memhubGate(context.Background(), "off", execProber{runner: script, dir: "/repo"})
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "skipped" {
		t.Errorf("report = %+v, want skipped", rep)
	}
	script.AssertExhausted() // proves no call was made
}

// fakeProber lets memhubGate tests avoid execx entirely where useful.
type fakeProber struct{ err error }

func (f fakeProber) Probe(context.Context) error { return f.err }

var _ Prober = fakeProber{}
var _ execx.Runner = (*execxtest.Script)(nil)
