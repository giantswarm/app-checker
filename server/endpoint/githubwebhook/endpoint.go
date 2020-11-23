package githubwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/giantswarm/apiextensions/v3/pkg/apis/application/v1alpha1"
	"github.com/giantswarm/app/v3/pkg/app"
	"github.com/giantswarm/app/v3/pkg/key"
	"github.com/giantswarm/k8sclient/v5/pkg/k8sclient"
	"github.com/giantswarm/microerror"
	"github.com/giantswarm/micrologger"
	kitendpoint "github.com/go-kit/kit/endpoint"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	api "k8s.io/kubernetes/pkg/apis/core"
)

const (
	// Method is the HTTP method this endpoint is register for.
	Method = "POST"
	// Name identifies the endpoint. It is aligned to the package path.
	Name = "app/deployer"
	// Path is the HTTP request path this endpoint is registered for.
	Path = "/"

	releases = "releases"
)

var (
	draughtsmanRepositories = map[string]bool{
		"draughtsman":                true,
		"aws-app-collection":         true,
		"azure-app-collection":       true,
		"kvm-app-collection":         true,
		"conformance-app-collection": true,
	}
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
				if draughtsmanRepositories[*event.Repo.Name] {
					e.logger.LogCtx(ctx, "level", "debug", "message", fmt.Sprintf("no need to deploy for draughtsman project %#q", *event.Repo.Name))
					return nil, nil
				}

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
	payload, err := parsePayload(event.Deployment.Payload)
	if err != nil {
		return microerror.Mask(err)
	}

	var appCRName string
	{
		var prefixName string
		{
			if *event.Repo.Name == releases {
				prefixName = payload.Chart
			} else {
				prefixName = *event.Repo.Name
			}
		}

		if payload.Unique {
			appCRName = fmt.Sprintf("%s-%s", prefixName, "unique")
		} else {
			appCRName = fmt.Sprintf("%s-%s", prefixName, *event.Deployment.Ref)
		}
	}

	var catalog string
	{
		v, err := semver.NewVersion(payload.AppVersion)
		if err != nil {
			return microerror.Mask(err)
		}

		if v.Prerelease() == "" {
			catalog = "control-plane-catalog"
		} else {
			catalog = "control-plane-test-catalog"
		}

		if *event.Repo.Name == releases {
			if *event.Deployment.Ref == "master" {
				catalog = releases
			} else {
				catalog = fmt.Sprintf("%s-test", releases)
			}
		}
	}

	appConfig := app.Config{
		AppName:             *event.Repo.Name,
		AppNamespace:        payload.Namespace,
		AppCatalog:          catalog,
		AppVersion:          payload.AppVersion,
		DisableForceUpgrade: true,
		Name:                appCRName,
	}

	if *event.Repo.Name == releases {
		appConfig.AppName = payload.Chart
	}

	desiredAppCR := app.NewCR(appConfig)

	var lastResourceVersion uint64

	// Find matching app CR.
	currentApp, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(payload.Namespace).Get(ctx, appCRName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		newApp, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(payload.Namespace).Create(ctx, desiredAppCR, metav1.CreateOptions{})
		if err != nil {
			return microerror.Mask(err)
		}

		lastResourceVersion, err = getResourceVersion(newApp.GetResourceVersion())
		if err != nil {
			return microerror.Mask(err)
		}
	} else if err != nil {
		return microerror.Mask(err)
	}

	// if app exist already, update app CR.
	if lastResourceVersion == 0 && !equals(currentApp, desiredAppCR) {
		desiredAppCR.ObjectMeta.ResourceVersion = currentApp.GetResourceVersion()

		updateAppCR, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(payload.Namespace).Update(ctx, desiredAppCR, metav1.UpdateOptions{})
		if err != nil {
			return microerror.Mask(err)
		}

		lastResourceVersion, err = getResourceVersion(updateAppCR.GetResourceVersion())
		if err != nil {
			return microerror.Mask(err)
		}
	}

	e.logger.LogCtx(ctx, "level", "debug", "message", fmt.Sprintf("deploying app %#q with version %#q", appCRName, payload.AppVersion))

	timeoutSeconds := int64(30)
	lo := metav1.ListOptions{
		FieldSelector:  fields.OneTermEqualSelector(api.ObjectNameField, appCRName).String(),
		TimeoutSeconds: &timeoutSeconds,
	}

	res, err := e.k8sClient.G8sClient().ApplicationV1alpha1().Apps(payload.Namespace).Watch(ctx, lo)
	if err != nil {
		return microerror.Mask(err)
	}

	// Waiting for status update.
	// meanwhile, creating deployment status event.
	err = e.updateDeploymentStatus(ctx, event, "in_progress", "")
	if err != nil {
		return microerror.Mask(err)
	}

	var status string
	for r := range res.ResultChan() {
		fmt.Println("DEBUG1")
		switch r.Type {
		case watch.Error:
			e.logger.LogCtx(ctx, "level", "info", "message", fmt.Sprintf("got error event: %#q", r.Object))
			return nil

		case watch.Added, watch.Modified:
			cr, err := key.ToApp(r.Object)
			if err != nil {
				return microerror.Mask(err)
			}

			resourceVersion, err := getResourceVersion(cr.GetResourceVersion())
			if err != nil {
				return microerror.Mask(err)
			}

			if resourceVersion <= lastResourceVersion {
				// no-op
				continue
			}

			status = cr.Status.Release.Status
			if status == "deployed" {
				err = e.updateDeploymentStatus(ctx, event, "success", "")
				if err != nil {
					return microerror.Mask(err)
				}
			} else if status == "not-installed" || status == "failed" {
				err = e.updateDeploymentStatus(ctx, event, "failure", currentApp.Status.Release.Reason)
				if err != nil {
					return microerror.Mask(err)
				}
			} else {
				err = e.updateDeploymentStatus(ctx, event, "pending", currentApp.Status.Release.Reason)
				if err != nil {
					return microerror.Mask(err)
				}
			}
		}
		fmt.Println("DEBUG2")
	}
	fmt.Println("DEBUG3")

	if status == "deployed" {
		e.logger.LogCtx(ctx, "level", "debug", "message", fmt.Sprintf("deployed app %#q with version %#q", appCRName, payload.AppVersion))
	} else {
		err = e.updateDeploymentStatus(ctx, event, "failure", "deployment take longer than 30 seconds")
		if err != nil {
			return microerror.Mask(err)
		}
	}

	return nil
}

func (e *Endpoint) updateDeploymentStatus(ctx context.Context, event *github.DeploymentEvent, status, reason string) error {
	if len(reason) >= 140 {
		reason = reason[0:137] + "..."
	}
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

func getResourceVersion(resourceVersion string) (uint64, error) {
	r, err := strconv.ParseUint(resourceVersion, 0, 64)
	if err != nil {
		return 0, microerror.Mask(err)
	}

	return r, nil
}

func parsePayload(rawPayload []byte) (*payload, error) {
	var e payload

	err := json.Unmarshal(rawPayload, &e)
	if err != nil {
		return nil, microerror.Mask(err)
	}

	if e.AppVersion == "" {
		return nil, microerror.Maskf(decodeFailedError, "not found field `appVersion` in payload")
	}
	if e.Namespace == "" {
		return nil, microerror.Maskf(decodeFailedError, "not found field `namespace` in payload")
	}

	return &e, nil
}
