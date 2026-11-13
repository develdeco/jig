package gitx

import (
	"slices"
	"strings"
	"testing"
)

func TestNoCallerWithoutATrace(t *testing.T) {
	env := []string{"A=1", "GIT_TRACE2=1", "B=2"}
	if got := withCaller(slices.Clone(env), "."); !slices.Equal(got, env) {
		t.Errorf("env changed without a trace target: %v", got)
	}
	if got := withCaller(nil, "."); got != nil {
		t.Errorf("nil env became %v", got)
	}

	// Each path builds its child environment through runOnce; neither adds
	// the variables when nothing traces.
	t.Setenv("GIT_TRACE2_EVENT", "")
	for _, kv := range inheritedEnv() {
		if strings.HasPrefix(kv, "JIG_GIT_") {
			t.Errorf("inherited env carries %s", kv)
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
