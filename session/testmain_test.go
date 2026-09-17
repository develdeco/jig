package session

import (
	"os"
	"testing"

	"github.com/develdeco/jig/gittest"
)

func TestMain(m *testing.M) {
	os.Exit(gittest.Run(m))
}
