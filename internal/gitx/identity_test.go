package gitx

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// withoutPinnedIdentity runs "git var" with this process's environment minus
// any author or committer variable, so a host's own variables cannot answer
// for the repo's config, or for its absence. It stands in for ResolveIdentity's
// own call, without editing the process environment to get there.
func withoutPinnedIdentity(dir string) func(string) (string, error) {
	drop := map[string]bool{
		"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
		"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
		"EMAIL": true,
	}
	var kept []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			kept = append(kept, kv)
		}
	}
	return func(name string) (string, error) {
		return RunReplaceEnv(dir, kept, "var", name)
	}
}

// TestResolveIdentityReadsTheReposConfig: the identity is the name and email
// git resolves for the repo, its own config included, parsed out of "git
// var"'s line even when the name has spaces and the email a plus tag.
func TestResolveIdentityReadsTheReposConfig(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Store Person"},
		{"config", "user.email", "store.person+jig@example.invalid"},
	} {
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	got, err := resolveIdentity(dir, withoutPinnedIdentity(dir))
	if err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}
	if want := (Identity{Name: "Store Person", Email: "store.person+jig@example.invalid"}); got != want {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
}

// TestResolveIdentityHonorsTheEnvironmentFirst: ResolveIdentity asks git with
// this process's environment, so a pinned author wins over the repo's
// config, as it does for the commit it stands for.
func TestResolveIdentityHonorsTheEnvironmentFirst(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Store Person"},
		{"config", "user.email", "store.person@example.invalid"},
	} {
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	pinned := append(os.Environ(), "GIT_AUTHOR_NAME=Pinned Author", "GIT_AUTHOR_EMAIL=pinned@example.invalid")
	got, err := resolveIdentity(dir, func(name string) (string, error) {
		return RunReplaceEnv(dir, pinned, "var", name)
	})
	if err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}
	if want := (Identity{Name: "Pinned Author", Email: "pinned@example.invalid"}); got != want {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
}

// TestResolveIdentityRefusesWithoutOne: a repo with no identity, where
// user.useConfigOnly keeps git from guessing one from the host's account and
// name, is refused with IDENTITY_REQUIRED, one message line, and the two
// commands that set an identity.
func TestResolveIdentityRefusesWithoutOne(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.useConfigOnly", "true"},
	} {
		if _, err := Run(dir, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	_, err := resolveIdentity(dir, withoutPinnedIdentity(dir))
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "IDENTITY_REQUIRED" {
		t.Fatalf("resolveIdentity = %v, want the IDENTITY_REQUIRED refusal", err)
	}
	if strings.Contains(ae.Msg, "\n") {
		t.Errorf("Msg = %q, want one line", ae.Msg)
	}
	if len(ae.Help) != 2 || !strings.Contains(ae.Help[0], "user.name") || !strings.Contains(ae.Help[1], "user.email") {
		t.Errorf("Help = %q, want the user.name and user.email commands", ae.Help)
	}
	if !strings.Contains(ae.Msg, dir) {
		t.Errorf("Msg = %q, want it to name %q", ae.Msg, dir)
	}
	for _, h := range ae.Help {
		if !strings.HasPrefix(h, "Run `") {
			t.Errorf("help line %q, want jig's \"Run `...`\" hint style", h)
		}
	}
}

// TestResolveIdentityOfAConfiguredRepo is the control: the exported function
// resolves an ordinary repo-level identity, as the store's commits do.
func TestResolveIdentityOfAConfiguredRepo(t *testing.T) {
	dir := initRepo(t, "https://example.invalid/fake/repo.git")
	got, err := ResolveIdentity(dir)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if want := (Identity{Name: "jig-fixture", Email: "fixture@example.invalid"}); got != want {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
}
