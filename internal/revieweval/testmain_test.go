package revieweval

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// launchEnv and launchTempRoot are this test binary's environment and temp
// root as it was launched, before TestMain or gittest.Run changed
// anything: the operator's own ambient environment, which the eval passes
// through and does not scrub. A variable present later but absent from
// launchEnv, or holding a different value, was introduced by the test
// harness, and the live path would hand it to a session unless
// dispatchEnv drops it. Both are taken first thing in TestMain: harness
// setup belongs after them, never in an init() or a package-level
// initializer, or it would pass for the operator's launch environment and
// go unchecked.
var (
	launchEnv      []string
	launchTempRoot string
)

func TestMain(m *testing.M) {
	launchEnv = os.Environ()
	launchTempRoot = os.TempDir()
	tempRootSpellings = spellingsOf(launchTempRoot)
	os.Exit(gittest.Run(m))
}
