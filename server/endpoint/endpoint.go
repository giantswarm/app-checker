package endpoint

import (
	"github.com/giantswarm/k8sclient/v5/pkg/k8sclient"
	"github.com/giantswarm/microendpoint/endpoint/healthz"
	"github.com/giantswarm/microendpoint/endpoint/version"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"

	"github.com/giantswarm/app-checker/server/endpoint/githubwebhook"
	"github.com/giantswarm/app-checker/service"
)

type Config struct {
	K8sClient k8sclient.Interface
	Logger    micrologger.Logger
	Service   *service.Service

	Environment      string
	GithubToken      string
	WebhookSecretKey []byte
}

type Endpoint struct {
	GithubWebhook *githubwebhook.Endpoint
	Healthz       *healthz.Endpoint
	Version       *version.Endpoint
}

func New(config Config) (*Endpoint, error) {
	if config.K8sClient == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.K8sClient must not be empty", config)
	}
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Logger must not be empty", config)
	}
	if config.Service == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Service or it's Healthz descendents must not be empty", config)
	}

	if config.Environment == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.Environment must not be empty", config)
	}
	if config.GithubToken == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.GithubToken must not be empty", config)
	}
	if config.WebhookSecretKey == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.WebhookSecretKey must not be empty", config)
	}

	var err error

	var githubWebhookEndpoint *githubwebhook.Endpoint
	{
		c := githubwebhook.Config{
			K8sClient: config.K8sClient,
			Logger:    config.Logger,

			Env:              config.Environment,
			GithubToken:      config.GithubToken,
			WebhookSecretKey: config.WebhookSecretKey,
		}

		githubWebhookEndpoint, err = githubwebhook.New(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	var healthzEndpoint *healthz.Endpoint
	{
		c := healthz.Config{
			Logger: config.Logger,
		}

		healthzEndpoint, err = healthz.New(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	var versionEndpoint *version.Endpoint
	{
		c := version.Config{
			Logger:  config.Logger,
			Service: config.Service.Version,
		}

		versionEndpoint, err = version.New(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	e := &Endpoint{
		GithubWebhook: githubWebhookEndpoint,
		Healthz:       healthzEndpoint,
		Version:       versionEndpoint,
	}

	return e, nil
}
