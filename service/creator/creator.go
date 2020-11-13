package creator

import (
	"context"
	"fmt"
	"time"

	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
)

type CreatorConfig struct {
	Logger micrologger.Logger

	GithubToken      string
	WebhookSecretKey string
	WebhookURL       string
}

type Creator struct {
	logger micrologger.Logger

	githubClient     *github.Client
	webhookSecretKey string
	webhookURL       string
}

func NewCreator(config CreatorConfig) (*Creator, error) {
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Logger must not be empty", config)
	}

	if config.GithubToken == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.GithubToken must not be empty", config)
	}
	if config.WebhookSecretKey == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.WebhookSecretKey must not be empty", config)
	}
	if config.WebhookURL == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.WebhookURL must not be empty", config)
	}

	var githubClient *github.Client
	{
		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: config.GithubToken},
		)
		tc := oauth2.NewClient(ctx, ts)

		githubClient = github.NewClient(tc)
	}

	c := &Creator{
		logger: config.Logger,

		githubClient:     githubClient,
		webhookSecretKey: config.WebhookSecretKey,
		webhookURL:       config.WebhookURL,
	}

	return c, nil
}

func (c *Creator) Boot(ctx context.Context) {
	for range time.Tick(10 * time.Minute) {
		installed, err := c.checkWebhook(ctx)
		if err != nil {
			c.logger.LogCtx(ctx, "level", "debug", "message", "could not check webhook status", "stack", fmt.Sprintf("%#v", err))
		}

		if !installed {
			c.logger.LogCtx(ctx, "level", "debug", "message", "create webhook", "stack", fmt.Sprintf("%#v", err))

			err := c.createWebhook(ctx)
			if err != nil {
				c.logger.LogCtx(ctx, "level", "debug", "message", "count not create webhook", "stack", fmt.Sprintf("%#v", err))
			}

			c.logger.LogCtx(ctx, "level", "debug", "message", "created webhook", "stack", fmt.Sprintf("%#v", err))
		}
	}
}
