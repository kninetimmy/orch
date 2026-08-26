package opencode

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

type catalogRunner func(execx.Cmd) (execx.Result, error)

func (f catalogRunner) Run(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
	return f(cmd)
}

func TestReadCatalogFiltersAndRetainsOnlySafeFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo & project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoint := "/api/model?" + url.Values{"location[directory]": {root}}.Encode()
	response := fmt.Sprintf(`{
		"location":{"directory":%q},
		"data":[
			{"id":"agent","providerID":"safe-provider","enabled":true,"capabilities":{"tools":true,"input":["text","image"],"output":["text"]},"variants":[{"id":"low","settings":{"secret":"variant-secret"}},{"id":"high","headers":{"authorization":"variant-token"}}],"settings":{"apiKey":"model-secret"},"headers":{"account-id":"account-secret"}},
			{"id":"disabled","providerID":"safe-provider","enabled":false,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[]},
			{"id":"no-input","providerID":"safe-provider","enabled":true,"capabilities":{"tools":true,"input":["image"],"output":["text"]},"variants":[]},
			{"id":"no-output","providerID":"safe-provider","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["image"]},"variants":[]},
			{"id":"no-tools","providerID":"safe-provider","enabled":true,"capabilities":{"tools":false,"input":["text"],"output":["text"]},"variants":[]}
		]
	}`, root)
	runner := catalogRunner(func(cmd execx.Cmd) (execx.Result, error) {
		if cmd.Name != Executable || len(cmd.Args) != 3 || cmd.Args[0] != "api" || cmd.Args[1] != "get" || cmd.Args[2] != wantEndpoint {
			t.Fatalf("command = %s %q, want %s api get %q", cmd.Name, cmd.Args, Executable, wantEndpoint)
		}
		if cmd.Dir != root {
			t.Fatalf("command dir = %q, want %q", cmd.Dir, root)
		}
		return execx.Result{Stdout: response}, nil
	})

	catalog, err := ReadCatalog(context.Background(), runner, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].ID != "safe-provider/agent" {
		t.Fatalf("models = %+v, want only safe-provider/agent", catalog.Models)
	}
	if got := strings.Join(catalog.Models[0].Variants, ","); got != "low,high" {
		t.Fatalf("variants = %q, want low,high", got)
	}
	if got := fmt.Sprintf("%+v", catalog); strings.Contains(got, "secret") || strings.Contains(got, "account") || strings.Contains(got, "authorization") {
		t.Fatalf("catalog retained provider-sensitive fields: %s", got)
	}
}

func TestReadCatalogRejectsAnotherLocation(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	runner := catalogRunner(func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: fmt.Sprintf(`{"location":{"directory":%q},"data":[]}`, other)}, nil
	})
	_, err := ReadCatalog(context.Background(), runner, root)
	if err == nil || !strings.Contains(err.Error(), "different directory") {
		t.Fatalf("error = %v, want different-directory rejection", err)
	}
}

func TestReadCatalogDiagnosticsDoNotDiscloseCommandOutput(t *testing.T) {
	const sensitive = "credential-token account-identifier raw-api-payload"
	tests := []struct {
		name   string
		result execx.Result
		err    error
	}{
		{name: "cannot start", err: fmt.Errorf("spawn failed: %s", sensitive)},
		{name: "nonzero", result: execx.Result{Stdout: sensitive, Stderr: sensitive, ExitCode: 7}},
		{name: "malformed", result: execx.Result{Stdout: `{"headers":{"authorization":"credential-token"},"data":"raw-api-payload account-identifier"}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := catalogRunner(func(execx.Cmd) (execx.Result, error) { return tc.result, tc.err })
			_, err := ReadCatalog(context.Background(), runner, t.TempDir())
			if err == nil {
				t.Fatal("ReadCatalog succeeded")
			}
			for _, forbidden := range strings.Fields(sensitive) {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("diagnostic disclosed %q: %v", forbidden, err)
				}
			}
			if !strings.Contains(err.Error(), "OpenCode model catalog") || !strings.Contains(err.Error(), "retry") {
				t.Fatalf("diagnostic is not actionable: %v", err)
			}
		})
	}
}

func TestReadCatalogRejectsMalformedSafeShape(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		fmt.Sprintf(`{"location":{"directory":%q}}`, root),
		fmt.Sprintf(`{"location":{"directory":%q},"data":[{"id":"m","providerID":"p","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]}}]}`, root),
		fmt.Sprintf(`{"location":{"directory":%q},"data":[{"id":"m","providerID":"p","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{}]}]}`, root),
	}
	for i, response := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			runner := catalogRunner(func(execx.Cmd) (execx.Result, error) { return execx.Result{Stdout: response}, nil })
			_, err := ReadCatalog(context.Background(), runner, root)
			if err == nil || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("error = %v, want malformed response", err)
			}
		})
	}
}
