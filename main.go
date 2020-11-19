package main

import (
	"context"

	applicationv1alpha1 "github.com/giantswarm/apiextensions/v3/pkg/apis/application/v1alpha1"
	"github.com/giantswarm/k8sclient/v5/pkg/k8sclient"
	"github.com/giantswarm/k8sclient/v5/pkg/k8srestconfig"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/microkit/command"
	microserver "github.com/giantswarm/microkit/server"
	"github.com/giantswarm/micrologger"
	"github.com/spf13/viper"
	"k8s.io/client-go/rest"

	"github.com/giantswarm/app-checker/flag"
	"github.com/giantswarm/app-checker/pkg/project"
	"github.com/giantswarm/app-checker/server"
	"github.com/giantswarm/app-checker/service"
)

var (
	f = flag.New()
)

func main() {
	err := mainE(context.Background())
	if err != nil {
		panic(microerror.JSON(err))
	}
}

func mainE(ctx context.Context) error {
	var err error

	// Create a new logger that is used by all packages.
	var newLogger micrologger.Logger
	{
		c := micrologger.Config{}

		newLogger, err = micrologger.New(c)
		if err != nil {
			return microerror.Mask(err)
		}
	}

	// We define a server factory to create the custom server once all command
	// line flags are parsed and all microservice configuration is storted out.
	serverFactory := func(v *viper.Viper) microserver.Server {
		var restConfig *rest.Config
		{
			c := k8srestconfig.Config{
				Logger: newLogger,

				Address:    v.GetString(f.Service.Kubernetes.Address),
				InCluster:  v.GetBool(f.Service.Kubernetes.InCluster),
				KubeConfig: v.GetString(f.Service.Kubernetes.KubeConfig),
				TLS: k8srestconfig.ConfigTLS{
					CAFile:  v.GetString(f.Service.Kubernetes.TLS.CAFile),
					CrtFile: v.GetString(f.Service.Kubernetes.TLS.CrtFile),
					KeyFile: v.GetString(f.Service.Kubernetes.TLS.KeyFile),
				},
			}

			restConfig, err = k8srestconfig.New(c)
			if err != nil {
				panic(err)
			}
		}

		var k8sClient k8sclient.Interface
		{
			c := k8sclient.ClientsConfig{
				Logger: newLogger,
				SchemeBuilder: k8sclient.SchemeBuilder{
					applicationv1alpha1.AddToScheme,
				},

				RestConfig: restConfig,
			}

			k8sClient, err = k8sclient.NewClients(c)
			if err != nil {
				panic(err)
			}
		}

		var newService *service.Service
		{
			c := service.Config{
				Logger: newLogger,

				Flag:  f,
				Viper: v,
			}

			newService, err = service.New(c)
			if err != nil {
				panic(microerror.JSON(err))
			}

		}

		// Create a new custom server which bundles our endpoints.
		var newServer microserver.Server
		{
			c := server.Config{
				Flag:      f,
				K8sClient: k8sClient,
				Logger:    newLogger,
				Service:   newService,

				Viper: v,
			}

			newServer, err = server.New(c)
			if err != nil {
				panic(microerror.JSON(err))
			}
		}

		return newServer
	}

	// Create a new microkit command which manages our custom microservice.
	var newCommand command.Command
	{
		c := command.Config{
			Logger:        newLogger,
			ServerFactory: serverFactory,

			Description: project.Description(),
			GitCommit:   project.GitSHA(),
			Name:        project.Name(),
			Source:      project.Source(),
			Version:     project.Version(),
		}

		newCommand, err = command.New(c)
		if err != nil {
			return microerror.Mask(err)
		}
	}

	daemonCommand := newCommand.DaemonCommand().CobraCommand()

	daemonCommand.PersistentFlags().String(f.Service.Github.GitHubToken, "", "OAuth token for authenticating against GitHub. Needs 'repo_deployment' scope.\"")
	daemonCommand.PersistentFlags().String(f.Service.Github.WebhookSecretKey, "", "Secret key to decrypt webhook payload.\"")
	daemonCommand.PersistentFlags().String(f.Service.Installation.Environment, "", "Environment name that app-checker is running in.")
	daemonCommand.PersistentFlags().String(f.Service.Installation.WebhookBaseURL, "", "Webhook address that this operator listening to.")

	daemonCommand.PersistentFlags().String(f.Service.Kubernetes.Address, "http://127.0.0.1:6443", "Address used to connect to Kubernetes. When empty in-cluster config is created.")
	daemonCommand.PersistentFlags().Bool(f.Service.Kubernetes.InCluster, false, "Whether to use the in-cluster config to authenticate with Kubernetes.")
	daemonCommand.PersistentFlags().String(f.Service.Kubernetes.KubeConfig, "", "KubeConfig used to connect to Kubernetes. When empty other settings are used.")
	daemonCommand.PersistentFlags().String(f.Service.Kubernetes.TLS.CAFile, "", "Certificate authority file path to use to authenticate with Kubernetes.")
	daemonCommand.PersistentFlags().String(f.Service.Kubernetes.TLS.CrtFile, "", "Certificate file path to use to authenticate with Kubernetes.")
	daemonCommand.PersistentFlags().String(f.Service.Kubernetes.TLS.KeyFile, "", "Key file path to use to authenticate with Kubernetes.")

	err = newCommand.CobraCommand().Execute()
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}
