package correct

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	// The scope tests commit in clones of their own; the pinned identity
	// makes those commits possible and the same on every run.
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
