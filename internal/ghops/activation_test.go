package ghops

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

var activationTarget = RepoTarget{Host: "github.enterprise.invalid", Owner: "old", Repository: "name", ID: "R_approved"}

func activationCall(reply string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"api", "graphql", "--hostname", "github.enterprise.invalid",
			"--method", "POST", "--header", "Content-Type: application/json", "--input", "-"},
		Env: ghTestEnv, Stdout: reply,
	}
}

func TestActivationRepoLabelPages(t *testing.T) {
	page := func(after *string, reply string) execxtest.Call {
		call := activationCall(reply)
		call.CheckStdin = func(t *testing.T, stdin string) {
			t.Helper()
			var req struct {
				Query     string
				Variables struct {
					RepositoryID string
					After        *string
				}
			}
			if err := json.Unmarshal([]byte(stdin), &req); err != nil || req.Variables.RepositoryID != activationTarget.ID ||
				!strings.Contains(req.Query, "node(id: $repositoryId)") || !strings.Contains(req.Query, "... on Repository") ||
				(after == nil) != (req.Variables.After == nil) || after != nil && *req.Variables.After != *after {
				t.Fatalf("label lookup did not keep its ID and cursor: %s (%v)", stdin, err)
			}
		}
		return call
	}
	cursor := "page-2"
	first := page(nil, `{"data":{"node":{"id":"R_approved","labels":{"nodes":[{"id":"LA_docs","name":"Docs, Site"}],"pageInfo":{"hasNextPage":true,"endCursor":"page-2"}}}}}`)
	for _, tc := range []struct{ name, second, errorText string }{
		{"complete", `{"data":{"node":{"id":"R_approved","labels":{"nodes":[{"id":"LA_core","name":"Core"}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`, ""},
		{"replacement page", `{"data":{"node":{"id":"R_replacement","labels":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`, "did not return a label page"},
		{"repeated cursor", `{"data":{"node":{"id":"R_approved","labels":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"page-2"}}}}}`, "did not advance its cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The explicit API hostname may not be the ambient default.
			t.Setenv("GH_HOST", "github.com")
			g, script := openScripted(t, tempRoot(t), first, page(&cursor, tc.second))
			repo, err := g.ForTarget(activationTarget)
			if err != nil {
				t.Fatal(err)
			}
			missing, err := repo.MissingLabels(t.Context(), []string{"core", "docs, site", "Missing"})
			script.AssertExhausted()
			if tc.errorText == "" {
				if err != nil || len(missing) != 1 || missing[0] != "Missing" {
					t.Fatalf("missing=%q err=%v; want only Missing", missing, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("err = %v, want %q", err, tc.errorText)
			}
		})
	}
}

func TestActivationRepoIssueReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, id, url, errorText string
	}{
		{"renamed", "R_approved", "https://github.enterprise.invalid/new/name/issues/71", ""},
		{"replacement ID", "R_replacement", "https://github.enterprise.invalid/old/name/issues/71", "did not confirm issue creation"},
		{"different host", "R_approved", "https://github.com/new/name/issues/71", "invalid created issue URL"},
		{"wrong number", "R_approved", "https://github.enterprise.invalid/new/name/issues/72", "inconsistent created issue number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, script := openScripted(t, tempRoot(t), activationCall(fmt.Sprintf(
				`{"data":{"createIssue":{"issue":{"number":71,"url":%q,"repository":{"id":%q}}}}}`, tc.url, tc.id)))
			repo, err := g.ForTarget(activationTarget)
			if err != nil {
				t.Fatal(err)
			}
			labels := validLabels()
			names, err := labels.flatten()
			if err != nil {
				t.Fatal(err)
			}
			repo.labelIDs = make(map[string]string)
			for _, name := range names {
				repo.labelIDs[name] = "LA_" + name
			}
			number, url, err := repo.CreateIssue(t.Context(), "issue", "body", labels)
			script.AssertExhausted()
			if tc.errorText == "" {
				if err != nil || number != 71 || url != tc.url {
					t.Fatalf("number=%d url=%q err=%v", number, url, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.errorText) || number != 0 || url != "" {
				t.Fatalf("number=%d url=%q err=%v; want only error %q", number, url, err, tc.errorText)
			}
		})
	}
}
