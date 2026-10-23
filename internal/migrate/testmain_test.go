package migrate

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// TestMain isolates the tests' git from the host's config: Apply commits as
// the identity git resolves in the store, so a test store carries its own.
func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}
