package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/memhub"
)

// recallArgs is the exact argument vector Client.Recall sends, pinning
// the canary query and flags every gate test scripts against.
var recallArgs = []string{"recall", memhub.RecallProbeQuery, "--json", "--max-results", "1"}

func TestMemhubGateRequiredHealthy(t *testing.T) {
	// Scripted in order: health first, then recall — pins the ordering.
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "memhub", Args: []string{"status"}, Dir: "/repo"},
		{Name: "memhub", Args: recallArgs, Dir: "/repo", Stdout: `{"results":[]}`},
	}}
	rep, err := memhubGate(context.Background(), "required", memhub.New(script, "/repo"))
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "healthy" || rep.Recall != "healthy" || rep.Mode != "required" {
		t.Errorf("report = %+v", rep)
	}
	script.AssertExhausted()
}

func TestMemhubGateRequiredRecallFailureFailsClosed(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "memhub", Args: []string{"status"}, Dir: "/repo"},
		{Name: "memhub", Args: recallArgs, Dir: "/repo", Exit: 1, Stderr: "wedged"},
	}}
	_, err := memhubGate(context.Background(), "required", memhub.New(script, "/repo"))
	if !errors.Is(err, ErrMemhubRequired) {
		t.Fatalf("err = %v, want ErrMemhubRequired", err)
	}
	script.AssertExhausted()
}

func TestMemhubGateRequiredExitOneFailsClosed(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1,
	}}}
	_, err := memhubGate(context.Background(), "required", memhub.New(script, "/repo"))
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
	_, err := memhubGate(context.Background(), "required", memhub.New(script, "/repo"))
	if !errors.Is(err, ErrMemhubRequired) {
		t.Fatalf("err = %v, want ErrMemhubRequired", err)
	}
	if !strings.Contains(err.Error(), sentinel.Error()) {
		t.Errorf("err = %v, want to mention the spawn error", err)
	}
	script.AssertExhausted()
}

func TestMemhubGateBestEffortHealthFailureSkipsRecall(t *testing.T) {
	// Exactly one call scripted — pins that recall is never attempted
	// against a memhub whose status already failed.
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1, Stderr: "down",
	}}}
	rep, err := memhubGate(context.Background(), "best-effort", memhub.New(script, "/repo"))
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "unhealthy" || rep.Recall != "skipped" || rep.Detail == "" {
		t.Errorf("report = %+v, want unhealthy/skipped with detail", rep)
	}
	script.AssertExhausted()
}

func TestMemhubGateBestEffortRecallFailureRecordsNotFatal(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "memhub", Args: []string{"status"}, Dir: "/repo"},
		{Name: "memhub", Args: recallArgs, Dir: "/repo", Exit: 1, Stderr: "wedged"},
	}}
	rep, err := memhubGate(context.Background(), "best-effort", memhub.New(script, "/repo"))
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "healthy" || rep.Recall != "unhealthy" || rep.Detail == "" {
		t.Errorf("report = %+v, want healthy/unhealthy with detail", rep)
	}
	script.AssertExhausted()
}

func TestMemhubGateOffSkipsProbe(t *testing.T) {
	script := &execxtest.Script{T: t} // no calls scripted
	rep, err := memhubGate(context.Background(), "off", memhub.New(script, "/repo"))
	if err != nil {
		t.Fatalf("memhubGate: %v", err)
	}
	if rep.Probe != "skipped" || rep.Recall != "skipped" {
		t.Errorf("report = %+v, want skipped/skipped", rep)
	}
	script.AssertExhausted() // proves no call was made
}

// fakeProber lets memhubGate tests avoid execx entirely where useful.
type fakeProber struct {
	err       error
	recallErr error
}

func (f fakeProber) Probe(context.Context) error  { return f.err }
func (f fakeProber) Recall(context.Context) error { return f.recallErr }

var _ Prober = fakeProber{}
var _ execx.Runner = (*execxtest.Script)(nil)
