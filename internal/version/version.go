// Package version provides the build version shared by both executables.
package version

// Version is the full Git tag, injected with -ldflags "-X f95rpc/internal/version.Version=v1.2.3".
// Plain go builds stay unversioned rather than claiming a release version.
var Version = "dev"
