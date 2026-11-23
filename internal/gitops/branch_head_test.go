package gitops

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
)

type branchTransportRunner func(context.Context, execx.Cmd) (execx.Result, error)

func (r branchTransportRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	return r(ctx, cmd)
}

func TestBoundRemoteDoesNotProjectTransportSecrets(t *testing.T) {
	const url = "https://fixture:credential-token@example.invalid/repo.git"
	const head = "0000000000000000000000000000000000000001"
	const ref = "refs/heads/orch/issue-1"
	for _, failure := range []string{"remote", "ls-remote", "push"} {
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
					return execx.Result{Stdout: head + "\t" + ref + "\n"}, nil
				}
				t.Fatal("unexpected transport command")
				return execx.Result{}, nil
			})}
			bound, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "orch/issue-1", head)
			if err == nil && failure == "push" {
				err = bound.Delete(t.Context(), ExplicitConfirmation())
			}
			if err == nil || strings.Contains(err.Error(), "credential-token") {
				t.Fatal("transport failure must refuse without disclosing credentials")
			}
		})
	}
}
