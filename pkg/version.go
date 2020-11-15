package version

import (
	"fmt"
	"runtime"
)

var (
	Version    = "v0.1.0"
	GitVersion = "unknown"
	GitCommit  = "unknown"
	GoVersion  = fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
)
