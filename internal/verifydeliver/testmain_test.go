package verifydeliver

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	// Publish's commits (reconcile, memorize, squash) now take the
	// operator's own git identity instead of a pinned one; this package's
	// pool-lease clones carry no repo-level identity config of their own, so
	// pin it process-wide here to keep those commits deterministic.
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
