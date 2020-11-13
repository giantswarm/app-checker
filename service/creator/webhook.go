package creator

import (
	"context"

	"github.com/giantswarm/microerror"
	"github.com/google/go-github/v32/github"
)

func (c *Creator) createWebhook(ctx context.Context) error {
	hook := github.Hook{
		Config: map[string]interface{}{
			"url":          c.webhookURL,
			"content_type": "json",
			"secret":       c.webhookSecretKey,
		},
		Events: []string{"deployment"},
	}

	_, _, err := c.githubClient.Organizations.CreateHook(ctx, "giantswarm", &hook)
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}

func (c *Creator) checkWebhook(ctx context.Context) (bool, error) {
	hooks, _, err := c.githubClient.Organizations.ListHooks(ctx, "giantswarm", &github.ListOptions{})
	if err != nil {
		return false, microerror.Mask(err)
	}

	for _, hook := range hooks {
		if *hook.URL == c.webhookURL {
			return true, nil
		}
	}

	return false, nil
}
