package observe

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	// The tests commit in the fixture clone, so the identity is pinned for the
	// whole binary.
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
