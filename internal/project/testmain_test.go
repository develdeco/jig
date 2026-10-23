package project

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// TestMain pins the git identity for the whole test binary: InitStandalone
// commits its scaffold as the person, and the store it creates is a new
// repo with no config of its own, so a test cannot give it one.
func TestMain(m *testing.M) {
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
