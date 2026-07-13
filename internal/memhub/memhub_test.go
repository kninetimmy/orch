package memhub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestClientProbeHealthy(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo",
	}}}
	c := New(script, "/repo")
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	script.AssertExhausted()
}

func TestClientProbeNonZeroExit(t *testing.T) {
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Exit: 1, Stderr: "unreachable",
	}}}
	c := New(script, "/repo")
	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe succeeded, want error")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("err = %v, want it to mention memhub's stderr", err)
	}
	script.AssertExhausted()
}

func TestClientProbeSpawnErrorPassthrough(t *testing.T) {
	sentinel := errors.New("exec: \"memhub\": executable file not found in $PATH")
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "memhub", Args: []string{"status"}, Dir: "/repo", Err: sentinel,
	}}}
	c := New(script, "/repo")
	err := c.Probe(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel returned unwrapped", err)
	}
	script.AssertExhausted()
}
