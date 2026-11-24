package gitops

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

func includeHeaderFixture(t *testing.T, root, config string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "headers.gitconfig")
	if err := os.WriteFile(file, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "config", "--add", "include.path", filepath.ToSlash(file))
}

func TestBoundHTTPSRefusesUnverifiedHeaders(t *testing.T) {
	const endpoint = "https://example.invalid/owner/repo.git"
	const unknown = "X-Orch-Test: dummy-header-value"
	for _, test := range []struct{ name, file, parameters, countValue string }{
		{name: "base multi-value", file: "[http]\n extraHeader = Authorization: Dummy first\n extraHeader = " + unknown + "\n"},
		{name: "matching URL scope", file: "[http \"https://*.invalid/owner\"]\n extraHeader = " + unknown + "\n"},
		{name: "malformed authorization", file: "[http]\n extraHeader = Authorization : Dummy dummy-header-value\n"},
		{name: "multiline authorization", file: "[http]\n extraHeader = \"Authorization: Dummy first\\ndummy-header-value\"\n"},
		{name: "missing value", file: "[http]\n extraHeader\n"},
		{name: "command parameters", parameters: "'http.extraheader'='" + unknown + "'"},
		{name: "environment config", countValue: unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupGitEnv(t)
			g, root := newRepo(t)
			rawGit(t, root, "remote", "add", "origin", endpoint)
			if test.file != "" {
				includeHeaderFixture(t, root, test.file)
			}
			if test.parameters != "" {
				t.Setenv("GIT_CONFIG_PARAMETERS", test.parameters)
			}
			if test.countValue != "" {
				t.Setenv("GIT_CONFIG_COUNT", "1")
				t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
				t.Setenv("GIT_CONFIG_VALUE_0", test.countValue)
			}
			g.r = branchTransportRunner(func(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
				if cmd.Args[0] == "ls-remote" || cmd.Args[0] == "push" {
					t.Error("unverified header configuration reached a transfer")
					return execx.Result{ExitCode: 1}, nil
				}
				return (execx.Local{}).Run(ctx, cmd)
			})
			_, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "issue", "0000000000000000000000000000000000000001",
				func(value string) bool { return value == endpoint })
			if err == nil || !strings.Contains(err.Error(), "bound HTTPS cleanup") ||
				strings.Contains(err.Error(), "dummy-header-value") || strings.Contains(err.Error(), endpoint) {
				t.Fatal("header policy did not refuse with a redacted diagnostic")
			}
		})
	}
}

func TestBoundHTTPSPinsValidatedHeaders(t *testing.T) {
	const proxyAuth = "Basic ZHVtbXk6ZHVtbXk="
	const ambientUserAgent = "orch-test-environment/1"
	setupGitEnv(t)
	for _, name := range []string{"HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("GIT_ALLOW_PROTOCOL", "https")
	t.Setenv("GIT_HTTP_USER_AGENT", ambientUserAgent)
	var accepted atomic.Int32
	var pushObserved atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		wantUserAgent := "orch-cleanup/1"
		if accepted.Load() == 0 {
			wantUserAgent = ambientUserAgent // The first request is ordinary Git.
		}
		if got := r.Header.Values("User-Agent"); !slices.Equal(got, []string{wantUserAgent}) {
			t.Errorf("%s User-Agent = %q, want [%q]", service, got, wantUserAgent)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path != "/owner/repo.git/info/refs" || r.Header.Get("X-Orch-Test") != "" || r.Header.Get("Proxy-Authorization") != proxyAuth ||
			!slices.Equal(r.Header.Values("Authorization"), []string{"Dummy first", "Dummy second", "Dummy third"}) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		accepted.Add(1)
		if service == "git-receive-pack" {
			pushObserved.Store(true)
			// Only observe push discovery here; real deletion is exercised in
			// the separate Windows smoke fixture.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		fmt.Fprint(w, "001e# service=git-upload-pack\n00000000")
	}))
	t.Cleanup(server.Close)
	g, root := newRepo(t)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", ca)
	rawGit(t, root, "config", "http.schannelUseSSLCAInfo", "true")
	authority := strings.TrimPrefix(server.URL, "https://")
	endpoint := "https://fixture-user:fixture-password@" + authority + "/owner/repo.git"
	// Use a reset, two increasing URL matches, a non-matching scope, and
	// lower-priority command config after the URL-scoped entries.
	includeHeaderFixture(t, root, fmt.Sprintf("[remote \"origin\"]\n url = %s\n"+
		"[http]\n extraHeader = X-Orch-Test: reset by following entry\n extraHeader =\n extraHeader = Authorization: Dummy first\n"+
		"[http \"%s/owner/\"]\n extraHeader = authorization: Dummy second\n"+
		"[http \"https://%%66ixture-user@%s/owner/repo.git/\"]\n extraHeader = Authorization: Dummy third\n userAgent = orch-test-config/1\n"+
		"[http \"%s/owner/other.git/\"]\n extraHeader = X-Orch-Test: irrelevant URL scope\n", endpoint, server.URL, authority, server.URL))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.https://fixture-user@"+authority+"/owner/repo.git/.extraHeader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Proxy-Authorization: "+proxyAuth)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'http.extraHeader'='X-Orch-Test: less specific than the chosen scope'")
	// Confirm that Git's ordinary configuration really sends this exact list
	// before cleanup pins it. The fixture rejects any other list.
	res, err := (execx.Local{}).Run(t.Context(), execx.Cmd{Name: "git", Dir: root, Args: []string{"ls-remote", "origin"}, Env: gitEnv})
	if err != nil || res.ExitCode != 0 || accepted.Load() != 1 {
		t.Fatal("ordinary Git did not authenticate with the expected effective header list")
	}
	changed := false
	g.r = branchTransportRunner(func(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
		for _, arg := range cmd.Args {
			if strings.Contains(arg, "fixture-user") || strings.Contains(arg, "fixture-password") || strings.Contains(arg, "Dummy ") || strings.Contains(arg, proxyAuth) {
				t.Fatal("bound verification exposed authentication data in argv")
			}
		}
		if cmd.Args[0] == "ls-remote" && !changed {
			changed = true
			// Edit only our disposable repo after the preflight config read.
			// The dummy header is an observation marker, not a routing rule.
			includeHeaderFixture(t, root, fmt.Sprintf("[http \"https://fixture-user@%s/owner/repo.git/\"]\n extraHeader = X-Orch-Test: late marker\n", authority))
		}
		return (execx.Local{}).Run(ctx, cmd)
	})
	binding, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "issue", "0000000000000000000000000000000000000001",
		func(value string) bool { return value == endpoint })
	if err != nil || !changed || accepted.Load() != 2 {
		t.Fatalf("validated authentication did not survive a config edit before transfer: %v", err)
	}
	if exists, err := binding.Exists(t.Context()); err != nil || exists || accepted.Load() != 3 {
		t.Fatalf("the same validated authentication was not pinned for the next transfer: %v", err)
	}
	if err := binding.Delete(t.Context(), ExplicitConfirmation()); err == nil || accepted.Load() != 4 || !pushObserved.Load() {
		t.Fatal("push discovery did not use the pinned authentication and User-Agent")
	}
}
