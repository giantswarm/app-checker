package service

import (
	"github.com/giantswarm/operatorkit/flag/service/kubernetes"

	"github.com/giantswarm/app-checker/flag/service/github"
	"github.com/giantswarm/app-checker/flag/service/installation"
)

// Service is an intermediate data structure for command line configuration flags.
type Service struct {
	Installation installation.Installation
	Kubernetes   kubernetes.Kubernetes
	Github       github.Github
}
