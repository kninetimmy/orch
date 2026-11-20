package run

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/paths"
)

// DeliveryTarget is what the adapter shows and echoes with approval. Neither
// its checkout nor its GitHub selector can select a destination for the engine.
type DeliveryTarget struct {
	Checkout   string           `json:"checkout"`
	CheckoutID string           `json:"checkout_id"`
	GitHub     ghops.RepoTarget `json:"github"`
}

func (t DeliveryTarget) valid() bool {
	return filepath.IsAbs(t.Checkout) && t.CheckoutID != "" && t.GitHub.Valid()
}

func deliveryCheckout(ctx context.Context, env Env) (*gitops.Git, string, error) {
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, "", err
	}
	if err := git.RequirePrimary(ctx); err != nil {
		return nil, "", err
	}
	common, err := git.CommonDir(ctx)
	if err != nil {
		return nil, "", err
	}
	id, err := checkoutIdentity(git.Root(), common)
	return git, id, err
}

// Bind the directory and the clone's Git metadata, not branch/HEAD or dirty
// contents. In particular a new clone at the same pathname is not the same
// checkout. This is observation, not an atomic filesystem reservation.
func checkoutIdentity(root, common string) (string, error) {
	if !filepath.IsAbs(common) {
		return "", fmt.Errorf("cannot verify Git common directory for Delivery")
	}
	rootID, err := paths.DirectoryID(root)
	if err != nil {
		return "", err
	}
	commonID, err := paths.DirectoryID(common)
	if err != nil {
		return "", err
	}
	return contentDigest([2]string{rootID, commonID})
}

func deliveryGitHub(ctx context.Context, env Env) (*ghops.GH, ghops.Repo, ghops.RepoTarget, error) {
	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, ghops.Repo{}, ghops.RepoTarget{}, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return nil, ghops.Repo{}, ghops.RepoTarget{}, err
	}
	target, err := repo.Target()
	return gh, repo, target, err
}
