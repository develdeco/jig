package fixture

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}
