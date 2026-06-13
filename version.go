package main

import (
	"fmt"
	"runtime"
)

// Version information for homer-data-generator.
var (
	// VERSION_APPLICATION is the application version.
	VERSION_APPLICATION = "0.1.0"

	// BuildDate is the build date (UTC, set via -ldflags).
	BuildDate = ""

	// BuildTime is the build time (UTC, set via -ldflags).
	BuildTime = ""

	// GitCommit is the git commit hash (set via -ldflags).
	GitCommit = ""

	// GoVersion is the Go toolchain used to build.
	GoVersion = runtime.Version()

	// BuildOS is the target operating system.
	BuildOS = runtime.GOOS

	// BuildArch is the target architecture.
	BuildArch = runtime.GOARCH
)

// GetVersionString returns a formatted version string.
func GetVersionString() string {
	s := fmt.Sprintf("homer-data-generator %s", VERSION_APPLICATION)
	if BuildDate != "" {
		s += fmt.Sprintf("\nbuilt %s %s", BuildDate, BuildTime)
	}
	if GitCommit != "" {
		s += fmt.Sprintf(", commit %s", GitCommit)
	}
	s += fmt.Sprintf(", go %s, %s/%s", GoVersion, BuildOS, BuildArch)
	return s
}

// PrintVersion prints version information to stdout.
func PrintVersion() {
	fmt.Println(GetVersionString())
}
