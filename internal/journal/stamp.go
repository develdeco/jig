package journal

import (
	"runtime/debug"
	"strings"
	"sync"
)

// Stamp is the build stamp of this jig process: its version, its short vcs
// revision and "-dirty" when the build had uncommitted changes, as one string
// (v0.1.3-...+232c016, v0.1.3-...+232c016-dirty), or "unknown" when the
// build carries none. It is computed once per process, and is what every
// line and report jig writes names itself by, and what `jig version` prints.
func Stamp() string {
	stampOnce.Do(func() {
		info, _ := debug.ReadBuildInfo()
		stamp = FormatStamp(info)
	})
	return stamp
}

var (
	stampOnce sync.Once
	stamp     string
)

// FormatStamp derives a build stamp from build info: see Stamp.
func FormatStamp(info *debug.BuildInfo) string {
	if info == nil {
		return "unknown"
	}
	version := strings.TrimSuffix(info.Main.Version, "+dirty")
	dirty := version != info.Main.Version
	var revision string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = dirty || s.Value == "true"
		}
	}
	if version == "(devel)" {
		version = ""
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if version == "" && revision == "" {
		return "unknown"
	}
	out := version
	if revision != "" {
		if out != "" {
			out += "+"
		}
		out += revision
	}
	if dirty {
		out += "-dirty"
	}
	return out
}
