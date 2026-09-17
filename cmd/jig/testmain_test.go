package main

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

func TestMain(m *testing.M) {
	// See internal/verifydeliver/testmain_test.go: pins identity so any test
	// that drives a gate/publish/solve command through a fresh pool lease
	// still makes deterministic commits.
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}
