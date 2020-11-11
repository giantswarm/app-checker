// Package server provides a server implementation to connect network transport
// protocols and service business logic by defining server endpoints.
package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/giantswarm/k8sclient/v5/pkg/k8sclient"
	"github.com/giantswarm/microerror"
	microserver "github.com/giantswarm/microkit/server"
	"github.com/giantswarm/micrologger"
	"github.com/spf13/viper"

	"github.com/giantswarm/app-checker/flag"
	"github.com/giantswarm/app-checker/pkg/project"
	"github.com/giantswarm/app-checker/server/endpoint"
	"github.com/giantswarm/app-checker/service"
)

type Config struct {
	Flag      *flag.Flag
	K8sClient k8sclient.Interface
	Logger    micrologger.Logger
	Service   *service.Service

	Viper *viper.Viper
}

func New(config Config) (microserver.Server, error) {
	if config.Flag == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Flag must not be empty", config)
	}
	if config.K8sClient == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.K8sClient must not be empty", config)
	}
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Logger must not be empty", config)
	}
	if config.Viper == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Viper must not be empty", config)
	}

	var err error

	var endpointCollection *endpoint.Endpoint
	{
		c := endpoint.Config{
			K8sClient: config.K8sClient,
			Logger:    config.Logger,
			Service:   config.Service,

			Environment:      config.Viper.GetString(config.Flag.Service.Installation.Environment),
			GithubToken:      config.Viper.GetString(config.Flag.Service.Tokens.GitHubToken),
			WebhookSecretKey: []byte(config.Viper.GetString(config.Flag.Service.Tokens.WebhookSecretKey)),
		}

		endpointCollection, err = endpoint.New(c)
		if err != nil {
			return nil, microerror.Mask(err)
		}
	}

	s := &server{
		logger: config.Logger,

		bootOnce: sync.Once{},
		config: microserver.Config{
			Logger:      config.Logger,
			ServiceName: project.Name(),
			Viper:       config.Viper,

			Endpoints: []microserver.Endpoint{
				endpointCollection.GithubWebhook,
				endpointCollection.Healthz,
				endpointCollection.Version,
			},
			ErrorEncoder: encodeError,
		},
		shutdownOnce: sync.Once{},
	}

	return s, nil
}

type server struct {
	logger micrologger.Logger

	bootOnce     sync.Once
	config       microserver.Config
	shutdownOnce sync.Once
}

func (s *server) Boot() {
	s.bootOnce.Do(func() {
		// Here goes your custom boot logic for your server/endpoint if
		// any.
	})
}

func (s *server) Config() microserver.Config {
	return s.config
}

func (s *server) Shutdown() {
	s.shutdownOnce.Do(func() {
		// Here goes your custom shutdown logic for your
		// server/endpoint if any.
	})
}

func encodeError(ctx context.Context, err error, w http.ResponseWriter) {
	rErr := err.(microserver.ResponseError)
	uErr := rErr.Underlying()

	rErr.SetCode(microserver.CodeInternalError)
	rErr.SetMessage(uErr.Error())
	w.WriteHeader(http.StatusInternalServerError)
}
