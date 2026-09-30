package codexnative

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/manifest"
)

// Options identifies the installed host and the calling Orch build. It cannot
// inject credentials, provider settings, a shell command or model-turn args.
type Options struct {
	Executable    string // empty resolves codex on PATH
	Dir           string // required absolute cwd
	ClientVersion string
}

// Capabilities is catalog/authentication evidence, never an observed execution
// profile, entitlement proof, sandbox proof or a metrics.Observation.
type Capabilities struct {
	HostVersion    string
	PlatformFamily string
	PlatformOS     string
	Selection      manifest.Selection
}

var hostVersion = regexp.MustCompile(`^(?:codex_cli_rs|codex-cli|Codex Desktop)/([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)(?:\s|$)`)

// Preflight starts a fresh local app-server, inspects native metadata, and shuts
// it down. It never attempts login, authentication refresh or model work.
func Preflight(ctx context.Context, options Options, selection manifest.Selection) (capabilities Capabilities, err error) {
	if strings.TrimSpace(selection.Model) == "" || strings.TrimSpace(selection.Effort) == "" || selection.Variant != "" || selection.NoVariant {
		return capabilities, errors.New("codex preflight requires an exact model and effort selection")
	}
	if strings.TrimSpace(options.ClientVersion) == "" {
		return capabilities, errors.New("codex preflight requires an Orch client version")
	}
	if options.Executable == "" {
		options.Executable = "codex"
	}
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	c, err := start(ctx, execx.Cmd{Name: options.Executable, Args: []string{"app-server", "--listen", "stdio://"}, Dir: options.Dir})
	if err != nil {
		return capabilities, err
	}
	defer func() {
		err = errors.Join(err, c.close(), c.contextError("preflight"))
		if err != nil {
			capabilities.Selection = manifest.Selection{}
		}
	}()
	return inspect(c, options.ClientVersion, selection)
}

func inspect(c *connection, clientVersion string, selection manifest.Selection) (Capabilities, error) {
	var capabilities Capabilities
	var initialize struct {
		UserAgent      string `json:"userAgent"`
		PlatformFamily string `json:"platformFamily"`
		PlatformOS     string `json:"platformOs"`
	}
	params := struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}{}
	params.ClientInfo.Name = "orch"
	params.ClientInfo.Version = clientVersion
	if err := c.call("initialize", params, &initialize); err != nil {
		return capabilities, err
	}
	version := hostVersion.FindStringSubmatch(initialize.UserAgent)
	if len(version) != 2 {
		return capabilities, errors.New("codex preflight: native host version unavailable in initialize")
	}
	capabilities.HostVersion = version[1]
	capabilities.PlatformFamily = initialize.PlatformFamily
	capabilities.PlatformOS = initialize.PlatformOS
	if err := c.initialized(); err != nil {
		return capabilities, err
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
		RequiresOpenAIAuth *bool `json:"requiresOpenaiAuth"`
	}
	if err := c.call("account/read", struct {
		RefreshToken bool `json:"refreshToken"`
	}{false}, &account); err != nil {
		return capabilities, err
	}
	if account.Account == nil || account.Account.Type != "chatgpt" || account.RequiresOpenAIAuth == nil || !*account.RequiresOpenAIAuth {
		return capabilities, errors.New("codex preflight requires a signed-in ChatGPT account and an OpenAI-authenticated provider")
	}
	if err := c.requireManagedAuth(); err != nil {
		return capabilities, err
	}
	listParams := struct {
		Cursor        *string `json:"cursor,omitempty"`
		Limit         int     `json:"limit"`
		IncludeHidden bool    `json:"includeHidden"`
	}{Limit: 100, IncludeHidden: true}
	seenCursors := map[string]bool{}
	found, supported := false, false
	for page := 0; ; page++ {
		if page == 100 {
			return capabilities, errors.New("codex preflight: model/list exceeds 100 pages")
		}
		var models struct {
			Data []struct {
				ID      string `json:"id"`
				Model   string `json:"model"`
				Efforts []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.call("model/list", listParams, &models); err != nil {
			return capabilities, err
		}
		if models.Data == nil {
			return capabilities, fmt.Errorf("%w: model/list data missing", ErrMalformedMessage)
		}
		for _, model := range models.Data {
			if model.ID == "" || model.Model == "" || model.Efforts == nil {
				return capabilities, fmt.Errorf("%w: incomplete model capability", ErrMalformedMessage)
			}
			for _, effort := range model.Efforts {
				if effort.Effort == "" {
					return capabilities, fmt.Errorf("%w: incomplete reasoning effort", ErrMalformedMessage)
				}
			}
			if model.ID != selection.Model && model.Model != selection.Model {
				continue
			}
			if model.ID != selection.Model || model.Model != selection.Model {
				return capabilities, errors.New("codex preflight: catalog substitutes the requested model")
			}
			if found {
				return capabilities, errors.New("codex preflight: duplicate requested model in catalog")
			}
			found = true
			for _, effort := range model.Efforts {
				if effort.Effort == selection.Effort {
					supported = true
				}
			}
		}
		if models.NextCursor == nil {
			break
		}
		cursor := *models.NextCursor
		if cursor == "" || len(cursor) > 1024 || seenCursors[cursor] {
			return capabilities, errors.New("codex preflight: invalid or repeated model/list cursor")
		}
		seenCursors[cursor] = true
		listParams.Cursor = &cursor
	}
	if !found {
		return capabilities, fmt.Errorf("codex preflight: requested model %q is unavailable", selection.Model)
	}
	if !supported {
		return capabilities, fmt.Errorf("codex preflight: requested effort %q is unsupported for %q", selection.Effort, selection.Model)
	}
	capabilities.Selection = selection
	return capabilities, nil
}
