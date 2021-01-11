module github.com/giantswarm/app-checker

go 1.14

require (
	github.com/Masterminds/semver/v3 v3.1.1
	github.com/giantswarm/apiextensions/v3 v3.14.1
	github.com/giantswarm/app/v3 v3.7.0
	github.com/giantswarm/k8sclient/v5 v5.0.0
	github.com/giantswarm/microendpoint v0.2.0
	github.com/giantswarm/microerror v0.3.0
	github.com/giantswarm/microkit v0.2.2
	github.com/giantswarm/micrologger v0.4.0
	github.com/giantswarm/operatorkit v1.2.0
	github.com/go-kit/kit v0.10.0
	github.com/google/go-github/v32 v32.1.0
	github.com/spf13/viper v1.7.1
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	k8s.io/apimachinery v0.18.9
	k8s.io/client-go v0.18.9
	k8s.io/kubernetes v1.13.0
)

// Use fork of CAPI with Kubernetes 1.18 support.
replace sigs.k8s.io/cluster-api => github.com/giantswarm/cluster-api v0.3.10-gs
