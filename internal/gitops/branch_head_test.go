package gitops

import (
	"context"
	"encoding/pem"
	"errors"
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

type branchTransportRunner func(context.Context, execx.Cmd) (execx.Result, error)

func (r branchTransportRunner) Run(ctx context.Context, cmd execx.Cmd) (execx.Result, error) {
	return r(ctx, cmd)
}

func TestBoundHTTPSRemoteDoesNotFollowRedirects(t *testing.T) {
	setupGitEnv(t)
	for _, name := range []string{"HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("GIT_ALLOW_PROTOCOL", "https")
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
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
	overrides := "'http." + endpoint + "/.followRedirects'='true' 'http." + endpoint + "/.sslVerify'='false'"
	t.Setenv("GIT_CONFIG_PARAMETERS", overrides)
	bind := func() error {
		_, err := g.BindRemoteBranchAtHead(t.Context(), "origin", "issue", "0000000000000000000000000000000000000001",
			func(value string) bool { return value == endpoint })
		return err
	}
	if err := bind(); err == nil || !strings.Contains(err.Error(), "unset GIT_SSL_NO_VERIFY") {
		t.Fatalf("bound HTTPS did not refuse the verification opt-out: %v", err)
	}
	if err := os.Unsetenv("GIT_SSL_NO_VERIFY"); err != nil {
		t.Fatal(err)
	}
	if err := bind(); err == nil || strings.Contains(err.Error(), endpoint) {
		t.Fatalf("bound HTTPS trusted an unrecognized certificate or leaked the URL: %v", err)
	}
	if err := os.Unsetenv("GIT_CONFIG_PARAMETERS"); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", ca)
	rawGit(t, root, "config", "http.schannelUseSSLCAInfo", "true")
	if err := bind(); err != nil {
		t.Fatalf("matching HTTPS endpoint was not usable without inherited Git parameters: %v", err)
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", overrides)
	if err := bind(); err != nil {
		t.Fatalf("matching HTTPS endpoint was not usable with inherited Git parameters: %v", err)
	}
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	rawGit(t, root, "config", "remote.origin.proxy", proxy.URL)
	for _, args := range [][]string{{"ls-remote", "origin"}, {"push", "--dry-run", "origin", ":refs/heads/issue"}} {
		before := proxyRequests.Load()
		res, err := (execx.Local{}).Run(t.Context(), execx.Cmd{Name: "git", Dir: root, Args: args, Env: gitEnv})
		if err != nil || res.ExitCode == 0 || proxyRequests.Load() <= before {
			t.Fatalf("named %s did not select the named HTTPS proxy: %+v %v", args[0], res, err)
		}
	}
	before := proxyRequests.Load()
	if err := bind(); err != nil || proxyRequests.Load() != before {
		t.Fatalf("bound lookup used a named HTTPS proxy: %v", err)
	}
	rawGit(t, root, "config", "--unset", "remote.origin.proxy")
	redirect.Store(true)
	rawGit(t, root, "ls-remote", "--heads", "origin")
	if count := otherReads.Load(); count != 1 {
		t.Fatalf("named remote did not follow the configured redirect: %d alternate reads", count)
	}
	if err := bind(); err == nil || otherReads.Load() != 1 || strings.Contains(err.Error(), endpoint) {
		t.Fatalf("bound remote followed a redirect or exposed the endpoint: %v (%d alternate reads)", err, otherReads.Load())
	}
}

func TestBoundSSHTransportPinsOpenSSHPolicy(t *testing.T) {
	setupGitEnv(t)
	_, root := newRepo(t)
	t.Setenv("GIT_SSH_COMMAND", "ambient-ssh-command")
	t.Setenv("GIT_SSH", "ambient-ssh")
	t.Setenv("GIT_SSH_VARIANT", "plink")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.sshCommand'='ambient-core-command'")
	const head = "0000000000000000000000000000000000000001"
	const ref = "refs/heads/issue"
	var sshCommand string
	for _, endpoint := range []string{"git@github.example.invalid:owner/repo.git", "ssh://git@github.example.invalid:22/owner/repo.git"} {
		t.Run(endpoint, func(t *testing.T) {
			var transfers []string
			g := &Git{root: root, r: branchTransportRunner(func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
				if cmd.Args[0] == "remote" {
					return execx.Result{Stdout: endpoint + "\n"}, nil
				}
				transfers = append(transfers, cmd.Args[0])
				var picked string
				for _, entry := range cmd.Env {
					if value, ok := strings.CutPrefix(entry, "GIT_SSH_COMMAND="); ok {
						picked = value
					}
				}
				if picked == "" || strings.Contains(picked, "ambient") || strings.Contains(picked, "github.example.invalid") ||
					!slices.Contains(cmd.Env, "GIT_SSH_VARIANT=ssh") {
					t.Fatal("bound Git transfer did not replace ambient SSH routing with an endpoint-independent command")
				}
				_, emptyConfig, err := systemOpenSSH()
				if err != nil || !strings.Contains(picked, " -F "+emptyConfig+" ") {
					t.Fatal("bound Git transfer must ignore both SSH client configuration files")
				}
				if sshCommand != "" && sshCommand != picked {
					t.Fatal("lookup and deletion did not use the same SSH policy")
				}
				sshCommand = picked
				return execx.Result{Stdout: head + "\t" + ref + "\n"}, nil
			})}
			binding, err := g.BindRemoteBranchAtHead(t.Context(), "tracking", "issue", head, func(value string) bool { return value == endpoint })
			if err != nil {
				t.Fatal(err)
			}
			if err := binding.Delete(t.Context(), ExplicitConfirmation()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(transfers, []string{"ls-remote", "push"}) {
				t.Fatalf("transport calls = %v", transfers)
			}
		})
	}
	if t.Failed() {
		return
	}
	if executable, _, err := systemOpenSSH(); err == nil {
		if _, err := os.Stat(executable); os.IsNotExist(err) {
			t.Log("No system OpenSSH; the installed-client policy probe is unavailable")
			return
		}
	}
	// Use Git's shell to exercise the exact command quoting, but ask OpenSSH
	// only to report its effective policy. No SSH server is contacted.
	config := rawGit(t, root, "-c", "alias.report-ssh=!"+sshCommand+" -G -p 22 git@github.example.invalid", "report-ssh")
	fields := make(map[string]string)
	for line := range strings.SplitSeq(config, "\n") {
		key, value, _ := strings.Cut(line, " ")
		fields[key] = strings.TrimSpace(value)
	}
	for k, want := range map[string][]string{
		"hostname": {"github.example.invalid"}, "user": {"git"}, "port": {"22"},
		"batchmode": {"yes"}, "stricthostkeychecking": {"true", "yes"},
		"canonicalizehostname": {"false", "no"}, "proxycommand": {"", "none"}, "proxyjump": {"", "none"},
		"controlmaster": {"false", "no"}, "controlpath": {"", "none"},
		"checkhostip":      {"false", "no"},
		"verifyhostkeydns": {"false", "no"}, "updatehostkeys": {"false", "no"},
	} {
		if !slices.Contains(want, fields[k]) {
			t.Errorf("effective OpenSSH %s = %q, want %v", k, fields[k], want)
		}
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
