package githubwebhook

type payload struct {
	AppVersion string `json:"appVersion"`
	Chart      string `json:"chart"`
	Namespace  string `json:"namespace"`
	Unique     bool   `json:"unique"`
}
