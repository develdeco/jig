package revieweval

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// launchEnv is this test binary's environment as it was launched, before
// TestMain or gittest.Run changed anything: the operator's own ambient
// environment. A variable present now but absent from it, or holding a
// different value, was introduced by the test harness, and the live path
// would hand it to a session unless dispatchEnv drops it.
var launchEnv []string

func TestMain(m *testing.M) {
	launchEnv = os.Environ()
	os.Exit(gittest.Run(m))
}
