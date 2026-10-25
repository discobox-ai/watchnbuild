// Package version exposes the release version or the VCS revision of a build.
package version

import "runtime/debug"

// Version is set by release builds through ldflags.
var Version = ""

// String returns the release version, VCS revision, or dev for unversioned builds.
func String() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "dev"
}
