package main

import (
	"io"
	"runtime"
	"runtime/debug"

	"github.com/develdeco/jig/internal/axi"
)

// cmdVersion implements `jig version`.
func cmdVersion(args []string, stdout io.Writer) int {
	fs := newFlagSet("version")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	info, _ := debug.ReadBuildInfo()
	axi.Render(stdout,
		axi.KV("version", [][2]string{
			{"version", formatVersion(info)},
			{"commit", formatCommit(info)},
			{"go", runtime.Version()},
		}),
		axi.Help("Run `jig` to see every command"),
	)
	return 0
}

// formatVersion derives jig's displayed version from build info. Since
// Go 1.24, `go build` in a VCS checkout stamps the main module's Version:
// the exact tag when HEAD sits exactly at that tag with a clean tree,
// otherwise a pseudo-version, and Go already appends "+dirty" to that
// value itself when the tree carried local modifications. This function
// only reads that value through - it never re-derives or re-appends
// dirty state, so "+dirty" never appears twice.
//
// It falls back to "(devel)" when build info is unavailable or empty;
// -buildvcs=false builds already report "(devel)" themselves.
func formatVersion(info *debug.BuildInfo) string {
	if info == nil || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}

// formatCommit reads the short vcs revision jig was built from. Local
// modifications already surface in the version string (see formatVersion),
// so this reports the bare revision without its own dirty suffix. It
// returns "unknown" when build info is unavailable or carries no vcs
// stamp (e.g. `go run`, -buildvcs=false, or a binary built without
// module/vcs info).
func formatCommit(info *debug.BuildInfo) string {
	if info == nil {
		return "unknown"
	}

	var revision string
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			revision = s.Value
			break
		}
	}
	if revision == "" {
		return "unknown"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	return revision
}
