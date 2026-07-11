// Package execxtest provides a scripted fake execx.Runner for
// transcript tests: each expected call is asserted in order and
// answered with a recorded result, so tests pin the exact argument
// vectors the core sends to git and gh without running either.
package execxtest

import (
	"context"
	"slices"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

// Call is one expected invocation and its scripted response.
type Call struct {
	// Name is the expected Cmd.Name, for example "git".
	Name string
	// Args is the expected exact argument vector.
	Args []string
	// Dir is the expected Cmd.Dir; empty skips the check because
	// many tests build paths from t.TempDir().
	Dir string
	// Env, when non-nil, is the expected exact Cmd.Env.
	Env []string

	// Stdout, Stderr, and Exit form the scripted Result.
	Stdout string
	Stderr string
	Exit   int
	// Err, when non-nil, is returned instead of a Result, modeling
	// a command that could not run at all.
	Err error
}

// Script is a Runner that fails the test on any call that deviates
// from the recorded transcript.
type Script struct {
	T     *testing.T
	Calls []Call

	next int
}

// Run asserts that c matches the next scripted call and returns its
// recorded response.
func (s *Script) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	s.T.Helper()
	if s.next >= len(s.Calls) {
		s.T.Fatalf("unexpected call %d: %s %v (script has %d calls)", s.next+1, c.Name, c.Args, len(s.Calls))
	}
	want := s.Calls[s.next]
	s.next++
	if c.Name != want.Name {
		s.T.Fatalf("call %d: got command %q, want %q", s.next, c.Name, want.Name)
	}
	if !slices.Equal(c.Args, want.Args) {
		s.T.Fatalf("call %d: %s args\ngot  %q\nwant %q", s.next, c.Name, c.Args, want.Args)
	}
	if want.Dir != "" && c.Dir != want.Dir {
		s.T.Fatalf("call %d: %s dir\ngot  %q\nwant %q", s.next, c.Name, c.Dir, want.Dir)
	}
	if want.Env != nil && !slices.Equal(c.Env, want.Env) {
		s.T.Fatalf("call %d: %s env\ngot  %q\nwant %q", s.next, c.Name, c.Env, want.Env)
	}
	if want.Err != nil {
		return execx.Result{}, want.Err
	}
	return execx.Result{Stdout: want.Stdout, Stderr: want.Stderr, ExitCode: want.Exit}, nil
}

// AssertExhausted fails the test unless every scripted call was
// consumed. Tests use it to prove fail-closed short circuits: an
// operation that must be denied before touching git leaves an empty
// script untouched.
func (s *Script) AssertExhausted() {
	s.T.Helper()
	if s.next != len(s.Calls) {
		s.T.Fatalf("script not exhausted: %d of %d calls made", s.next, len(s.Calls))
	}
}
