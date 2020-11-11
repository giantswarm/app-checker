package githubwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/giantswarm/apiextensions/v3/pkg/apis/application/v1alpha1"
	"github.com/giantswarm/app/v3/pkg/app"
	"github.com/giantswarm/backoff"
	"github.com/giantswarm/k8sclient/v5/pkg/k8sclient"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"
	kitendpoint "github.com/go-kit/kit/endpoint"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Method is the HTTP method this endpoint is register for.
	Method = "POST"
	// Name identifies the endpoint. It is aligned to the package path.
	Name = "app/deployer"
	// Path is the HTTP request path this endpoint is registered for.
	Path = "/"
)

type Config struct {
	K8sClient k8sclient.Interface
	Logger    micrologger.Logger

	Env              string
	GithubToken      string
	WebhookSecretKey []byte
}

type Endpoint struct {
	k8sClient k8sclient.Interface
	logger    micrologger.Logger

	env              string
	githubClient     *github.Client
	webhookSecretKey []byte
	waitDuration     time.Duration
}

func New(config Config) (*Endpoint, error) {
	if config.K8sClient == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.K8sClient must not be empty", config)
	}
	if config.Logger == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.Logger must not be empty", config)
	}

	if config.Env == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.Env must not be empty", config)
	}
	if config.GithubToken == "" {
		return nil, microerror.Maskf(invalidConfigError, "%T.GithubToken must not be empty", config)
	}
	if config.WebhookSecretKey == nil {
		return nil, microerror.Maskf(invalidConfigError, "%T.WebhookSecretKey must not be empty", config)
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

	e := &Endpoint{
		k8sClient: config.K8sClient,
		logger:    config.Logger,

		env:          config.Env,
		githubClient: githubClient,
		waitDuration: 1 * time.Minute,
	}

	return e, nil
}

func (e Endpoint) Decoder() kithttp.DecodeRequestFunc {
	return func(ctx context.Context, r *http.Request) (interface{}, error) {
		payload, err := github.ValidatePayload(r, e.webhookSecretKey)
		if err != nil {
			return nil, microerror.Mask(err)
		}

		event, err := github.ParseWebHook(github.WebHookType(r), payload)
		if err != nil {
			return nil, microerror.Mask(err)
		}

		return event, nil
	}
}

func (e Endpoint) Encoder() kithttp.EncodeResponseFunc {
	return func(ctx context.Context, w http.ResponseWriter, response interface{}) error {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		return json.NewEncoder(w).Encode(response)
	}
}

func (e Endpoint) Endpoint() kitendpoint.Endpoint {
	return func(ctx context.Context, r interface{}) (interface{}, error) {
		switch event := r.(type) {
		case *github.DeploymentEvent:
			if *event.Deployment.Environment == e.env {
				err := e.processDeploymentEvent(ctx, event)
				if err != nil {
					return nil, microerror.Mask(err)
				}
			}
		default:
			// no-op
		}
		return nil, nil
	}
}

func (e *Endpoint) processDeploymentEvent(ctx context.Context, event *github.DeploymentEvent) error {

	var payload map[string]interface{}
	{
		err := json.Unmarshal(event.Deployment.Payload, &payload)
		if err != nil {
			return microerror.Mask(err)
		}
	}

	var appCRName string
	{
		rawUnique, ok := payload["unique"]
		if !ok {
			return microerror.Maskf(decodeFailedError, "not found field `unique` in payload")
		}

		unique := rawUnique.(bool)
		if unique {
			appCRName = fmt.Sprintf("%s-%s", *event.Repo.Name, "unique")
		} else {
			appCRName = fmt.Sprintf("%s-%s", *event.Repo.Name, *event.Deployment.Ref)
		}
	}

	var appVersion string
	{
		rawAppVersion, ok := payload["appVersion"]
		if !ok {
			return microerror.Maskf(decodeFailedError, "not found field `appVersion` in payload")
		}

		appVersion = rawAppVersion.(string)
	}

	var namespace string
	{
		rawNamespace, ok := payload["namespace"]
		if !ok {
			return microerror.Maskf(decodeFailedError, "not found field `namespace` in payload")
		}

		namespace = rawNamespace.(string)
	}

	var catalog string
	{
		v, err := semver.NewVersion(appVersion)
		if err != nil {
			return microerror.Mask(err)
		}

		if v.Prerelease() == "" {
			catalog = "control-plane-catalog"
		} else {
			catalog = "control-plane-test-catalog"
		}

		if *event.Repo.Name == "releases" {
			catalog = "releases-catalog"
		}
	}

	appConfig := app.Config{
		AppName:             *event.Repo.Name,
		AppNamespace:        namespace,
		AppCatalog:          catalog,
		AppVersion:          appVersion,
		DisableForceUpgrade: true,
		Name:                appCRName,
	}

	desiredAppCR := app.NewCR(appConfig)

	// Find matching app CR.
	currentApp, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(namespace).Get(ctx, appCRName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(namespace).Create(ctx, desiredAppCR, metav1.CreateOptions{})
		if err != nil {
			return microerror.Mask(err)
		}
	} else if err != nil {
		return microerror.Mask(err)
	}

	lastDeployed := time.Time{}

	// if it exist, update app CR.
	if !equals(currentApp, desiredAppCR) {
		desiredAppCR.ObjectMeta.ResourceVersion = currentApp.GetResourceVersion()
		updateAppCR, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(namespace).Update(ctx, desiredAppCR, metav1.UpdateOptions{})
		if err != nil {
			return microerror.Mask(err)
		}
		lastDeployed = updateAppCR.Status.Release.LastDeployed.Time
	}

	// waiting for status update.
	// meanwhile, creating deployment status event.
	o := func() error {
		currentApp, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(namespace).Get(ctx, appCRName, metav1.GetOptions{})
		if err != nil {
			return backoff.Permanent(microerror.Mask(err))
		}

		status := strings.ToLower(currentApp.Status.Release.Status)
		if status == "deployed" {
			if currentApp.Status.Release.LastDeployed.After(lastDeployed) {
				return microerror.Maskf(waitError, "app had been not deployed after %#q, current deployment time: %#q", lastDeployed, currentApp.Status.Release.LastDeployed)
			}

			err = e.updateDeploymentStatus(ctx, event, "success", "")
			if err != nil {
				return microerror.Mask(err)
			}
		} else if status == "not installed" || status == "failed" {
			err = e.updateDeploymentStatus(ctx, event, "failure", currentApp.Status.Release.Reason)
			if err != nil {
				return microerror.Mask(err)
			}

			return backoff.Permanent(microerror.Maskf(executionFailedError, "deployment failed (status: %#q, reason: %#q)", currentApp.Status.Release.Status, currentApp.Status.Release.Reason))
		} else {
			err = e.updateDeploymentStatus(ctx, event, "pending", currentApp.Status.Release.Reason)
			if err != nil {
				return microerror.Mask(err)
			}

			return microerror.Maskf(waitError, "app not deployed status:  %#q", currentApp.Status.Release.Status)
		}

		return nil
	}

	b := backoff.NewConstant(e.waitDuration, 5*time.Second)

	n := func(err error, t time.Duration) {
		e.logger.LogCtx(ctx, "level", "debug", "message", fmt.Sprintf("could not get `deployed` status from %#q app", appCRName), "stack", fmt.Sprintf("%#v", err))
	}

	err = backoff.RetryNotify(o, b, n)
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}

func (e *Endpoint) updateDeploymentStatus(ctx context.Context, event *github.DeploymentEvent, status, reason string) error {
	request := github.DeploymentStatusRequest{
		State:       &status,
		Description: &reason,
		Environment: &e.env,
	}

	_, _, err := e.githubClient.Repositories.CreateDeploymentStatus(ctx, *event.Repo.Owner.Login, event.Repo.GetName(), *event.Deployment.ID, &request)
	if err != nil {
		return microerror.Mask(err)
	}

	return nil
}

func (e Endpoint) Method() string {
	return Method
}

func (e Endpoint) Middlewares() []kitendpoint.Middleware {
	return []kitendpoint.Middleware{}
}

func (e Endpoint) Name() string {
	return Name
}

func (e Endpoint) Path() string {
	return Path
}

// equals asseses the equality of ReleaseStates with regards to distinguishing fields.
func equals(current, desired *v1alpha1.App) bool {
	if current.Name != desired.Name {
		return false
	}
	if !reflect.DeepEqual(current.Spec, desired.Spec) {
		return false
	}
	if !reflect.DeepEqual(current.Labels, desired.Labels) {
		return false
	}

	return true
}
