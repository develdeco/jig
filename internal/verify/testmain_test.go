package verify

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	// The tests commit in the fixture's clone and in leases they make, so the
	// identity is pinned for the whole binary (see internal/verifydeliver).
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
