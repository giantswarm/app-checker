// Package service implements business logic to create Kubernetes resources
// against the Kubernetes API.
package service

import (
	"context"
	"sync"

	"github.com/giantswarm/microendpoint/service/version"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"
	"github.com/spf13/viper"

	"github.com/giantswarm/app-checker/flag"
	"github.com/giantswarm/app-checker/pkg/project"
	"github.com/giantswarm/app-checker/service/creator"
)

// Config represents the configuration used to create a new service.
type Config struct {
	Logger micrologger.Logger

	Flag  *flag.Flag
	Viper *viper.Viper
}

type Service struct {
	Version *version.Service

	bootOnce       sync.Once
	webhookCreator *creator.Creator
}

// New creates a new configured service object.
func New(config Config) (*Service, error) {
	if config.Flag == nil {
		return nil, microerror.Maskf(invalidConfigError, "config.Flag must not be empty")
	}
	if config.Viper == nil {
		return nil, microerror.Maskf(invalidConfigError, "config.Viper must not be empty")
	}

	// Dependencies.
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "logger must not be empty")
	}

	var err error

	var versionService *version.Service
	{
		c := version.Config{
			Description: project.Description(),
			GitCommit:   project.GitSHA(),
			Name:        project.Name(),
			Source:      project.Source(),
			Version:     project.Version(),
		}

		versionService, err = version.New(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	var webhookCreator *creator.Creator
	{
		c := creator.CreatorConfig{
			Logger: config.Logger,

			GithubToken:      config.Viper.GetString(config.Flag.Service.Github.GitHubToken),
			WebhookSecretKey: []byte(config.Viper.GetString(config.Flag.Service.Github.WebhookSecretKey)),
			WebhookURL:       config.Viper.GetString(config.Flag.Service.Installation.WebhookBaseURL),
		}

		webhookCreator, err = creator.NewCreator(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	s := &Service{
		Version: versionService,

		bootOnce:       sync.Once{},
		webhookCreator: webhookCreator,
	}

	return s, nil
}

func (s *Service) Boot(ctx context.Context) {
	s.bootOnce.Do(func() {
		s.webhookCreator.Boot(ctx)
	})
}
