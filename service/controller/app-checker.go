package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver"
	"github.com/giantswarm/apiextensions/pkg/apis/application/v1alpha1"
	"github.com/giantswarm/backoff"
	"github.com/giantswarm/k8sclient/v3/pkg/k8sclient"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/giantswarm/app-checker/service/controller/key"
)

type AppCheckerConfig struct {
	K8sClient k8sclient.Interface
	Logger    micrologger.Logger

	Environment      string
	GithubOAuthToken string
	WaitDuration     int
}

type AppChecker struct {
	k8sClient k8sclient.Interface
	logger    micrologger.Logger

	environment    string
	githubClient   *github.Client
	lastReconciled map[string]string
	waitDuration   int
}

func NewAppChecker(config AppCheckerConfig) (*AppChecker, error) {
	if config.K8sClient == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.K8sClient must not be empty", config)
	}
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Logger must not be empty", config)
	}

	if config.Environment == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.Environment must not be empty", config)
	}
	if config.GithubOAuthToken == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.GithubOAuthToken must not be empty", config)
	}
	if config.WaitDuration == 0 {
		config.WaitDuration = 5
	}

	var client *github.Client
	{
		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: config.GithubOAuthToken},
		)
		tc := oauth2.NewClient(ctx, ts)

		client = github.NewClient(tc)
	}

	c := &AppChecker{
		k8sClient: config.K8sClient,
		logger:    config.Logger,

		environment:    config.Environment,
		githubClient:   client,
		lastReconciled: map[string]string{},
		waitDuration:   config.WaitDuration,
	}

	return c, nil
}

func (c AppChecker) Boot(ctx context.Context) {

	var lastResourceVersion string
	{
		// Found the hightest resourceVersion in app CRs
		apps, err := c.k8sClient.G8sClient().ApplicationV1alpha1().Apps("giantswarm").List(metav1.ListOptions{})
		if err != nil {
			panic(err)
		}

		for _, app := range apps.Items {
			if lastResourceVersion < app.ResourceVersion {
				lastResourceVersion = app.ResourceVersion
			}
		}
	}

	c.logger.Log("debug", fmt.Sprintf("starting ResourceVersion is %s", lastResourceVersion))

	for {
		options := metav1.ListOptions{
			LabelSelector:   "!retry_reconciliation",
			ResourceVersion: lastResourceVersion,
		}
		res, err := c.k8sClient.G8sClient().ApplicationV1alpha1().Apps("giantswarm").Watch(options)
		if err != nil {
			panic(err)
		}

		for r := range res.ResultChan() {
			cr, err := key.ToCustomResource(r.Object)
			if err != nil {
				panic(err)
			}

			err = c.reconcile(ctx, cr, r.Type)
			if err != nil {
				c.logger.Log("level", "info", "message", "failed to reconcile an app CR", "stack", fmt.Sprintf("%#v", err))
			}

			lastResourceVersion = cr.ResourceVersion
		}
		c.logger.Log("debug", "watch channel had been closed...")
	}
}

func (c AppChecker) createDeployment(ctx context.Context, cr v1alpha1.App) (*github.Deployment, error) {
	version, err := parseVersion(key.Version(cr))
	if err != nil {
		return nil, microerror.Mask(err)
	}

	c.logger.Log("debug", fmt.Sprintf("creating a github deployment in %#q environment for %#q app", c.environment, cr.Name))

	autoMerge := false
	requiredContext := []string{}
	request := github.DeploymentRequest{
		AutoMerge:        &autoMerge,
		RequiredContexts: &requiredContext,
		Environment:      &c.environment,
		Ref:              &version,
	}
	deploy, _, err := c.githubClient.Repositories.CreateDeployment(ctx, "giantswarm", key.AppName(cr), &request)
	if err != nil {
		return nil, microerror.Mask(err)
	}
	c.logger.Log("debug", "created a github deployment")

	return deploy, nil
}

func (c AppChecker) createDeploymentStatus(ctx context.Context, cr v1alpha1.App, deploymentID int64, status string) error {
	c.logger.Log("debug", fmt.Sprintf("creating a deployment status %#q in %#q environment with status as %#q", cr.Name, c.environment, status))

	request := github.DeploymentStatusRequest{
		State: &status,
	}

	_, _, err := c.githubClient.Repositories.CreateDeploymentStatus(ctx, "giantswarm", key.AppName(cr), deploymentID, &request)
	if err != nil {
		return microerror.Mask(err)
	}

	c.logger.Log("debug", "created a deployment status")

	return nil
}

func (c AppChecker) reconcile(ctx context.Context, cr v1alpha1.App, eventType watch.EventType) error {
	var deploy *github.Deployment
	var err error

	if lastResourceVersion, ok := c.lastReconciled[cr.Name]; ok {
		if lastResourceVersion >= cr.ResourceVersion {
			c.logger.Log("debug", fmt.Sprintf("reconciled from the last %#q app", cr.Name))
			return nil
		}
	}

	switch eventType {
	case watch.Added, watch.Modified:
		// check the existing github deployments first
		if deploy, err = c.findDeployment(ctx, cr); err != nil {
			return microerror.Mask(err)
		} else if deploy == nil {
			// create a new deployment
			deploy, err = c.createDeployment(ctx, cr)
			if err != nil {
				return microerror.Mask(err)
			}
		}
		err = c.waitUntilAppDeployed(ctx, cr, *deploy.ID)
		if err != nil {
			return microerror.Mask(err)
		}

		err = c.deleteLabels(cr)
		if err != nil {
			return microerror.Mask(err)
		}

	case watch.Deleted:
		if deploy, err = c.findDeployment(ctx, cr); err != nil {
			return microerror.Mask(err)
		}
		if deploy == nil {
			// no-op
			return nil
		}

		err = c.createDeploymentStatus(ctx, cr, *deploy.ID, "inactive")
		if err != nil {
			return microerror.Mask(err)
		}

		delete(c.lastReconciled, cr.Name)

	default:
		c.logger.Log("debug", fmt.Sprintf("event type %s for %#q app is not supported", eventType, cr.Name))
		return nil
	}

	return nil
}

func (c AppChecker) deleteLabels(cr v1alpha1.App) error {
	app, err := c.k8sClient.G8sClient().ApplicationV1alpha1().Apps(cr.Namespace).Get(cr.Name, metav1.GetOptions{})
	if err != nil {
		return microerror.Mask(err)
	}

	if v := app.GetLabels()["retry_reconciliation"]; v == "" {
		// no-op
		return nil
	}

	patches := []patch{
		{
			Op:   "remove",
			Path: "/metadata/labels/retry_reconciliation",
		},
	}

	bytes, err := json.Marshal(patches)
	if err != nil {
		return microerror.Mask(err)
	}

	app, err = c.k8sClient.G8sClient().ApplicationV1alpha1().Apps(cr.Namespace).Patch(cr.Name, types.JSONPatchType, bytes)
	if err != nil {
		return microerror.Mask(err)
	}

	c.lastReconciled[cr.Name] = app.ResourceVersion

	return nil
}

func (c AppChecker) findDeployment(ctx context.Context, cr v1alpha1.App) (*github.Deployment, error) {
	version, err := parseVersion(key.Version(cr))
	if err != nil {
		return nil, microerror.Mask(err)
	}

	c.logger.Log("debug", fmt.Sprintf("finding %#q deployment in %#q environment", cr.Name, c.environment))

	option := github.DeploymentsListOptions{
		Environment: c.environment,
		Ref:         version,
	}
	deploys, _, err := c.githubClient.Repositories.ListDeployments(ctx, "giantswarm", key.AppName(cr), &option)
	if err != nil {
		c.logger.Log("debug", fmt.Sprintf("no repository named %#q or others exceptions occured", cr.Name), "message", err.Error())
		return nil, microerror.Mask(err)
	}

	if len(deploys) == 0 {
		c.logger.Log("debug", "no app deployment found")
		return nil, nil
	}

	c.logger.Log("debug", "found a deployment")
	return deploys[0], nil
}

func (c AppChecker) refreshApp(cr v1alpha1.App) error {
	var patches []patch

	app, err := c.k8sClient.G8sClient().ApplicationV1alpha1().Apps(cr.Namespace).Get(cr.Name, metav1.GetOptions{})
	if err != nil {
		return microerror.Mask(err)
	}

	if v := app.GetLabels()["retry_reconciliation"]; v == "" {
		patches = append(patches, patch{
			Op:    "add",
			Path:  "/metadata/labels/retry_reconciliation",
			Value: strconv.Itoa(1),
		})
	} else {
		next, err := strconv.Atoi(v)
		if err != nil {
			return microerror.Mask(err)
		}

		patches = append(patches, patch{
			Op:    "replace",
			Path:  "/metadata/labels/retry_reconciliation",
			Value: strconv.Itoa(next + 1),
		})
	}

	bytes, err := json.Marshal(patches)
	if err != nil {
		return microerror.Mask(err)
	}

	_, err = c.k8sClient.G8sClient().ApplicationV1alpha1().Apps(cr.Namespace).Patch(cr.Name, types.JSONPatchType, bytes)
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}

func (c AppChecker) waitUntilAppDeployed(ctx context.Context, cr v1alpha1.App, deploymentID int64) error {
	o := func() error {
		app, err := c.k8sClient.G8sClient().ApplicationV1alpha1().Apps(cr.Namespace).Get(cr.Name, metav1.GetOptions{})
		if err != nil {
			return backoff.Permanent(microerror.Mask(err))
		}
		status := strings.ToLower(app.Status.Release.Status)
		if status == "deployed" && app.Status.Version == app.Spec.Version {
			// no-op
			return nil
		}

		// update the label as retrying-from-app-checker=true and increment the fake number
		err = c.refreshApp(cr)
		if err != nil {
			return microerror.Mask(err)
		}

		err = c.createDeploymentStatus(ctx, cr, deploymentID, "pending")
		if err != nil {
			return backoff.Permanent(microerror.Mask(err))
		}

		return microerror.Maskf(waitError, "app %#q in %#v status but expected have status %#v", cr.Name, app.Status.Release.Status, "deployed")
	}
	b := backoff.NewConstant(time.Duration(c.waitDuration)*time.Minute, 10*time.Second)

	n := func(err error, t time.Duration) {
		c.logger.Log("level", "debug", "message", fmt.Sprintf("could not get `deployed` status from %#q app", cr.Name))
	}

	err := backoff.RetryNotify(o, b, n)
	if err != nil {
		err = c.createDeploymentStatus(ctx, cr, deploymentID, "failure")
		if err != nil {
			return microerror.Mask(err)
		}
		return nil
	}

	err = c.createDeploymentStatus(ctx, cr, deploymentID, "success")
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}

func parseVersion(version string) (string, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return "", microerror.Mask(err)
	}

	if v.Prerelease() != "" {
		return v.Prerelease(), nil
	}

	return fmt.Sprintf("v%s", v.String()), nil
}

//func isDevBranch(version string) (bool, error) {
//	v, err := semver.NewVersion(version)
//	if err != nil {
//		return false, microerror.Mask(err)
//	}
//
//	// hash number is longer than 10 chars
//	if len(v.Prerelease()) < 10 {
//		return false, nil
//	}
//
//	return true, nil
//}
