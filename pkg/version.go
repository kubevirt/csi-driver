package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the version of the project, semver
	Version    = "v0.1.0"
	// GitCommit is the git commit at the time of the build
	GitCommit  = "unknown"
	// GoVersion is information about the go version at the time of the build
	GoVersion  = fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
)
