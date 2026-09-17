package main

import (
	"io"
	"runtime"
	"runtime/debug"

	"github.com/develdeco/jig/internal/axi"
)

// jigVersion is jig's release version. This is the one place it is defined;
// every other reference (help text, the version command) reads it from here.
const jigVersion = "0.1.0"

// cmdVersion implements `jig version`.
func cmdVersion(args []string, stdout io.Writer) int {
	fs := newFlagSet("version")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout,
		axi.KV("version", [][2]string{
			{"version", jigVersion},
			{"commit", buildCommit()},
			{"go", runtime.Version()},
		}),
		axi.Help("Run `jig` to see every command"),
	)
	return 0
}

// buildCommit reads the short vcs revision jig was built from via
// runtime/debug.ReadBuildInfo, appending "+dirty" when the build tree had
// local modifications. It returns "unknown" when build info carries no vcs
// stamp (e.g. `go run`, or a binary built without module/vcs info).
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		return "unknown"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		revision += "+dirty"
	}
	return revision
}
