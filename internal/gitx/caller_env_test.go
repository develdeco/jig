package gitx

import (
	"slices"
	"strings"
	"testing"
)

func TestNoCallerWithoutATrace(t *testing.T) {
	for _, env := range [][]string{
		{"A=1", "GIT_TRACE2=1", "B=2"},
		{"GIT_TRACE2_EVENT=", "A=1"},
		{"GIT_TRACE2_EVENT=/x", "GIT_TRACE2_EVENT="},
		nil,
	} {
		if got := withCaller(slices.Clone(env), "."); !slices.Equal(got, env) {
			t.Errorf("withCaller(%v) = %v; the environment must be unchanged", env, got)
		}
	}
}

func TestCallerVariablesAreAddedWithATrace(t *testing.T) {
	got := withCaller([]string{"GIT_TRACE2_EVENT=/x"}, "")
	if !slices.Contains(got, "JIG_GIT_DIR=") {
		t.Errorf("empty dir should give an empty JIG_GIT_DIR: %v", got)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "JIG_GIT_CALLER=") }) {
		t.Errorf("no JIG_GIT_CALLER: %v", got)
	}
}
