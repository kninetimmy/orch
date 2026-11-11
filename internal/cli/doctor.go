package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/adapters/claude"
	"github.com/kninetimmy/orch/adapters/codex"
	opencodeadapter "github.com/kninetimmy/orch/adapters/opencode"
	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/interview"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/metrics"
	opencodecatalog "github.com/kninetimmy/orch/internal/opencode"
	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
)

type adapterSpec struct {
	host         string
	executable   string
	pluginID     string
	manifestJSON string
	repair       string
}

var (
	claudeAdapter = adapterSpec{
		host:         "claude",
		executable:   "claude",
		pluginID:     "orch-claude@orch",
		manifestJSON: claude.PluginManifestJSON,
		repair:       "run `claude plugin marketplace update orch`, then `claude plugin update orch-claude@orch`, then restart Claude Code",
	}
	codexAdapter = adapterSpec{
		host:         "codex",
		executable:   "codex",
		pluginID:     "orch@orch",
		manifestJSON: codex.PluginManifestJSON,
		repair:       "run `codex plugin marketplace upgrade orch`, then restart Codex CLI",
	}
	opencodeAdapter = adapterSpec{
		host:         "opencode",
		executable:   opencodecatalog.Executable,
		pluginID:     "orch.delivery",
		manifestJSON: opencodeadapter.PackageJSON,
		repair:       "install the pinned Orch OpenCode adapter, then restart the OpenCode V2 service",
	}
)

const doctorUsage = "orch doctor: usage: orch doctor [--host <claude|codex|opencode>] [--json]"

// DoctorReport is shared by human CLI output, CLI JSON and MCP. An unhealthy
// report is a successful inspection, not an input or transport error.
type DoctorReport struct {
	SchemaVersion int           `json:"schema_version"`
	Version       string        `json:"version"`
	SelectedHost  string        `json:"selected_host,omitempty"`
	Healthy       bool          `json:"healthy"`
	Checks        []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // success, failure or advisory
	Detail string `json:"detail,omitempty"`
}

func runDoctor(env Env, args []string) error {
	selectedHost, asJSON, err := doctorArgs(args)
	if err != nil {
		return err
	}
	doc, err := doctorReport(env, selectedHost)
	if err != nil {
		return err
	}
	if asJSON {
		if err := json.NewEncoder(env.Stdout).Encode(doc); err != nil {
			return err
		}
	} else {
		printDoctor(env.Stdout, doc)
	}
	if !doc.Healthy {
		return errors.New("one or more checks failed")
	}
	return nil
}

func printDoctor(w io.Writer, doc *DoctorReport) {
	for _, check := range doc.Checks {
		prefix := "ok    "
		switch check.Status {
		case "failure":
			prefix = "FAIL  "
		case "advisory":
			prefix = "note  "
		}
		text := check.Name
		if check.Detail != "" {
			text += ": " + check.Detail
		}
		// Preserve the existing prose-only notes while keeping a stable check
		// name in structured results. Their detail is the complete note.
		if check.Status == "advisory" {
			switch check.Name {
			case "gh repository", "local overrides", "project conventions", "opencode runtime":
				text = check.Detail
			}
		}
		fmt.Fprintln(w, prefix+text)
	}
}

// doctorReport only observes. Repair suggestions are details, never commands
// executed here. All command working directories come from env.RepoRoot.
func doctorReport(env Env, selectedHost string) (*DoctorReport, error) {
	if err := validateDoctorHost(selectedHost); err != nil {
		return nil, err
	}
	doc := &DoctorReport{SchemaVersion: 1, Version: Version, SelectedHost: selectedHost, Healthy: true, Checks: []DoctorCheck{}}
	add := func(name, status, detail string) {
		doc.Checks = append(doc.Checks, DoctorCheck{Name: name, Status: status, Detail: detail})
		if status == "failure" {
			doc.Healthy = false
		}
	}
	note := func(name, detail string) { add(name, "advisory", detail) }
	note("orch version", Version)
	check := func(name string, err error) {
		if err != nil {
			add(name, "failure", err.Error())
			return
		}
		add(name, "success", "")
	}
	checkHost := func(host, name string, err error) {
		if err != nil && selectedHost != "" && host != selectedHost {
			note(name, fmt.Sprintf("%v (advisory; selected host is %s)", err, selectedHost))
			return
		}
		check(name, err)
	}

	_, gitErr := env.LookPath("git")
	check("git on PATH", gitErr)

	if gitErr == nil {
		// gitops.Open also verifies .orchestrator/ sits at the git
		// top level, which the containment guarantees depend on.
		_, repoErr := gitops.Open(context.Background(), env.Runner, env.RepoRoot)
		check("git repository", repoErr)
	}

	_, ghErr := env.LookPath("gh")
	check("gh on PATH", ghErr)

	if ghErr == nil {
		gh, authErr := ghops.Open(context.Background(), env.Runner, env.RepoRoot)
		check("gh authentication", authErr)
		if authErr == nil {
			repo, repoErr := gh.Repo(context.Background())
			switch {
			case errors.Is(repoErr, ghops.ErrNoGitHubRepo):
				// Assist works without a remote (PRD §5); Delivery
				// preflight fails closed on this same probe.
				note("gh repository", fmt.Sprintf("no GitHub repository resolved; Assist works without one, Delivery will fail closed (%v)", repoErr))
			case repoErr != nil:
				check("gh repository", repoErr)
			default:
				add("gh repository", "success", repo.NameWithOwner)
			}

			// Best-effort per task 39: a broken network, missing
			// release, or malformed response degrades to a note and
			// never fails doctor. Skipped entirely on unstamped "dev"
			// builds, which have nothing meaningful to compare.
			if Version != "dev" {
				latest, relErr := ghops.LatestRelease(context.Background(), env.Runner, env.RepoRoot)
				switch {
				case relErr != nil:
					note("release check", relErr.Error())
				case latest != Version:
					note("newer release available", fmt.Sprintf("installed %s, latest %s; re-run the installer to update", Version, latest))
				default:
					add("release", "success", fmt.Sprintf("up to date (%s)", Version))
				}
			}
		}
	}

	cfg, cfgErr := config.Load(env.RepoRoot)
	check("configuration", cfgErr)
	if cfgErr == nil && selectedHost != "" && cfg.Host(selectedHost) == nil {
		return nil, usageError(fmt.Sprintf("orch doctor: host %q is not configured", selectedHost))
	}
	if cfgErr == nil {
		note("protected survey", protectedListStatus(cfg))
	}

	if cfgErr == nil && config.HasLocalOverride(env.RepoRoot) {
		if len(cfg.Overrides) > 0 {
			note("local overrides", fmt.Sprintf("%s applied; overrides: %s", config.LocalOverridePath, strings.Join(cfg.Overrides, ", ")))
		} else {
			note("local overrides", fmt.Sprintf("%s present; no overrides set", config.LocalOverridePath))
		}
	}

	if cfgErr == nil {
		if cfg.Hosts.Claude != nil {
			checkHost("claude", "claude adapter", checkAdapter(env, claudeAdapter))
			// A note, never a failure: an unchecked Claude Code version
			// only silences the grant's relay instruction.
			if version, problem := claudeVersionProblem(env); problem == "" {
				add("claude code version", "success", fmt.Sprintf("%s; the transcript reader recognizes it", version))
			} else {
				note("claude code", problem+"; under an autonomy grant the context-relay instruction will not fire")
			}
		}
		if cfg.Hosts.Codex != nil {
			checkHost("codex", "codex adapter", checkAdapter(env, codexAdapter))
		}
		if cfg.Hosts.OpenCode != nil {
			catalog, err := checkOpenCodeAdapter(env, opencodeAdapter, note)
			checkHost("opencode", "opencode adapter", err)
			if err == nil {
				for _, selection := range opencodecatalog.CheckSelections(cfg.Hosts.OpenCode.Roles, catalog) {
					checkHost("opencode", "opencode "+selection.Role+" selection", selection.Err)
				}
			}
		}
	}

	if cfgErr == nil {
		for _, host := range cfg.EnabledHosts() {
			h := cfg.Host(host)
			stale, staleErr := agents.Stale(env.RepoRoot, host, h)
			switch {
			case len(stale) > 0:
				detail := strings.Join(stale, ", ")
				if staleErr != nil {
					detail += fmt.Sprintf(" (%v)", staleErr)
				}
				checkHost(host, host+" agent files", fmt.Errorf("absent, unreadable, or out of date: %s; run `orch render-agents` to regenerate them", detail))
			case staleErr != nil:
				checkHost(host, host+" agent files", staleErr)
			default:
				checkHost(host, host+" agent files", nil)
			}
		}
	}

	// A root instruction file holding the Orch managed block and
	// nothing else leaves that host's agents with no project
	// conventions at all: Orch writes only its own block, and it
	// authors no repository's conventions of its own. A note, not a
	// failure — a repository with genuinely none to state is not broken.
	//
	// This note used to add that Orch never carries a repository's
	// conventions across from the other host's file either
	// (interview.InstructionFile maps one file per host), which is why
	// the only advice it could give was to add them by hand. That is the
	// before; the after is that `orch configure` offers to repair
	// exactly this state, copying the sibling file's conventions in when
	// that file has any outside its own block, with a human approving
	// the whole resulting file first (interview's seed.go). So the note
	// names that command — reporting a state nothing could repair is
	// what it used to do.
	if cfgErr == nil {
		for _, host := range cfg.EnabledHosts() {
			file := interview.InstructionFile(host)
			// PlanRemove's DeleteWholeFile already answers exactly this
			// question: what is left once the managed region is
			// stripped is otherwise empty. An unreadable file or
			// structurally broken markers are not this check's subject
			// — `orch init`/`configure` already block on those — so
			// they stay silent here rather than borrowing this note.
			//
			// Deliberately still the looser reading of "block-only" than
			// the one seeding will repair (interview's isBlockOnly also
			// requires the block itself to be current): a drifted or
			// newer-versioned block-only file is worth reporting here
			// all the same, and `orch configure` names it as a blocker
			// rather than silently replacing a body this build cannot
			// vouch for.
			ch, err := instructions.PlanRemoveFile(filepath.Join(env.RepoRoot, file))
			if err != nil || !ch.DeleteWholeFile {
				continue
			}
			note("project conventions", fmt.Sprintf("%s holds the Orch managed block and nothing else; %s agents see no project conventions — add them outside the block, or run `orch configure`, which offers to seed them from %s when that file has them", file, host, interview.SiblingInstructionFile(host)))
		}
	}

	// Metrics-ignore trap (only meaningful, and only checked, when the
	// effective configuration has metrics enabled): a repository already
	// in this state gets no chance to self-report it through a failing
	// RequireClean gate until metrics has already written an untracked
	// file, so doctor names it directly.
	if cfgErr == nil && cfg.Metrics.Enabled {
		trapped, trapErr := metrics.TrapActive(cfg.Metrics.Enabled, env.RepoRoot)
		switch {
		case trapErr != nil:
			check("metrics .gitignore", trapErr)
		case trapped:
			check("metrics .gitignore", fmt.Errorf("metrics is enabled but %s is missing from .gitignore; run `orch configure` to add it", metrics.GitignoreLine))
		default:
			check("metrics .gitignore", nil)
		}
	}

	if cfgErr == nil {
		switch cfg.Memhub.Mode {
		case "off":
			note("memhub", "skipped (mode off)")
		default:
			mh := memhub.New(env.Runner, env.RepoRoot)
			mhErr := mh.Probe(context.Background())
			if mhErr == nil {
				// Health succeeded; only now does recall run, mirroring
				// the plan/activate gate's skip-recall-on-health-failure
				// rule (PRD §20): recall against a memhub whose status
				// already failed tells us nothing new.
				mhErr = mh.Recall(context.Background())
			}
			if cfg.Memhub.Mode == "required" {
				check("memhub", mhErr)
			} else if mhErr != nil {
				note("memhub", mhErr.Error())
			} else {
				check("memhub", nil)
			}
		}
	}

	st, stErr := state.Load(env.RepoRoot)
	check("state file", stErr)

	owner, lockErr := lockfile.Inspect(env.RepoRoot)
	check("delivery lock", lockErr)

	if stErr == nil && lockErr == nil {
		check("state/lock consistency", state.CheckConsistent(st, owner))
	}

	if owner != nil {
		if hostname, err := os.Hostname(); err == nil && owner.Hostname == hostname && !lockfile.PIDAlive(owner.PID) {
			note("delivery lock", fmt.Sprintf("acquiring process (pid %d) is no longer running — normal between commands; if no Delivery run is active, run `orch abort`", owner.PID))
		}
	}

	return doc, nil
}

func doctorArgs(args []string) (host string, asJSON bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			if asJSON {
				return "", false, usageError(doctorUsage)
			}
			asJSON = true
		case "--host":
			if host != "" || i+1 == len(args) || args[i+1] == "" {
				return "", false, usageError(doctorUsage)
			}
			i++
			host = args[i]
		default:
			return "", false, usageError(doctorUsage)
		}
	}
	return host, asJSON, validateDoctorHost(host)
}

func validateDoctorHost(host string) error {
	switch host {
	case "", "claude", "codex", "opencode":
		return nil
	default:
		return usageError(fmt.Sprintf("orch doctor: invalid host %q; expected claude, codex, or opencode", host))
	}
}

type adapterPlugin struct {
	ID        string `json:"id"`
	PluginID  string `json:"pluginId"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
}

// checkAdapter independently verifies the enabled host's installed
// plugin. Project agent-file freshness is a separate doctor check.
func checkAdapter(env Env, spec adapterSpec) error {
	fail := func(detail string) error {
		return fmt.Errorf("%s; %s", detail, spec.repair)
	}

	expected, err := adapterVersion(spec.manifestJSON)
	if err != nil {
		return fail(fmt.Sprintf("read shipped %s adapter version: %v", spec.host, err))
	}
	if _, err := env.LookPath(spec.executable); err != nil {
		return fail(fmt.Sprintf("%s not found on PATH (expected adapter version %q); install it or adjust PATH: %v", spec.executable, expected, err))
	}

	res, err := env.Runner.Run(context.Background(), execx.Cmd{
		Name: spec.executable,
		Args: []string{"plugin", "list", "--json"},
		Dir:  env.RepoRoot,
	})
	command := spec.executable + " plugin list --json"
	if err != nil {
		return fail(fmt.Sprintf("run `%s`: %v", command, err))
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = "no error output"
		}
		return fail(fmt.Sprintf("`%s` exited %d: %s", command, res.ExitCode, detail))
	}

	plugins, err := decodeAdapterPlugins(spec.host, []byte(res.Stdout))
	if err != nil {
		return fail(fmt.Sprintf("malformed `%s` output: %v", command, err))
	}

	var matches []adapterPlugin
	for _, plugin := range plugins {
		id := plugin.ID
		if spec.host == "codex" {
			id = plugin.PluginID
		}
		if id == spec.pluginID {
			matches = append(matches, plugin)
		}
	}

	switch len(matches) {
	case 0:
		return fail(fmt.Sprintf("%s is absent (expected version %q)", spec.pluginID, expected))
	case 1:
		// Continue below.
	default:
		var versions []string
		for _, plugin := range matches {
			if plugin.Version != "" {
				versions = append(versions, plugin.Version)
			}
		}
		if len(versions) > 0 {
			return fail(fmt.Sprintf("%s appears %d times (installed versions %q, expected %q); exactly one is required", spec.pluginID, len(matches), versions, expected))
		}
		return fail(fmt.Sprintf("%s appears %d times; exactly one at version %q is required", spec.pluginID, len(matches), expected))
	}

	plugin := matches[0]
	versionDetail := fmt.Sprintf("expected %q", expected)
	if plugin.Version != "" {
		versionDetail = fmt.Sprintf("installed %q, expected %q", plugin.Version, expected)
	}
	if spec.host == "codex" && !plugin.Installed {
		return fail(fmt.Sprintf("%s is marked not installed (%s)", spec.pluginID, versionDetail))
	}
	if !plugin.Enabled {
		return fail(fmt.Sprintf("%s is disabled (%s)", spec.pluginID, versionDetail))
	}
	if plugin.Version == "" {
		return fail(fmt.Sprintf("%s has no version (expected %q)", spec.pluginID, expected))
	}
	if plugin.Version != expected {
		return fmt.Errorf("%s version mismatch: installed %q, expected %q; align the Orch engine and %s adapter to the same release: re-run the Orch installer if the engine is older; if the adapter is older, %s", spec.pluginID, plugin.Version, expected, spec.host, spec.repair)
	}
	return nil
}

func adapterVersion(manifestJSON string) (string, error) {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return "", err
	}
	if manifest.Version == "" {
		return "", errors.New("manifest has no version")
	}
	return manifest.Version, nil
}

const minimumOpenCodeVersion = opencodecatalog.MinimumVersion

// checkOpenCodeAdapter enforces runtime floors, checks the active server plugin
// and live skill contracts, then returns the verified project-scoped model
// catalog for role selection checks. V2's plugin API does not currently expose
// adapter versions.
func checkOpenCodeAdapter(env Env, spec adapterSpec, note func(string, string)) (opencodecatalog.Catalog, error) {
	fail := func(detail string) (opencodecatalog.Catalog, error) {
		return opencodecatalog.Catalog{}, fmt.Errorf("%s; %s", detail, spec.repair)
	}
	runtime, err := opencodecatalog.Resolve(context.Background(), env.Runner, env.RepoRoot)
	if err != nil {
		return fail(err.Error())
	}
	if runtime.Warning != "" {
		note("opencode runtime", runtime.Warning)
	}
	if err := runtime.CheckPlugin(context.Background(), env.Runner, env.RepoRoot, spec.pluginID); err != nil {
		return fail(err.Error())
	}
	skills, err := runtime.ReadSkillCatalog(context.Background(), env.Runner, env.RepoRoot)
	if err != nil {
		return failOpenCodeSkills(err.Error())
	}
	if err := checkOpenCodeSkillContracts(skills); err != nil {
		return failOpenCodeSkills(err.Error())
	}
	catalog, err := runtime.ReadCatalog(context.Background(), env.Runner, env.RepoRoot)
	if err != nil {
		return fail(err.Error())
	}
	return catalog, nil
}

const canonicalOpenCodeSkillSource = "adapters/opencode/skills"

type openCodeSkillContract struct {
	id      string
	markers []string
}

func openCodeSkillContracts() []openCodeSkillContract {
	return []openCodeSkillContract{
		{
			id: "orch-setup",
			markers: []string{
				fmt.Sprintf(`{"schema_version": %d, "answers": {}}`, question.SchemaVersion),
				fmt.Sprintf("Question wire `schema_version` is closed at `%d` for both `AnswerSet` and `Document`", question.SchemaVersion),
			},
		},
		{
			id: "orch-delivery",
			markers: []string{
				fmt.Sprintf("Selection-bearing wire versions are closed: StatusDoc `%d`, GateDoc `%d`, Dispatch `%d`, Escalate `%d`, Review `%d`.",
					run.StatusSchemaVersion, run.GateSchemaVersion, run.DispatchSchemaVersion, run.EscalateSchemaVersion, run.ReviewSchemaVersion),
				"Reject any other `schema_version` before reading or submitting a `Selection`.",
				fmt.Sprintf("`GateDoc` (`schema_version: %d`)", run.GateSchemaVersion),
				fmt.Sprintf("`{\"schema_version\": %d, \"issue_number\": N}`. Result (`DispatchResult`)", run.DispatchSchemaVersion),
				fmt.Sprintf("{\"schema_version\": %d, \"issue_number\": N, \"reviewed_head_oid\": \"...\"", run.ReviewSchemaVersion),
				fmt.Sprintf("{\"schema_version\": %d, \"issue_number\": N, \"trigger\": \"...\", \"detail\": \"...\"}", run.EscalateSchemaVersion),
			},
		},
		{
			id: "orch-architect",
			markers: []string{
				fmt.Sprintf("`orch run status --json` returns `StatusDoc` schema_version `%d`; reject any other before reading its Selection-bearing run state.", run.StatusSchemaVersion),
			},
		},
	}
}

func checkOpenCodeSkillContracts(catalog opencodecatalog.SkillCatalog) error {
	contents := make(map[string]string, len(catalog.Skills))
	for _, skill := range catalog.Skills {
		contents[skill.ID] = strings.Join(strings.Fields(skill.Content), " ")
	}
	for _, contract := range openCodeSkillContracts() {
		content, ok := contents[contract.id]
		if !ok {
			return fmt.Errorf("live OpenCode skill %q is absent", contract.id)
		}
		for _, marker := range contract.markers {
			if !strings.Contains(content, strings.Join(strings.Fields(marker), " ")) {
				return fmt.Errorf("live OpenCode skill %q lacks this Orch build's wire contract", contract.id)
			}
		}
	}
	return nil
}

func failOpenCodeSkills(detail string) (opencodecatalog.Catalog, error) {
	return opencodecatalog.Catalog{}, fmt.Errorf("%s; configure the checked-out `%s` directory as an additive OpenCode skill source, then restart the OpenCode V2 service", detail, canonicalOpenCodeSkillSource)
}

func decodeAdapterPlugins(host string, data []byte) ([]adapterPlugin, error) {
	if host == "claude" {
		var plugins []adapterPlugin
		if err := json.Unmarshal(data, &plugins); err != nil {
			return nil, err
		}
		if plugins == nil {
			return nil, errors.New("expected a top-level array")
		}
		return plugins, nil
	}

	var listing struct {
		Installed []adapterPlugin `json:"installed"`
	}
	if err := json.Unmarshal(data, &listing); err != nil {
		return nil, err
	}
	if listing.Installed == nil {
		return nil, errors.New(`expected an object with an "installed" array`)
	}
	return listing.Installed, nil
}
