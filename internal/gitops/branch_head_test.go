package gitops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

type branchTransportRunner func(context.Context, execx.Cmd) (execx.Result, error)

func (r branchTransportRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	return r(ctx, cmd)
}

func TestBoundHTTPSRemoteDoesNotFollowRedirects(t *testing.T) {
	setupGitEnv(t)
	for _, name := range []string{"HTTPS_PROXY", "ALL_PROXY", "https_proxy", "all_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("GIT_ALLOW_PROTOCOL", "https")
	t.Setenv("GIT_SSL_NO_VERIFY", "1") // Only the loopback server's ephemeral certificate.
	var redirect atomic.Bool
	var otherReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/owner/repo.git/info/refs":
			if redirect.Load() {
				http.Redirect(w, r, "/owner/other.git/info/refs?service=git-upload-pack", http.StatusFound)
				return
			}
		case "/owner/other.git/info/refs":
			otherReads.Add(1)
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		fmt.Fprint(w, "001e# service=git-upload-pack\n00000000") // Empty ref advertisement.
	}))
	t.Cleanup(server.Close)
	g, root := newRepo(t)
	endpoint := server.URL + "/owner/repo.git"
	rawGit(t, root, "remote", "add", "origin", endpoint)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'http."+endpoint+"/.followRedirects'='true'")
	bind := func() error {
		_, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "issue", "0000000000000000000000000000000000000001",
			func(value string) bool { return value == endpoint })
		return err
	}
	if err := bind(); err != nil {
		t.Fatalf("matching HTTPS endpoint was not usable: %v", err)
	}
	redirect.Store(true)
	rawGit(t, root, "ls-remote", "--heads", "origin")
	if count := otherReads.Load(); count != 1 {
		t.Fatalf("named remote did not follow the configured redirect: %d alternate reads", count)
	}
	if err := bind(); err == nil || otherReads.Load() != 1 || strings.Contains(err.Error(), endpoint) {
		t.Fatalf("bound remote followed a redirect or exposed the endpoint: %v (%d alternate reads)", err, otherReads.Load())
	}
}

func TestBoundRemoteDoesNotProjectTransportSecrets(t *testing.T) {
	const url = "https://fixture:credential-token@example.invalid/owner/repo.git"
	const head = "0000000000000000000000000000000000000001"
	const ref = "refs/heads/orch/issue-1"
	for _, failure := range []string{"remote", "repository", "ls-remote", "push"} {
		t.Run(failure, func(t *testing.T) {
			g := &Git{root: t.TempDir(), r: branchTransportRunner(func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
				for _, arg := range cmd.Args {
					if strings.Contains(arg, "credential-token") {
						t.Fatal("transport put credentials on the command line")
					}
				}
				if !slices.Contains(cmd.Env, "GIT_TRACE=0") || !slices.Contains(cmd.Env, "GIT_TRACE2_EVENT=0") {
					t.Fatal("inherited Git tracing could log the URL")
				}
				if cmd.Args[0] == failure {
					// Neither raw Git output nor a runner error is safe to project.
					return execx.Result{ExitCode: 1, Stdout: url, Stderr: url}, errors.New(url)
				}
				switch cmd.Args[0] {
				case "remote":
					return execx.Result{Stdout: url + "\n"}, nil
				case "ls-remote":
					if failure == "repository" {
						t.Fatal("transport used an unverified destination")
					}
					return execx.Result{Stdout: head + "\t" + ref + "\n"}, nil
				}
				t.Fatal("unexpected transport command")
				return execx.Result{}, nil
			})}
			bound, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "orch/issue-1", head, func(endpoint string) bool {
				return failure != "repository" && endpoint == url
			})
			if err == nil && failure == "push" {
				err = bound.Delete(t.Context(), ExplicitConfirmation())
			}
			if err == nil || strings.Contains(err.Error(), "credential-token") || strings.Contains(err.Error(), "example.invalid") {
				t.Fatal("transport failure must refuse without disclosing credentials")
			}
		})
	}
}
