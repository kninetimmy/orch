package ghops

import (
	"context"
	"errors"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestLatestRelease(t *testing.T) {
	root := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name:   "gh",
		Args:   []string{"api", "repos/" + UpstreamRepo + "/releases/latest", "--jq", ".tag_name"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: "v0.2.0\n",
	}}}
	tag, err := LatestRelease(context.Background(), script, root)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if tag != "v0.2.0" {
		t.Errorf("tag = %q, want %q", tag, "v0.2.0")
	}
	script.AssertExhausted()
}

func TestLatestReleaseNonZeroExit(t *testing.T) {
	root := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name:   "gh",
		Args:   []string{"api", "repos/" + UpstreamRepo + "/releases/latest", "--jq", ".tag_name"},
		Dir:    root,
		Env:    ghTestEnv,
		Stderr: "HTTP 404: Not Found",
		Exit:   1,
	}}}
	if _, err := LatestRelease(context.Background(), script, root); err == nil {
		t.Fatal("LatestRelease: want error on non-zero exit")
	}
}

func TestLatestReleaseEmptyTag(t *testing.T) {
	root := tempRoot(t)
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "gh",
		Args: []string{"api", "repos/" + UpstreamRepo + "/releases/latest", "--jq", ".tag_name"},
		Dir:  root,
		Env:  ghTestEnv,
	}}}
	if _, err := LatestRelease(context.Background(), script, root); err == nil {
		t.Fatal("LatestRelease: want error on empty tag_name")
	}
}

func TestLatestReleaseRunnerError(t *testing.T) {
	root := tempRoot(t)
	sentinel := errors.New("spawn failed")
	script := &execxtest.Script{T: t, Calls: []execxtest.Call{{
		Name: "gh",
		Args: []string{"api", "repos/" + UpstreamRepo + "/releases/latest", "--jq", ".tag_name"},
		Err:  sentinel,
	}}}
	_, err := LatestRelease(context.Background(), script, root)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped runner error", err)
	}
}
