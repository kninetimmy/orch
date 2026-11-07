package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
)

func TestPluginHealthRequiresActiveServerAndVerifiedLocation(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active := `{"id":"orch.delivery","features":{"server":true},"state":{"status":"active"}}`
	response := func(directory, entries string) string {
		return fmt.Sprintf(`{"location":{"directory":%q},"data":[%s]}`, directory, entries)
	}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"active server", response(root, active), ""},
		{"failed with ID", response(root, `{"id":"orch.delivery","features":{"server":true},"state":{"status":"failed","error":"credential-token"}}`), "failed to load"},
		{"missing state", response(root, `{"id":"orch.delivery","features":{"server":true}}`), "state.status"},
		{"missing server", response(root, `{"id":"orch.delivery","state":{"status":"active"}}`), "server component"},
		{"server disabled", response(root, `{"id":"orch.delivery","features":{"server":false},"state":{"status":"active"}}`), "server component"},
		{"absent", response(root, `{"id":"other"}`), "is absent"},
		{"duplicate", response(root, active+","+active), "appears 2 times"},
		{"starting", response(root, `{"id":"orch.delivery","state":{"status":"starting"}}`), "not ready"},
		{"empty before startup", response(root, ""), "not ready or is empty"},
		{"malformed data", `{"data":{}}`, "malformed"},
		{"missing data", `{"location":{}}`, "data array"},
		{"missing location", `{"data":[]}`, "location.directory is missing"},
		{"wrong directory", response(t.TempDir(), active), "different directory"},
		{"unverifiable directory", response(filepath.Join(root, "missing"), active), "cannot be verified"},
		{"invalid state shape", response(root, `{"id":"orch.delivery","state":"credential-token"}`), "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := catalogRunner(func(cmd execx.Cmd) (execx.Result, error) {
				wantEndpoint := "/api/plugin?" + url.Values{"location[directory]": {root}}.Encode()
				if cmd.Name != "opencode" || len(cmd.Args) != 3 || cmd.Args[2] != wantEndpoint || cmd.Dir != root {
					t.Fatalf("unexpected scoped plugin request: %+v", cmd)
				}
				return execx.Result{Stdout: tc.body}, nil
			})
			err := (Runtime{Executable: "opencode"}).CheckPlugin(context.Background(), runner, root, "orch.delivery")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "credential-token") {
				t.Fatalf("error = %v, want safe %q", err, tc.want)
			}
		})
	}
}

func TestPluginCommandFailuresDoNotDiscloseOutput(t *testing.T) {
	for _, tc := range []struct {
		result execx.Result
		err    error
	}{
		{err: errors.New("credential-token")},
		{result: execx.Result{Stdout: "credential-token", Stderr: "credential-token", ExitCode: 7}},
	} {
		runner := catalogRunner(func(execx.Cmd) (execx.Result, error) { return tc.result, tc.err })
		err := (Runtime{Executable: Executable}).CheckPlugin(context.Background(), runner, t.TempDir(), "orch.delivery")
		if err == nil || strings.Contains(err.Error(), "credential-token") || !strings.Contains(err.Error(), "retry") {
			t.Fatalf("expected safe actionable error, got %v", err)
		}
	}
}

func TestResolvedCommandIsUsedForEveryCatalog(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"location[directory]": {root}}.Encode()
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{
		{Name: "opencode2", Args: []string{"--version"}, Err: errors.New("not found")},
		{Name: "opencode", Args: []string{"--version"}, Stdout: "opencode v2.10.0"},
		{Name: "opencode", Args: []string{"api", "get", "/api/plugin?" + query}, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[{"id":"orch.delivery","features":{"server":true},"state":{"status":"active"}}]}`, root)},
		{Name: "opencode", Args: []string{"api", "get", "/api/skill?" + query}, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, root)},
		{Name: "opencode", Args: []string{"api", "get", "/api/model?" + query}, Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, root)},
	}}
	for i := range script.Calls {
		script.Calls[i].Dir = root
	}
	runtime, err := Resolve(context.Background(), script, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CheckPlugin(context.Background(), script, root, "orch.delivery"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReadSkillCatalog(context.Background(), script, root); err != nil {
		t.Fatal(err)
	}
	catalog, err := runtime.ReadCatalog(context.Background(), script, root)
	if err != nil || catalog.Warning == "" {
		t.Fatalf("catalog=%+v error=%v, want successful unverified runtime advisory", catalog, err)
	}
	script.AssertExhausted()
}
