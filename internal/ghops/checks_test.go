package ghops

import (
	"context"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func rollupCall(root, stdout string) execxtest.Call {
	return execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "view", "43", "--json", "statusCheckRollup"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: stdout,
	}
}

func checksCall(root, stdout string, exit int) execxtest.Call {
	return execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "checks", "43", "--required", "--json", "name,state,bucket,link"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: stdout,
		Exit:   exit,
	}
}

func TestRequiredCINoChecks(t *testing.T) {
	root := tempRoot(t)
	// Empty rollup: exactly one call — pr checks is never invoked.
	g, script := openScripted(t, root, rollupCall(root, `{"statusCheckRollup":[]}`))
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CINoChecks || sum.Total != 0 || len(sum.Required) != 0 {
		t.Errorf("sum = %+v, want no-checks", sum)
	}
}

func TestRequiredCIPassing(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{},{}]}`),
		checksCall(root, `[{"name":"build","state":"SUCCESS","bucket":"pass","link":"https://ci/1"},{"name":"lint","state":"SUCCESS","bucket":"pass","link":"https://ci/2"}]`, 0),
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CIPassing || sum.Total != 3 || len(sum.Required) != 2 {
		t.Errorf("sum = %+v, want passing with 2 required of 3", sum)
	}
	if sum.Required[0].Name != "build" || sum.Required[0].Link != "https://ci/1" {
		t.Errorf("Required[0] = %+v", sum.Required[0])
	}
}

func TestRequiredCIPendingExit8(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{}]}`),
		checksCall(root, `[{"name":"build","state":"IN_PROGRESS","bucket":"pending","link":""},{"name":"lint","state":"SUCCESS","bucket":"pass","link":""}]`, 8),
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CIPending {
		t.Errorf("State = %q, want pending", sum.State)
	}
}

func TestRequiredCIFailingExit1(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{}]}`),
		checksCall(root, `[{"name":"build","state":"FAILURE","bucket":"fail","link":""},{"name":"lint","state":"IN_PROGRESS","bucket":"pending","link":""}]`, 1),
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CIFailing {
		t.Errorf("State = %q, want failing (fail wins over pending)", sum.State)
	}
}

func TestRequiredCINoneRequired(t *testing.T) {
	root := tempRoot(t)
	// Checks exist but none is required: no-checks with Total > 0 so
	// the engine can say "CI exists but none of it is required".
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{}]}`),
		checksCall(root, `[]`, 0),
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CINoChecks || sum.Total != 2 {
		t.Errorf("sum = %+v, want no-checks with Total 2", sum)
	}
}

// TestRequiredCINoRequiredChecksDiscoveryEmptyStdout pins the gh 2.87.3
// shape discovered live in this repo's first dogfooded Delivery run:
// a repo with CI checks but none marked required by branch protection
// makes `pr checks --required` exit 0 with empty stdout (not `[]`)
// and the stderr notice below, rather than the `[]` this package's
// other no-required-checks test scripts.
func TestRequiredCINoRequiredChecksDiscoveryEmptyStdout(t *testing.T) {
	root := tempRoot(t)
	call := checksCall(root, "", 0)
	call.Stderr = "no required checks reported on the 'main' branch"
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{}]}`),
		call,
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CINoChecks || sum.Total != 2 || len(sum.Required) != 0 {
		t.Errorf("sum = %+v, want no-checks with Total 2 and no required checks", sum)
	}
}

// TestRequiredCIWhitespaceOnlyStdout confirms whitespace-only stdout
// (e.g. a stray newline) is treated the same as truly empty stdout,
// not as malformed JSON.
func TestRequiredCIWhitespaceOnlyStdout(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{},{}]}`),
		checksCall(root, "\n  \t\n", 0),
	)
	sum, err := g.RequiredCI(context.Background(), 43)
	if err != nil {
		t.Fatalf("RequiredCI: %v", err)
	}
	script.AssertExhausted()
	if sum.State != CINoChecks || sum.Total != 2 || len(sum.Required) != 0 {
		t.Errorf("sum = %+v, want no-checks with Total 2 and no required checks", sum)
	}
}

// TestRequiredCIMalformedStdout confirms non-empty stdout that isn't
// valid JSON still fails closed rather than being silently treated as
// no required checks.
func TestRequiredCIMalformedStdout(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{}]}`),
		checksCall(root, "not json", 0),
	)
	_, err := g.RequiredCI(context.Background(), 43)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "returned unparsable JSON") {
		t.Fatalf("err = %v, want unparsable JSON error", err)
	}
}

func TestRequiredCIUnexpectedExit(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root,
		rollupCall(root, `{"statusCheckRollup":[{}]}`),
		checksCall(root, "", 4),
	)
	_, err := g.RequiredCI(context.Background(), 43)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "exited 4") {
		t.Fatalf("err = %v, want unexpected exit error", err)
	}
}

func TestDeriveCIState(t *testing.T) {
	tests := map[string]struct {
		buckets []string
		want    CIState
	}{
		"none":               {nil, CINoChecks},
		"all pass":           {[]string{"pass", "pass"}, CIPassing},
		"skipping is pass":   {[]string{"pass", "skipping"}, CIPassing},
		"pending":            {[]string{"pass", "pending"}, CIPending},
		"fail":               {[]string{"pass", "fail"}, CIFailing},
		"cancel":             {[]string{"cancel"}, CIFailing},
		"fail beats pending": {[]string{"pending", "fail"}, CIFailing},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			checks := make([]Check, len(tt.buckets))
			for i, b := range tt.buckets {
				checks[i] = Check{Bucket: b}
			}
			if got := deriveCIState(checks); got != tt.want {
				t.Errorf("deriveCIState(%v) = %q, want %q", tt.buckets, got, tt.want)
			}
		})
	}
}
