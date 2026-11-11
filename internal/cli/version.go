package cli

import (
	"runtime/debug"
	"strings"
)

const defaultVersion = "0.1.0-dev"

// When the binary was built without -ldflags (for example by
// "go install ...@latest"), Go still records the module version and the git
// revision inside the binary. Use them so "healthcli version" tells the truth.
// Values set with -ldflags win, because they change Version before this runs.
func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		applyBuildInfo(info)
	}
}

func applyBuildInfo(info *debug.BuildInfo) {
	if Version != defaultVersion {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		Version = strings.TrimPrefix(v, "v")
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && Commit == "none" && s.Value != "":
			Commit = s.Value
			if len(Commit) > 7 {
				Commit = Commit[:7]
			}
		case s.Key == "vcs.time" && Date == "unknown" && s.Value != "":
			Date = s.Value
		}
	}
}
