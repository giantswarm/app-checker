package githubwebhook

type payload struct {
	AppVersion string `json:"appVersion"`
	Namespace  string `json:"namespace"`
	Unique     bool   `json:"unique"`
}
