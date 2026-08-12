// Package version carries the build version stamped at release time.
package version

// version is overridden via -ldflags at release builds.
var version = "v0.0.0-dev"

// String returns the build version.
func String() string { return version }
