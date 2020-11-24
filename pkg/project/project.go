package project

var (
	description = "The app-checker does something."
	gitSHA      = "n/a"
	name        = "app-checker"
	source      = "https://github.com/giantswarm/app-checker"
	version     = "0.1.1-dev"
)

func Description() string {
	return description
}

func GitSHA() string {
	return gitSHA
}

func Name() string {
	return name
}

func Source() string {
	return source
}

func Version() string {
	return version
}
