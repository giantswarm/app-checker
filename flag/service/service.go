package service

import (
	"github.com/giantswarm/operatorkit/flag/service/kubernetes"

	"github.com/giantswarm/app-checker/flag/service/installation"
	"github.com/giantswarm/app-checker/flag/service/tokens"
)

// Service is an intermediate data structure for command line configuration flags.
type Service struct {
	Installation installation.Installation
	Kubernetes   kubernetes.Kubernetes
	Tokens       tokens.Tokens
}
