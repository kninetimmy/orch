package ghops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ActivationRepo operates only on a verified repository node on its GitHub
// host. It does not expose GH's checkout-relative commands or retain the
// mutable owner/name. A name can be reassigned even after a successful check.
type ActivationRepo struct {
	gh       *GH
	host, id string
	labelIDs map[string]string // Prepared by EnsureLabelTaxonomy for this activation only.
}

// ForTarget binds the activation operations to a verified Repo.Target without
// changing the original handle's checkout-relative behavior.
func (g *GH) ForTarget(target RepoTarget) (*ActivationRepo, error) {
	if !target.Valid() {
		return nil, fmt.Errorf("%w: invalid explicit GitHub repository target", ErrNoGitHubRepo)
	}
	return &ActivationRepo{gh: g, host: target.Host, id: target.ID}, nil
}

// gh api selects authentication and the GraphQL endpoint by --hostname. All
// variables (including long issue bodies) are JSON over stdin, not gh field
// flags or owner/repo placeholders. Never retry via a name-based API.
func (r *ActivationRepo) graphql(ctx context.Context, operation, query string, variables map[string]any, out any) error {
	where := "GitHub " + operation + " on " + r.host
	request, err := json.Marshal(map[string]any{
		"operationName": operation, "query": query, "variables": variables,
	})
	if err != nil {
		return fmt.Errorf("%s: encode GraphQL request: %w", where, err)
	}
	stdout, err := r.gh.ghStdin(ctx, bytes.NewReader(request), "api", "graphql", "--hostname", r.host,
		"--method", "POST", "--header", "Content-Type: application/json", "--input", "-")
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	var response struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return fmt.Errorf("%s: decode GraphQL response: %w", where, err)
	}
	// Reject partial data as well as a complete error, even on a zero exit.
	if len(response.Errors) != 0 {
		return fmt.Errorf("%s: GraphQL error: %q", where, response.Errors[0].Message)
	}
	if len(response.Data) == 0 || bytes.Equal(response.Data, []byte("null")) {
		return fmt.Errorf("%s: missing GraphQL data", where)
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return fmt.Errorf("%s: decode GraphQL data: %w", where, err)
	}
	return nil
}

type activationLabel struct{ ID, Name string }

const activationLabelsQuery = `query OrchActivationLabels($repositoryId: ID!, $after: String) {
  node(id: $repositoryId) {
    ... on Repository {
      id
      labels(first: 100, after: $after) {
        nodes { id name }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

func (r *ActivationRepo) readLabelIDs(ctx context.Context) (map[string]string, error) {
	present := map[string]string{}
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	var after *string
	for {
		var data struct {
			Node struct {
				ID     string
				Labels struct {
					Nodes    []activationLabel
					PageInfo struct {
						HasNextPage *bool
						EndCursor   string
					}
				}
			}
		}
		err := r.graphql(ctx, "OrchActivationLabels", activationLabelsQuery,
			map[string]any{"repositoryId": r.id, "after": after}, &data)
		if err != nil {
			return nil, err
		}
		page := data.Node.Labels
		if data.Node.ID != r.id || page.Nodes == nil || page.PageInfo.HasNextPage == nil {
			return nil, fmt.Errorf("GitHub label lookup did not return a label page for repository ID %q on %s", r.id, r.host)
		}
		for _, label := range page.Nodes {
			name := strings.ToLower(label.Name)
			if name == "" || !githubNodeID.MatchString(label.ID) || present[name] != "" || seenIDs[label.ID] {
				return nil, fmt.Errorf("GitHub label lookup for repository ID %q on %s returned an invalid or duplicate label %q", r.id, r.host, label.Name)
			}
			present[name], seenIDs[label.ID] = label.ID, true
		}
		if !*page.PageInfo.HasNextPage {
			return present, nil
		}
		cursor := page.PageInfo.EndCursor
		if cursor == "" || seenCursors[cursor] {
			return nil, fmt.Errorf("GitHub label lookup for repository ID %q on %s did not advance its cursor", r.id, r.host)
		}
		seenCursors[cursor], after = true, &cursor
	}
}

// MissingLabels checks plan-declared area labels on the verified repository.
func (r *ActivationRepo) MissingLabels(ctx context.Context, names []string) ([]string, error) {
	present, err := r.readLabelIDs(ctx)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, name := range names {
		if present[strings.ToLower(name)] == "" {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

const createLabelMutation = `mutation OrchCreateLabel($input: CreateLabelInput!) {
  createLabel(input: $input) { label { id name } }
}`

// EnsureLabelTaxonomy creates only missing taxonomy labels in order and
// retains label node IDs from this repository for subsequent issue creation.
// The inventory is a snapshot, not a lock against concurrent label edits.
func (r *ActivationRepo) EnsureLabelTaxonomy(ctx context.Context) error {
	present, err := r.readLabelIDs(ctx)
	if err != nil {
		return err
	}
	for _, def := range taxonomy {
		if present[def.name] != "" {
			continue
		}
		var data struct {
			CreateLabel struct{ Label activationLabel }
		}
		input := map[string]string{"repositoryId": r.id, "name": def.name, "color": def.color, "description": def.description}
		if err := r.graphql(ctx, "OrchCreateLabel", createLabelMutation, map[string]any{"input": input}, &data); err != nil {
			return fmt.Errorf("create taxonomy label %q: %w", def.name, err)
		}
		label := data.CreateLabel.Label
		if !strings.EqualFold(label.Name, def.name) || !githubNodeID.MatchString(label.ID) {
			return fmt.Errorf("GitHub did not confirm creation of taxonomy label %q on %s", def.name, r.host)
		}
		for name, id := range present {
			if id == label.ID {
				return fmt.Errorf("GitHub returned the same node ID for taxonomy label %q and label %q on %s", def.name, name, r.host)
			}
		}
		present[def.name] = label.ID
	}
	r.labelIDs = present
	return nil
}

const createIssueMutation = `mutation OrchCreateIssue($input: CreateIssueInput!) {
  createIssue(input: $input) { issue { number url repository { id } } }
}`

// CreateIssue sends the repository ID and label IDs in one mutation. It never
// resolves a repository or label by name on another GitHub handle.
func (r *ActivationRepo) CreateIssue(ctx context.Context, title, body string, labels Labels, forbiddenAreas ...string) (int, string, error) {
	names, err := labels.flatten(forbiddenAreas...)
	if err != nil {
		return 0, "", err
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id := r.labelIDs[strings.ToLower(name)]
		if id == "" {
			return 0, "", fmt.Errorf("no node ID for label %q from repository ID %q on %s", name, r.id, r.host)
		}
		ids = append(ids, id)
	}
	var data struct {
		CreateIssue struct {
			Issue struct {
				Number     int
				URL        string
				Repository struct{ ID string }
			}
		}
	}
	input := map[string]any{"repositoryId": r.id, "title": title, "body": body, "labelIds": ids}
	if err := r.graphql(ctx, "OrchCreateIssue", createIssueMutation, map[string]any{"input": input}, &data); err != nil {
		return 0, "", err
	}
	issue := data.CreateIssue.Issue
	if issue.Repository.ID != r.id {
		return 0, "", fmt.Errorf("GitHub did not confirm issue creation in repository ID %q on %s", r.id, r.host)
	}
	u, err := url.Parse(issue.URL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, r.host) || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		strings.Count(strings.TrimSuffix(u.Path, "/"), "/") != 4 {
		return 0, "", fmt.Errorf("GitHub returned an invalid created issue URL on %s", r.host)
	}
	if n, err := parseNumberFromURL(u.Path, "issues"); err != nil || n != issue.Number {
		return 0, "", fmt.Errorf("GitHub returned inconsistent created issue number and URL on %s", r.host)
	}
	return issue.Number, issue.URL, nil
}

const activationFixQuery = `query OrchActivationFix($repositoryId: ID!, $number: Int!) {
  node(id: $repositoryId) {
    ... on Repository { id pullRequest(number: $number) { number state } }
  }
}`

// PullRequestState resolves a plan's fixes reference within the verified
// repository, never within another repository that happens to have that number.
func (r *ActivationRepo) PullRequestState(ctx context.Context, number int) (string, error) {
	var data struct {
		Node struct {
			ID          string
			PullRequest struct {
				Number int
				State  string
			}
		}
	}
	if err := r.graphql(ctx, "OrchActivationFix", activationFixQuery,
		map[string]any{"repositoryId": r.id, "number": number}, &data); err != nil {
		return "", err
	}
	pr := data.Node.PullRequest
	if data.Node.ID != r.id || pr.Number != number || pr.State == "" {
		return "", fmt.Errorf("GitHub did not return PR #%d from repository ID %q on %s", number, r.id, r.host)
	}
	return pr.State, nil
}
