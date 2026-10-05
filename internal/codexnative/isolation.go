package codexnative

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/paths"
)

var ErrIsolationUnavailable = errors.New("codex isolation unavailable")

// IsolationPaths must name the complete approved layout. Protected directories
// may contain one another, but none may overlap the workspace or scratch.
// Existing Delivery worktrees nested under the main checkout are unsuitable.
type IsolationPaths struct {
	Workspace         string
	Scratch           string
	MainCheckout      string
	ControllerState   string
	SiblingWorkspaces []string
	CredentialPaths   []string
}

// IsolationCapabilities reports checked controls, not live inference evidence.
type IsolationCapabilities struct {
	HostVersion        string
	SandboxReady       bool
	ProfileAllowed     bool
	ToolsDisabled      bool // effective configuration only, never model-tool proof
	DisabledMCP        int  // names and configuration are not retained as evidence
	ModelToolsVerified bool // supported native enforcement plus actual-connection controls
}

type isolationBoundary struct {
	workspace  string
	scratch    string
	protected  []string
	reviewer   bool
	evaluation bool
	nonce      string
}

// IsolationPreflight checks a native profile without model turns or user-file
// probes. Supported native enforcement and actual-connection controls are both
// required; success does not authorize a task or enable this metadata connection.
func IsolationPreflight(ctx context.Context, options Options, layout IsolationPaths, reviewer bool) (capabilities IsolationCapabilities, err error) {
	b, err := prepareIsolation(layout, reviewer)
	if err != nil {
		return capabilities, err
	}
	return isolationPreflight(ctx, options, b)
}

func isolationPreflight(ctx context.Context, options Options, b isolationBoundary) (capabilities IsolationCapabilities, err error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return capabilities, fmt.Errorf("%w: only native Windows elevated sandbox diagnostics are supported", ErrIsolationUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	c, capabilities, err := openIsolation(ctx, options, b)
	if err != nil {
		return capabilities, err
	}
	defer func() {
		err = errors.Join(err, c.close(), c.contextError("isolation preflight"))
		if err != nil {
			capabilities.ModelToolsVerified = false
		}
	}()
	err = modelToolBoundary(c, b, capabilities)
	if err == nil && b.evaluation {
		err = verifyEvaluationConfig(c, b)
	}
	capabilities.ModelToolsVerified = err == nil
	return capabilities, err
}

func modelToolBoundary(c *connection, b isolationBoundary, capabilities IsolationCapabilities) error {
	if capabilities.HostVersion == "0.159.2" {
		return fmt.Errorf("%w: native 0.159.2 disabledPluginIds does not filter plugin capabilities; inherited connectors, plugins, hooks and additional agent tools have no verified closed boundary", ErrIsolationUnavailable)
	}
	// ponytail: only Windows 0.160.0 has reviewed source and native tool replay;
	// extend support only with equivalent enforcement evidence for another host.
	if capabilities.HostVersion != "0.160.0" {
		return restrictionError("native host has no reviewed model-tool enforcement")
	}
	if c == nil || c.cmd == nil || !c.isolation || !c.diagnosticReady || !capabilities.SandboxReady || !capabilities.ProfileAllowed || !capabilities.ToolsDisabled || c.profile != b.profile() || c.cmd.Dir != b.workspace {
		return restrictionError("model tools require the actual verified connection and boundary")
	}
	// Recheck after authentication/catalog inspection, not just the earlier child.
	config, err := readIsolationConfig(c, b.workspace)
	if err != nil {
		return err
	}
	if err := config.verify(b, c.disabledMCP); err != nil {
		return err
	}
	if config.ModelProvider != "openai" {
		return restrictionError("model tools require the managed OpenAI provider")
	}
	if err := config.verifyFeatures(modelRestrictedFeatures); err != nil {
		return err
	}
	if err := verifyRestrictedFeatures(c, modelRestrictedFeatures...); err != nil {
		return err
	}
	return verifyDisabledCapabilities(c, b)
}

func isolationPath(path string, directory bool) (string, error) {
	if path == "" || !filepath.IsAbs(path) || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n*?[]{}") {
		return "", fmt.Errorf("%w: isolation requires absolute literal paths", ErrIsolationUnavailable)
	}
	if runtime.GOOS == "windows" {
		// Device/UNC namespaces, alternate streams and trailing-dot/space aliases
		// are not supported by this local-drive profile contract.
		if len(path) < 3 || path[1] != ':' || strings.ContainsAny(path[2:], ":") {
			return "", fmt.Errorf("%w: isolation requires native local-drive paths", ErrIsolationUnavailable)
		}
		for _, part := range strings.Split(filepath.ToSlash(path[2:]), "/") {
			if part != "." && part != ".." && (strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ")) {
				return "", fmt.Errorf("%w: unsafe Windows path alias", ErrIsolationUnavailable)
			}
		}
	}
	canonical, err := paths.Canonical(path)
	if err != nil {
		return "", fmt.Errorf("%w: cannot canonicalize isolation path", ErrIsolationUnavailable)
	}
	canonical, err = finalIsolationPath(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve native isolation path aliases", ErrIsolationUnavailable)
	}
	if filepath.Dir(canonical) == canonical {
		return "", fmt.Errorf("%w: volume roots cannot be isolation locations", ErrIsolationUnavailable)
	}
	if directory {
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("%w: isolation directory is missing or inaccessible", ErrIsolationUnavailable)
		}
	}
	return canonical, nil
}

func overlap(a, b string) (bool, error) {
	in, err := paths.Inside(a, b)
	if err != nil || in {
		return in, err
	}
	return paths.Inside(b, a)
}

func prepareIsolation(layout IsolationPaths, reviewer bool) (isolationBoundary, error) {
	b := isolationBoundary{reviewer: reviewer, nonce: rand.Text()}
	var err error
	b.workspace, err = isolationPath(layout.Workspace, true)
	if err != nil {
		return b, err
	}
	b.scratch, err = isolationPath(layout.Scratch, true)
	if err != nil {
		return b, err
	}
	if overlaps, err := overlap(b.workspace, b.scratch); err != nil || overlaps {
		return b, fmt.Errorf("%w: workspace and scratch overlap or cannot be compared", ErrIsolationUnavailable)
	}
	if len(layout.CredentialPaths) == 0 {
		return b, fmt.Errorf("%w: credential locations must be protected", ErrIsolationUnavailable)
	}
	protected := append([]string{layout.MainCheckout, layout.ControllerState}, layout.SiblingWorkspaces...)
	protected = append(protected, layout.CredentialPaths...)
	// The trusted host retains its own authentication; the command sandbox
	// must deny those locations even when the caller lists synthetic credentials.
	for _, name := range []string{"CODEX_HOME", "HOME", "USERPROFILE"} {
		if path := os.Getenv(name); path != "" {
			if name != "CODEX_HOME" {
				path = filepath.Join(path, ".codex")
			}
			protected = append(protected, path)
		}
	}
	gitPaths, err := sharedGitPaths(b.workspace)
	if err != nil {
		return b, err
	}
	protected = append(protected, gitPaths...)
	seen := map[string]bool{}
	for _, path := range protected {
		canonical, err := isolationPath(path, false)
		if err != nil {
			return b, err
		}
		for _, allowed := range []string{b.workspace, b.scratch} {
			if overlaps, err := overlap(allowed, canonical); err != nil || overlaps {
				return b, fmt.Errorf("%w: protected path overlaps workspace/scratch or cannot be compared", ErrIsolationUnavailable)
			}
		}
		key := canonical
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			b.protected = append(b.protected, canonical)
		}
	}
	return b, nil
}

func readGitPointer(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	err = errors.Join(err, f.Close())
	if err != nil || len(data) > 4096 || strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("%w: invalid shared Git pointer", ErrIsolationUnavailable)
	}
	return strings.TrimSpace(string(data)), nil
}

func sharedGitPaths(workspace string) ([]string, error) {
	gitDir := filepath.Join(workspace, ".git")
	info, err := os.Lstat(gitDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // synthetic/non-Git workspace
	}
	if err != nil {
		return nil, fmt.Errorf("%w: cannot inspect workspace Git metadata", ErrIsolationUnavailable)
	}
	if info.Mode().IsRegular() {
		pointer, err := readGitPointer(gitDir)
		if err != nil || !strings.HasPrefix(pointer, "gitdir: ") {
			return nil, fmt.Errorf("%w: invalid workspace Git pointer", ErrIsolationUnavailable)
		}
		gitDir = strings.TrimPrefix(pointer, "gitdir: ")
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(workspace, gitDir)
		}
	}
	gitDir, err = isolationPath(gitDir, true)
	if err != nil {
		return nil, err
	}
	metadata := []string{gitDir}
	commonFile := filepath.Join(gitDir, "commondir")
	if _, err := os.Lstat(commonFile); err == nil {
		common, err := readGitPointer(commonFile)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot inspect shared Git common directory", ErrIsolationUnavailable)
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		common, err = isolationPath(common, true)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, common)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: cannot inspect shared Git common directory", ErrIsolationUnavailable)
	}
	var protected []string
	for _, path := range metadata {
		inside, err := paths.Inside(workspace, path)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot compare shared Git metadata", ErrIsolationUnavailable)
		}
		if !inside {
			protected = append(protected, path)
		}
	}
	return protected, nil
}

func (b isolationBoundary) profile() string {
	if b.reviewer {
		return "orch_reviewer_" + b.nonce
	}
	return "orch_worker_" + b.nonce
}

func quoteTOML(value string) string {
	data, _ := json.Marshal(value) // JSON string escaping is valid TOML basic-string escaping.
	return string(data)
}

func (b isolationBoundary) args() []string {
	access := "write"
	if b.reviewer {
		access = "read"
	}
	entries := []string{`":root"="read"`, quoteTOML(b.workspace) + "=" + quoteTOML(access), quoteTOML(b.scratch) + `="write"`}
	for _, path := range b.protected {
		entries = append(entries, quoteTOML(path)+`="deny"`)
	}
	// A fresh profile name avoids merging inherited extensions/workspace roots
	// into this boundary. These are process-only overrides, never config writes.
	profile := `{filesystem={` + strings.Join(entries, ",") + `},network={enabled=false}}`
	args := []string{"app-server", "--listen", "stdio://", "-c", `windows.sandbox="elevated"`, "-c", "permissions." + b.profile() + "=" + profile,
		"-c", "default_permissions=" + quoteTOML(b.profile()), "-c", `shell_environment_policy={inherit="none",set={TEMP=` + quoteTOML(b.scratch) + `,TMP=` + quoteTOML(b.scratch) + `,TMPDIR=` + quoteTOML(b.scratch) + `}}`,
		"-c", `approval_policy="never"`, "-c", `web_search="disabled"`, "-c", `model_provider="openai"`}
	if b.evaluation {
		args = append(args, "-c", "project_doc_max_bytes=0", "-c", "skills.include_instructions=false", "-c", "include_apps_instructions=false", "-c", "include_collaboration_mode_instructions=false", "-c", "include_environment_context=false")
		for _, feature := range evaluationRestrictedFeatures {
			args = append(args, "-c", "features."+feature+"=false")
		}
	}
	for _, feature := range append(append([]string(nil), restrictedFeatures...), modelRestrictedFeatures...) {
		args = append(args, "-c", "features."+feature+"=false")
	}
	return args
}

func serverEnvironment(environ []string, scratch string) []string {
	var env []string
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(name) {
		case "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "USERPROFILE", "HOME", "LOCALAPPDATA", "APPDATA", "CODEX_HOME":
			env = append(env, entry) // trusted host resolves its own existing authentication
		}
	}
	return append(env, "TEMP="+scratch, "TMP="+scratch, "TMPDIR="+scratch)
}

func (b isolationBoundary) commandEnvironment() map[string]*string {
	env := map[string]*string{}
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			env[strings.ToUpper(name)] = nil
		}
	}
	for _, name := range []string{"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATH", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = &value
		}
	}
	for _, name := range []string{"TEMP", "TMP", "TMPDIR"} {
		value := b.scratch
		env[name] = &value
	}
	for _, name := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "CODEX_HOME", "OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN"} {
		env[name] = nil
	}
	return env
}

func openIsolation(ctx context.Context, options Options, b isolationBoundary) (c *connection, capabilities IsolationCapabilities, err error) {
	if strings.TrimSpace(options.ClientVersion) == "" {
		return nil, capabilities, errors.New("codex isolation requires an Orch client version")
	}
	dir, err := isolationPath(options.Dir, true)
	if err != nil || !strings.EqualFold(dir, b.workspace) {
		return nil, capabilities, fmt.Errorf("%w: app-server cwd must equal the approved workspace", ErrIsolationUnavailable)
	}
	if options.Executable == "" {
		options.Executable = "codex"
	}
	// Discover the merged server names without executing commands, then disable
	// each one explicitly on a fresh connection. An empty table is only a merge.
	discoveryCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	discovery, capabilities, err := startIsolation(discoveryCtx, options, b, b.args())
	if err != nil {
		return nil, capabilities, err
	}
	config, readErr := readIsolationConfig(discovery, b.workspace)
	err = errors.Join(readErr, discovery.close(), discovery.contextError("configuration discovery"))
	if err != nil {
		return nil, capabilities, err
	}
	servers, err := config.serverNames()
	if err != nil {
		return nil, capabilities, err
	}
	c, capabilities, err = startIsolation(ctx, options, b, disableMCP(b.args(), servers))
	if err != nil {
		return nil, capabilities, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.close(), c.contextError("isolation initialization"))
			c = nil
		}
	}()
	config, err = readIsolationConfig(c, b.workspace)
	if err != nil {
		return c, capabilities, err
	}
	if err = config.verify(b, servers); err != nil {
		return c, capabilities, err
	}
	if err = verifyRestrictedFeatures(c); err != nil {
		return c, capabilities, err
	}
	capabilities.ToolsDisabled = true
	capabilities.DisabledMCP = len(servers)
	var readiness struct {
		Status string `json:"status"`
	}
	if err = c.call("windowsSandbox/readiness", struct{}{}, &readiness); err != nil {
		return c, capabilities, err
	}
	if readiness.Status != "ready" {
		if readiness.Status == "" {
			return c, capabilities, fmt.Errorf("%w: windowsSandbox/readiness status missing", ErrIsolationUnavailable)
		}
		return c, capabilities, fmt.Errorf("%w: elevated Windows sandbox is not ready; operator setup is required", ErrIsolationUnavailable)
	}
	capabilities.SandboxReady = true
	var profiles struct {
		Data []struct {
			ID      string `json:"id"`
			Allowed *bool  `json:"allowed"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err = c.call("permissionProfile/list", map[string]any{"cwd": b.workspace}, &profiles); err != nil {
		return c, capabilities, err
	}
	if profiles.Data == nil || profiles.NextCursor != nil {
		return c, capabilities, fmt.Errorf("%w: incomplete native permission profile evidence", ErrIsolationUnavailable)
	}
	found := false
	for _, profile := range profiles.Data {
		if profile.ID == b.profile() {
			if found || profile.Allowed == nil || !*profile.Allowed {
				return c, capabilities, fmt.Errorf("%w: native permission profile denied or ambiguous", ErrIsolationUnavailable)
			}
			found = true
		}
	}
	if !found {
		return c, capabilities, fmt.Errorf("%w: native permission profile missing", ErrIsolationUnavailable)
	}
	capabilities.ProfileAllowed = true
	c.diagnosticReady = true
	c.disabledMCP = servers
	return c, capabilities, nil
}

func startIsolation(ctx context.Context, options Options, b isolationBoundary, args []string) (c *connection, capabilities IsolationCapabilities, err error) {
	c, err = startWithEnv(ctx, execx.Cmd{Name: options.Executable, Args: args, Dir: b.workspace}, serverEnvironment(os.Environ(), b.scratch))
	if err != nil {
		return nil, capabilities, err
	}
	c.isolation, c.profile = true, b.profile()
	defer func() {
		if err != nil {
			err = errors.Join(err, c.close(), c.contextError("isolation initialization"))
			c = nil
		}
	}()
	var initialize struct {
		UserAgent string `json:"userAgent"`
	}
	params := map[string]any{"clientInfo": map[string]string{"name": "orch", "version": options.ClientVersion}, "capabilities": map[string]bool{"experimentalApi": true}}
	if err = c.call("initialize", params, &initialize); err != nil {
		return c, capabilities, err
	}
	version := hostVersion.FindStringSubmatch(initialize.UserAgent)
	if len(version) != 2 {
		return c, capabilities, fmt.Errorf("%w: native host version unavailable", ErrIsolationUnavailable)
	}
	capabilities.HostVersion = version[1] // evidence, never an eligibility allowlist
	err = c.initialized()
	return c, capabilities, err
}

// diagnosticCommand is private: only no-model synthetic tests call it.
// Metadata/diagnostic connections cannot start turns or unsandboxed processes.
func (c *connection) diagnosticCommand(b isolationBoundary, argv []string, cancelByTimeout bool) (int, error) {
	if !c.diagnosticReady || c.profile != b.profile() || c.cmd.Dir != b.workspace {
		return 0, fmt.Errorf("%w: diagnostic command requires its verified connection and boundary", ErrIsolationUnavailable)
	}
	timeout := 10000
	if cancelByTimeout {
		timeout = 1000
	}
	var result struct {
		ExitCode *int    `json:"exitCode"`
		Stdout   *string `json:"stdout"`
		Stderr   *string `json:"stderr"`
	}
	if err := c.call("command/exec", map[string]any{"command": argv, "cwd": b.workspace, "permissionProfile": b.profile(), "timeoutMs": timeout, "env": b.commandEnvironment()}, &result); err != nil {
		return 0, err
	}
	if result.ExitCode == nil || result.Stdout == nil || result.Stderr == nil {
		return 0, fmt.Errorf("%w: incomplete diagnostic command evidence", ErrMalformedMessage)
	}
	// Native default capture and the client's message bound apply. Output is
	// discarded, never logged. Custom capture/streaming is unsupported on Windows.
	return *result.ExitCode, nil
}
