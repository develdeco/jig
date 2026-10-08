package mirror

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
