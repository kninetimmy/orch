package gitops

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

// LocalBranchAtHead treats a missing exact branch as absent, but not a
// different tip, symbolic ref or failed lookup.
func (g *Git) LocalBranchAtHead(ctx context.Context, branch, head string) (bool, error) {
	ref := "refs/heads/" + branch
	res, err := g.run(ctx, g.root, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)", "--", ref)
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stderr) != "" {
		return false, fmt.Errorf("could not verify the local branch ref; preserve it and inspect Git")
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		name, details, _ := strings.Cut(strings.TrimSuffix(line, "\r"), "\t")
		if name != ref {
			continue
		}
		if details != head+"\t" {
			return false, fmt.Errorf("local branch differs from the approved PR head or is symbolic; preserve it")
		}
		return true, nil
	}
	return false, nil
}

// RemoteBranchBinding keeps the verified endpoint as well as the approved
// ref/OID. A lease pins the ref value, not the repository it is sent to.
type RemoteBranchBinding struct {
	g                           *Git
	remote, endpoint, transport string
	ref, head                   string
	sshCommand                  string
	httpsConfig                 string
}

// BindRemoteBranchAtHead requires a single effective read/push destination
// matching the caller's recorded repository, then checks the exact ref there.
func (g *Git) BindRemoteBranchAtHead(ctx context.Context, remote, branch, head string, matchesRepository func(string) bool) (*RemoteBranchBinding, error) {
	endpoint, err := g.branchDestination(ctx, remote)
	if err != nil {
		return nil, err
	}
	if matchesRepository == nil || !matchesRepository(endpoint) {
		return nil, fmt.Errorf("remote destination does not identify the recorded PR repository; preserve the checkouts and branches and inspect Git")
	}
	binding := &RemoteBranchBinding{g: g, remote: remote, endpoint: endpoint, transport: "orch-cleanup://" + rand.Text(),
		ref: "refs/heads/" + branch, head: head}
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		if err := binding.bindHTTPS(ctx); err != nil {
			return nil, err
		}
	case strings.HasPrefix(endpoint, "ssh://"), strings.HasPrefix(endpoint, "git@"):
		binding.sshCommand, err = boundSSHCommand()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("remote destination has no supported bound transport; preserve the checkouts and branches")
	}
	if _, err := binding.Exists(ctx); err != nil {
		return nil, err
	}
	return binding, nil
}

func (g *Git) branchDestination(ctx context.Context, remote string) (string, error) {
	read, readErr := g.runBranchTransport(ctx, nil, "remote", "get-url", "--all", "--", remote)
	push, pushErr := g.runBranchTransport(ctx, nil, "remote", "get-url", "--push", "--all", "--", remote)
	// Strip one output terminator; trimming whitespace could change the
	// destination. Git expands URL rewrites in get-url.
	line := func(s string) string { return strings.TrimSuffix(s, "\n") }
	endpoint := line(read.Stdout)
	if readErr != nil || pushErr != nil || read.ExitCode != 0 || push.ExitCode != 0 ||
		strings.TrimSpace(read.Stderr) != "" || strings.TrimSpace(push.Stderr) != "" ||
		strings.TrimSpace(endpoint) == "" || strings.ContainsAny(endpoint, "\r\n\x00") || endpoint != line(push.Stdout) {
		return "", fmt.Errorf("remote read and deletion destinations could not be matched; preserve the branch")
	}
	return endpoint, nil
}

// VerifyDestination refuses observed changes, but is not the deletion
// authority: both transfers still use the pinned endpoint, not the alias.
func (b *RemoteBranchBinding) VerifyDestination(ctx context.Context) error {
	endpoint, err := b.g.branchDestination(ctx, b.remote)
	if err != nil {
		return err
	}
	if endpoint != b.endpoint {
		return fmt.Errorf("remote destination changed since cleanup preflight; preserve the branch")
	}
	return nil
}

// Exists accepts only absence or the exact approved remote ref at the pin.
func (b *RemoteBranchBinding) Exists(ctx context.Context) (bool, error) {
	res, err := b.run(ctx, "ls-remote", "--heads", "--refs", "--", b.transport, b.ref)
	if err == errBoundHTTPSVerify {
		return false, err
	}
	if err != nil || res.ExitCode != 0 {
		if b.sshCommand != "" {
			return false, fmt.Errorf("could not verify the approved remote branch with strict OpenSSH; preserve it and check the system client, known host key and noninteractive authentication")
		}
		return false, fmt.Errorf("could not verify the approved remote branch; preserve it and inspect Git")
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return false, nil
	}
	if out != b.head+"\t"+b.ref {
		return false, fmt.Errorf("remote branch differs from the approved PR head or could not be verified; preserve it")
	}
	return true, nil
}

// Delete uses an explicit lease and sends only the approved ref deletion.
func (b *RemoteBranchBinding) Delete(ctx context.Context, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: delete the bound remote branch", ErrNotConfirmed)
	}
	res, err := b.run(ctx, "push", "--no-follow-tags", "--no-recurse-submodules", "--force-with-lease="+b.ref+":"+b.head,
		"--", b.transport, ":"+b.ref)
	if err != nil || res.ExitCode != 0 {
		// Even Git's failure output can contain credentials or raw URLs.
		return fmt.Errorf("approved remote branch deletion was refused or could not be verified; preserve remaining work and inspect Git")
	}
	return nil
}

var errBoundHTTPSVerify = errors.New("bound HTTPS cleanup requires TLS verification; unset GIT_SSL_NO_VERIFY and re-confirm cleanup")

var errBoundHTTPSConfig = errors.New("bound HTTPS cleanup could not verify Git extra headers; preserve the work and inspect http.extraHeader settings")

func (b *RemoteBranchBinding) bindHTTPS(ctx context.Context) error {
	// Only the config command honors GIT_CONFIG as an alternate file. It
	// would not describe the configuration used by the transport.
	if _, overridden := os.LookupEnv("GIT_CONFIG"); overridden {
		return errors.New("bound HTTPS cleanup cannot verify Git extra headers with GIT_CONFIG set; unset it and re-confirm cleanup")
	}
	parameters := os.Getenv("GIT_CONFIG_PARAMETERS")
	res, err := b.g.runBranchTransport(ctx, []string{"GIT_CONFIG_PARAMETERS=" + parameters},
		"config", "--includes", "--null", "--get-regexp", "^http\\.(.*\\.)?extraheader$")
	if err != nil || res.Stderr != "" || (res.ExitCode != 0 && (res.ExitCode != 1 || res.Stdout != "")) {
		return errBoundHTTPSConfig
	}
	var headers []string
	if res.ExitCode == 0 {
		headers, err = b.httpsExtraHeaders(ctx, res.Stdout)
		if err != nil {
			return err
		}
	}
	// An exact URL match at the end of command config wins even over
	// URL-scoped file and environment entries. Reset the whole list, then
	// replay only the checked snapshot. File edits cannot add a header to
	// a later transfer; we do not freeze the rest of Git's configuration.
	key := "http." + strings.TrimRight(b.endpoint, "/") + "/."
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	if parameters != "" {
		parameters += " "
	}
	parameters += quote(key+"followRedirects") + "='false' " + quote(key+"sslVerify") + "='true'"
	for _, value := range append([]string{""}, headers...) {
		parameters += " " + quote(key+"extraHeader") + "=" + quote(value)
	}
	b.httpsConfig = "GIT_CONFIG_PARAMETERS=" + parameters
	return nil
}

func (b *RemoteBranchBinding) httpsExtraHeaders(ctx context.Context, records string) ([]string, error) {
	endpoint, user, ok := headerMatchURL(b.endpoint)
	records, terminated := strings.CutSuffix(records, "\x00")
	if !ok || !terminated {
		return nil, errBoundHTTPSConfig
	}
	var headers []string
	var best string
	for record := range strings.SplitSeq(records, "\x00") {
		name, value, valued := strings.Cut(record, "\n")
		if !valued {
			return nil, errBoundHTTPSConfig
		}
		if name == "http.extraheader" {
			if best != "" {
				continue
			}
		} else {
			scope, scoped := strings.CutPrefix(name, "http.")
			scope, extra := strings.CutSuffix(scope, ".extraheader")
			candidate, scopeUser, valid := headerMatchURL(scope)
			if !scoped || !extra || !valid {
				return nil, errBoundHTTPSConfig
			}
			if scopeUser != "" && scopeUser != user {
				continue
			}
			if candidate != best {
				wins, err := b.g.httpsHeaderScopeWins(ctx, endpoint, best, candidate)
				if err != nil {
					return nil, err
				}
				if !wins {
					continue
				}
				best = candidate
			}
		}
		if value == "" {
			headers = nil // An explicit empty value is Git's list reset.
		} else {
			headers = append(headers, value)
		}
	}
	for _, value := range headers {
		if !boundHTTPSAuthHeader(value) {
			return nil, errors.New("bound HTTPS cleanup allows only well-formed Authorization and Proxy-Authorization extra headers; preserve the work and inspect Git config")
		}
	}
	return headers, nil
}

// The HTTP callback sees matching entries in order, including entries before
// a more specific match. A normal --get-urlmatch returns only the last value.
// Ask Git to compare the candidate to the best scope so far, with marker
// values in an isolated stdin config. No auth values are needed for matching.
func (g *Git) httpsHeaderScopeWins(ctx context.Context, endpoint, best, candidate string) (bool, error) {
	var config strings.Builder
	escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	for i, scope := range []string{best, candidate} {
		if scope != "" {
			fmt.Fprintf(&config, "[http \"%s\"]\n orch-match = %d\n", escape.Replace(scope), i)
		}
	}
	res, err := g.r.Run(ctx, execx.Cmd{Name: "git", Dir: g.root, Env: branchTransportEnv(), Stdin: strings.NewReader(config.String()),
		Args: []string{"config", "--file", "-", "--no-includes", "--null", "--get-urlmatch", "http.orch-match", endpoint}})
	if err == nil && res.Stderr == "" {
		switch {
		case res.ExitCode == 0 && res.Stdout == "1\x00":
			return true, nil
		case res.ExitCode == 0 && res.Stdout == "0\x00" && best != "", res.ExitCode == 1 && res.Stdout == "" && best == "":
			return false, nil
		}
	}
	return false, errBoundHTTPSConfig
}

var urlUserEscapes = regexp.MustCompile("%[0-9A-Fa-f]{2}")

// Keep credentials off the matcher argv and out of its input. Git ignores
// passwords and compares usernames case-sensitively after normalizing URL
// escapes. Project an equivalent username to a fixed, non-secret marker.
func headerMatchURL(raw string) (masked, user string, ok bool) {
	u, err := url.Parse(raw)
	scheme, rest, hasScheme := strings.Cut(raw, "://")
	if err != nil || !hasScheme || u.Host == "" {
		return "", "", false
	}
	if u.User == nil {
		return raw, "", true
	}
	authority := rest
	if stop := strings.IndexAny(rest, "/?#"); stop >= 0 {
		authority = rest[:stop]
	}
	userinfo, _, _ := strings.Cut(authority, "@")
	if strings.Count(authority, "@") != 1 {
		return "", "", false
	}
	username, _, _ := strings.Cut(userinfo, ":")
	user = urlUserEscapes.ReplaceAllStringFunc(username, func(escape string) string {
		raw, _ := url.PathUnescape(escape)
		if strings.Contains("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~", raw) {
			return raw
		}
		return strings.ToUpper(escape)
	})
	return scheme + "://orch-match@" + rest[len(userinfo)+1:], user + "@", true
}

func boundHTTPSAuthHeader(value string) bool {
	name, field, ok := strings.Cut(value, ":")
	if !ok || (!strings.EqualFold(name, "Authorization") && !strings.EqualFold(name, "Proxy-Authorization")) ||
		strings.ContainsFunc(field, func(r rune) bool { return r != '\t' && (r < ' ' || r > '~') }) {
		return false
	}
	scheme, credentials, ok := strings.Cut(strings.Trim(field, " \t"), " ")
	return ok && scheme != "" && strings.Trim(credentials, " \t") != "" && !strings.ContainsFunc(scheme, func(r rune) bool {
		return !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&'*+-.^_`|~", r)
	})
}

func (b *RemoteBranchBinding) run(ctx context.Context, args ...string) (execx.Result, error) {
	// Git rewrites even a literal URL passed to push. A fresh opaque token
	// maps directly to the already-expanded endpoint in Git's single rewrite
	// pass, without alias lookup, a second user rewrite or a config write.
	// Keep URL bytes out of argv and append to any inherited config entries.
	var count uint64
	if raw := os.Getenv("GIT_CONFIG_COUNT"); raw != "" {
		var err error
		count, err = strconv.ParseUint(raw, 10, 31)
		if err != nil || count > (1<<31)-3 {
			return execx.Result{}, fmt.Errorf("could not bind the Git transport configuration")
		}
	}
	config := []string{
		"GIT_CONFIG_COUNT=" + strconv.FormatUint(count+2, 10),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=url.%s.insteadOf", count, b.endpoint),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count, b.transport),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=url.%s.pushInsteadOf", count+1, b.endpoint),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count+1, b.transport),
	}
	if b.sshCommand != "" {
		// Git gives GIT_SSH_COMMAND precedence over core.sshCommand and GIT_SSH.
		config = append(config, "GIT_SSH_COMMAND="+b.sshCommand, "GIT_SSH_VARIANT=ssh")
	} else {
		// Git checks for presence, not a Boolean value, so setting this to
		// false in the child environment would still disable verification.
		if _, insecure := os.LookupEnv("GIT_SSL_NO_VERIFY"); insecure {
			return execx.Result{}, errBoundHTTPSVerify
		}
		config = append(config, b.httpsConfig)
	}
	return b.g.runBranchTransport(ctx, config, args...)
}

func (g *Git) runBranchTransport(ctx context.Context, config []string, args ...string) (execx.Result, error) {
	return g.r.Run(ctx, execx.Cmd{Name: "git", Args: args, Dir: g.root, Env: append(branchTransportEnv(), config...)})
}

func branchTransportEnv() []string {
	// A trace can contain a credential-bearing URL even when the argv here
	// does not, for example in Git's own transport helper invocation.
	return append([]string{"GIT_TRACE=0", "GIT_TRACE_CURL=0", "GIT_TRACE_PACKET=0", "GIT_TRACE_PERFORMANCE=0",
		"GIT_TRACE2=0", "GIT_TRACE2_EVENT=0", "GIT_TRACE2_PERF=0"}, gitEnv...)
}

// RequireBranchIdle permits attachment only in allowedWorktree, if provided.
// A detached worktree can still use a branch via rebase (including sequencer
// update-refs), git am or bisect. Conservatively refuse these operations rather
// than bypass git branch -D's protection when using a conditional ref update.
func (g *Git) RequireBranchIdle(ctx context.Context, branch, allowedWorktree string) error {
	worktrees, err := g.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	common, err := g.CommonDir(ctx)
	if err != nil {
		return err
	}
	info, err := os.Stat(common)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("could not verify the Git common directory; preserve the branch")
	}
	for _, wt := range worktrees {
		if wt.Branch == branch && wt.Path != allowedWorktree {
			return fmt.Errorf("branch %s is checked out in an unexpected worktree; preserve it", branch)
		}
		out, err := g.git(ctx, wt.Path, "rev-parse", "--path-format=absolute", "--git-common-dir",
			"--git-path", "rebase-merge", "--git-path", "rebase-apply", "--git-path", "BISECT_START")
		if err != nil {
			return err
		}
		fields := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
		if len(fields) != 4 || !filepath.IsAbs(fields[0]) {
			return fmt.Errorf("could not verify Git operation state; preserve the branch")
		}
		linked, err := os.Stat(fields[0])
		if err != nil || !os.SameFile(info, linked) {
			return fmt.Errorf("could not verify worktree Git identity; preserve the branch")
		}
		for _, marker := range fields[1:] {
			rel, err := filepath.Rel(fields[0], marker)
			if err != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("could not verify Git operation state; preserve the branch")
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				return fmt.Errorf("rebase, am or bisect state is present or unverifiable; preserve the branch")
			}
		}
	}
	return nil
}

// ForceDeleteBranchAtHead checks worktree use and asks Git to delete the
// exact ref only if it still holds the approved OID. It leaves branch config
// alone, since removing the section separately could affect a recreated branch.
func (g *Git) ForceDeleteBranchAtHead(ctx context.Context, branch, head string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: force-delete branch %s", ErrNotConfirmed, branch)
	}
	exists, err := g.LocalBranchAtHead(ctx, branch, head)
	if err != nil || !exists {
		return err
	}
	if err := g.RequireBranchIdle(ctx, branch, ""); err != nil {
		return err
	}
	_, err = g.git(ctx, g.root, "update-ref", "--no-deref", "-d", "refs/heads/"+branch, head)
	return err
}
