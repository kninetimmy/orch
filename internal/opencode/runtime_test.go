package opencode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestRuntimeVersionFloorsAndAdvisories(t *testing.T) {
	for _, tc := range []struct {
		version string
		problem string
		warning bool
	}{
		{MinimumVersion, "", false},
		{"opencode2 v0.0.0-beta-18414", "", true},
		{"opencode v0.0.0-beta-18314", "", false},
		{"opencode v2.0.0", "", true},
		{"opencode v2.2.6", "", false},
		{"opencode2 v2.2.6", "", false},
		{"opencode v2.10.0", "", true},
		{"opencode v3.0.0", "", true},
		{"opencode2 v0.0.0-beta-18313", "too old", false},
		{"opencode v1.18.34", "too old", false},
		{"opencode v0.9.99", "too old", false},
		{"opencode v2.2", "malformed", false},
		{"opencode v2.02.6", "malformed", false},
		{"opencode v2.-1.0", "malformed", false},
		{"opencode v2.2.6 extra", "malformed", false},
		{"opencode v2.2.6\ncredential-token", "malformed", false},
		{"opencode v18446744073709551616.0.0", "malformed", false},
		{"opencode v2.18446744073709551616.0", "malformed", false},
		{"opencode v2.0.18446744073709551616", "malformed", false},
		{"opencode2 v0.0.0-beta-18446744073709551616", "malformed", false},
		{"opencode2 v0.0.0-beta-newer", "malformed", false},
		{"some-other-program v2.2.6", "malformed", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			warning, err := checkRuntimeVersion(tc.version)
			if tc.problem == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.problem) {
				t.Fatalf("error = %v, want %q", err, tc.problem)
			}
			if (warning != "") != tc.warning {
				t.Fatalf("warning = %q, want present=%v", warning, tc.warning)
			}
			if err != nil && strings.Contains(err.Error(), "credential-token") {
				t.Fatalf("version diagnostic leaked raw output: %v", err)
			}
		})
	}
}

func TestResolveSelectsV2Executable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls []execxtest.Call
		want  string
	}{
		{"prefer V2 opencode2 over legacy opencode", []execxtest.Call{
			{Name: "opencode2", Args: []string{"--version"}, Stdout: "opencode v2.2.6"},
		}, "opencode2"},
		{"only opencode provides V2", []execxtest.Call{
			{Name: "opencode2", Args: []string{"--version"}, Err: errors.New("not found: credential-token")},
			{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v2.10.0"},
		}, "opencode"},
		{"legacy opencode2 does not hide V2", []execxtest.Call{
			{Name: "opencode2", Args: []string{"--version"}, Stdout: "opencode v1.18.34"},
			{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v2.2.6"},
		}, "opencode"},
		{"V1 and malformed output fail safely", []execxtest.Call{
			{Name: "opencode2", Args: []string{"--version"}, Stdout: "credential-token"},
			{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v1.18.34"},
		}, ""},
		{"spawn and exit errors fail safely", []execxtest.Call{
			{Name: "opencode2", Args: []string{"--version"}, Err: errors.New("credential-token")},
			{Name: "opencode", Args: []string{"--version"}, Exit: 7, Stderr: "credential-token"},
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for i := range tc.calls {
				tc.calls[i].Dir = root
			}
			script := &execxtest.Script{T: t, Calls: tc.calls}
			runtime, err := Resolve(context.Background(), script, root)
			script.AssertExhausted()
			if tc.want == "" {
				if err == nil || strings.Contains(err.Error(), "credential-token") {
					t.Fatalf("expected safe failure, got %v", err)
				}
			} else if err != nil || runtime.Executable != tc.want {
				t.Fatalf("runtime=%+v error=%v, want %q", runtime, err, tc.want)
			}
		})
	}
}
