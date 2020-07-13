package service

import (
	"github.com/giantswarm/operatorkit/flag/service/kubernetes"

	"github.com/giantswarm/app-checker/flag/service/appchecker"
)

// Service is an intermediate data structure for command line configuration flags.
type Service struct {
	AppChecker appchecker.AppChecker
	Kubernetes kubernetes.Kubernetes
}
