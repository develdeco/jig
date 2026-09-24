package screen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// commandCase describes one Command() table row.
type commandCase struct {
	name     string
	cmd      string
	allow    bool
	mentions string // for denies: substring the reason must contain
}

func TestCommandScreenTable(t *testing.T) {
	cases := []commandCase{
		// -- banned subcommands --
		{"push via &&", "cd x && git push", false, "push"},
		{"push with force flag", "git push --force origin T-758", false, "push"},
		{"push with global git-dir/work-tree", `git --git-dir=/r/.git --work-tree=/r push`, false, "push"},
		{"push via semicolon", "echo hi; git push", false, "push"},
		{"reset --hard", "git reset --hard HEAD~1", false, "reset"},
		{"clean -fd", "git clean -fd", false, "clean"},
		{"rebase", "git rebase master", false, "rebase"},
		{"branch -D", "git branch -D T-758", false, "branch"},
		{"worktree remove", "git worktree remove .claude/worktrees/T-758", false, "worktree"},
		{"cherry-pick", "git cherry-pick abc123", false, "cherry-pick"},
		{"tag", "git tag v1.2.3", false, "tag"},
		{"revert", "git revert HEAD~1", false, "revert"},
		{"am", "git am 0001-patch.mbox", false, "am"},
		{"stash drop", "git stash drop", false, "stash"},
		{"stash clear", "git stash clear", false, "stash"},
		{"worktree prune", "git worktree prune", false, "worktree"},
		{"branch -d", "git branch -d T-758", false, "branch"},
		{"branch --delete", "git branch --delete T-758", false, "branch"},
		{"bareword push denied", "git log --grep push", false, "push"},

		// -- secret paths embedded in a command --
		{"env production windows path", `type C:\repo\.env.production`, false, ".env.production"},
		{"env unix path", "cat /opt/app/.env.production", false, ".env"},

		// -- allowed git usage --
		{"status --short", "git status --short", true, ""},
		{"log --format", "git log --format=%H -1", true, ""},
		{"log --oneline", "git log --oneline -5", true, ""},
		{"diff --stat", "git diff --stat", true, ""},
		{"add -A", "git add -A", true, ""},
		{"branch -vv", "git branch -vv", true, ""},
		{"rev-parse", "git rev-parse --abbrev-ref HEAD", true, ""},
		{"stash list", "git stash list", true, ""},
		{"worktree list", "git worktree list", true, ""},
		{"-C consumes value", "git -C /wt diff --name-only master", true, ""},
		{"npm no git", "npm run commit-lint", true, ""},
		{"yarn no git", "yarn cypress run --component", true, ""},
		{"commit allowed", `git commit -m 'x'`, true, ""},
		{"-C commit", "git -C /wt commit -m x", true, ""},
		{"quoted -C value, amend", `git -C "D:\work\repo\.claude\worktrees\T-758" commit --amend --no-edit`, true, ""},
		{"git-dir= work-tree= commit", "git --git-dir=/r/.git --work-tree=/r commit", true, ""},
		{"-c consumes value twice", "git -c user.email=x -c user.name=y commit -m z", true, ""},
		{"git.exe recognised", "git.exe commit -m x", true, ""},
		{"absolute path git", "/usr/bin/git commit", true, ""},
		{"reset --soft allowed", "git reset --soft HEAD~1", true, ""},
		{"bareword commit not banned", "git log --grep commit", true, ""},

		// -- added cases: secret screen additions --
		{"cat .env", "cat .env", false, ".env"},
		{"cat .env.local", "cat .env.local", false, ".env.local"},
		{"cat id_rsa", "cat id_rsa", false, "id_rsa"},
		{"cat id_rsa.pub", "cat id_rsa.pub", false, "id_rsa.pub"},
		{"openssl pem", "openssl x509 -in server.pem", false, "server.pem"},
		{"api_key.txt", "cat api_key.txt", false, "api_key.txt"},
		{"secrets_key no ext", "cat secrets_key", false, "secrets_key"},
		{"aws credentials", "ls ~/.aws/credentials", false, ".aws"},
		{"gh hosts", "cat ~/.config/gh/hosts.yml", false, "gh"},
		{"env glob", "cat .env*", false, ".env"},
		{"pem glob", "cat *.pem", false, "pem"},

		// -- allow cases --
		{"readme allowed", "cat readme.md", true, ""},
		{"envfile allowed (not dotfile)", "cat envfile", true, ""},
		{"monkey.txt allowed (no _key)", "cat monkey.txt", true, ""},
		{"git status plain", "git status", true, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := Command(c.cmd)
			if ok != c.allow {
				t.Fatalf("Command(%q) = (%q, %v); want ok=%v", c.cmd, reason, ok, c.allow)
			}
			if !c.allow {
				if reason == "" {
					t.Fatalf("Command(%q): expected a reason, got empty", c.cmd)
				}
				if c.mentions != "" && !strings.Contains(strings.ToLower(reason), strings.ToLower(c.mentions)) {
					t.Fatalf("Command(%q) reason %q does not mention %q", c.cmd, reason, c.mentions)
				}
			}
		})
	}
}

// TestUnknownToolDenied pins that a tool this screen has no path rules for
// is denied rather than judged on a guess: the screen is the grant, so a
// tool it cannot reason about gets nothing. PowerShell is one such name:
// the CLI this backend drives has no PowerShell tool.
func TestUnknownToolDenied(t *testing.T) {
	for _, tool := range []string{"PowerShell", "WebFetch", "Task", "mcp__x__y", ""} {
		reason, ok := ToolCall(tool, map[string]any{"command": "echo hi"})
		if ok {
			t.Errorf("ToolCall(%q) allowed, want denied", tool)
		}
		if !strings.Contains(reason, "does not know") {
			t.Errorf("ToolCall(%q) reason = %q, want it to say the tool is unknown", tool, reason)
		}
	}
}

// TestBashToolScreened pins that the shell tool still routes its command
// through Command.
func TestBashToolScreened(t *testing.T) {
	if _, ok := ToolCall("Bash", map[string]any{"command": `git -C D:\wt push`}); ok {
		t.Fatalf("expected Bash git push to be denied")
	}
	if _, ok := ToolCall("Bash", map[string]any{"command": `git -C D:\wt commit -m x`}); !ok {
		t.Fatalf("expected Bash git commit to be allowed")
	}
}

// TestSecretPathThroughEveryToolArgument pins the argument each tool picks
// files with, not one shared key list: Grep filters with "glob" and Glob
// selects with "pattern", so a credential file named there is what comes
// back in the transcript when nothing checks it.
func TestSecretPathThroughEveryToolArgument(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]any
	}{
		{"Grep", map[string]any{"pattern": "SECRET", "path": ".", "glob": ".env*", "output_mode": "content"}},
		{"Grep", map[string]any{"pattern": "KEY", "path": ".", "glob": "*.pem"}},
		{"Glob", map[string]any{"pattern": "**/*.pem"}},
		{"Glob", map[string]any{"pattern": "**/.env*"}},
		{"Read", map[string]any{"file_path": "/home/op/.ssh/id_ed25519"}},
		{"Read", map[string]any{"file_path": "/home/op/.netrc"}},
		{"Read", map[string]any{"file_path": "/home/op/.npmrc"}},
		{"Grep", map[string]any{"pattern": "x", "path": "~/.ssh"}},
	}
	for _, c := range cases {
		if reason, ok := ToolCall(c.tool, c.input); ok {
			t.Errorf("ToolCall(%q, %v) allowed, want denied", c.tool, c.input)
		} else if !strings.Contains(reason, "credentials") {
			t.Errorf("ToolCall(%q, %v) reason = %q, want a credential denial", c.tool, c.input, reason)
		}
	}
	if _, ok := ToolCall("Grep", map[string]any{"pattern": "func main", "path": ".", "glob": "*.go"}); !ok {
		t.Fatalf("an ordinary Grep was denied")
	}
}

// secretCase describes one secret-path table row, exercised through
// ToolCall's path keys (Read-tool style) or SecretPath directly.
type secretCase struct {
	name  string
	value string
	deny  bool
}

func TestSecretReadTable(t *testing.T) {
	cases := []secretCase{
		{"env local", "/repo/.env.local", true},
		{"plain source file", "src/index.js", false},
		{"env production windows", `C:\repo\.env.production`, true},
		{"id_rsa", "id_rsa", true},
		{"id_rsa.pub", "id_rsa.pub", true},
		{"pem", "server.pem", true},
		{"api_key.txt", "api_key.txt", true},
		{"secrets_key", "secrets_key", true},
		{"aws credentials", "~/.aws/credentials", true},
		{"gh hosts", "~/.config/gh/hosts.yml", true},
		{"readme", "readme.md", false},
		{"envfile", "envfile", false},
		{"monkey.txt", "monkey.txt", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SecretPath(c.value)
			if got != c.deny {
				t.Fatalf("SecretPath(%q) = %v; want %v", c.value, got, c.deny)
			}
		})
	}

	// Adapted Read-tool cases via ToolCall's file_path key.
	toolCases := []struct {
		name  string
		input map[string]any
		allow bool
	}{
		{"Read env local denied", map[string]any{"file_path": "/repo/.env.local"}, false},
		{"Read plain file allowed", map[string]any{"file_path": "src/index.js"}, true},
	}
	for _, c := range toolCases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := ToolCall("Read", c.input)
			if ok != c.allow {
				t.Fatalf("ToolCall(Read, %v) = (%q, %v); want ok=%v", c.input, reason, ok, c.allow)
			}
		})
	}
}

// TestGrants pins the tools a passing screen grants: the shell and
// file-read tools only. An edit tool is never granted here - its grant is
// the headless backend's path-scoped permission rule - and neither is a tool
// outside the headless session's surface.
func TestGrants(t *testing.T) {
	for _, tool := range []string{"Bash", "Read", "Glob", "Grep"} {
		if !Grants(tool) {
			t.Errorf("Grants(%q) = false, want true", tool)
		}
	}
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "PowerShell", "WebFetch", "Task", "Skill", "mcp__x__y", ""} {
		if Grants(tool) {
			t.Errorf("Grants(%q) = true, want false", tool)
		}
	}
}

// TestCredentialDirectories pins that a credential location is denied
// however it is spelled: a directory as much as a file under it, with or
// without a trailing separator, since a tool given a search root reads
// everything beneath it.
func TestCredentialDirectories(t *testing.T) {
	denied := []string{
		"/home/op/.ssh", "/home/op/.ssh/", "/home/op/.ssh/id_ed25519",
		"~/.aws", `C:\Users\op\.aws`, `C:\Users\op\.aws\credentials`,
		"/home/op/.config/gh", "/home/op/.gnupg", "/home/op/.kube/config",
		"/home/op/.docker/config.json",
	}
	for _, p := range denied {
		if !SecretPath(p) {
			t.Errorf("SecretPath(%q) = false, want true", p)
		}
	}
	allowed := []string{
		"/home/op/project/src/main.go", "alpha/alpha.go", "./README.md",
		"/home/op/.config/jig/project.yaml", "/home/op/sshfs/notes.txt",
	}
	for _, p := range allowed {
		if SecretPath(p) {
			t.Errorf("SecretPath(%q) = true, want false", p)
		}
	}
}

// TestSecretTargetResolvesSymlinks pins that the screen decides on what a
// call would open, not on how the path is written: a link inside the lease
// pointing at a credential directory is denied by where it lands.
func TestSecretTargetResolvesSymlinks(t *testing.T) {
	home := t.TempDir()
	creds := filepath.Join(home, ".aws")
	if err := os.MkdirAll(creds, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(creds, "credentials"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease := t.TempDir()
	link := filepath.Join(lease, "vendor")
	if err := os.Symlink(creds, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	through := filepath.Join(link, "credentials")
	if SecretPath(through) {
		t.Fatalf("this case is only meaningful when the literal path looks innocent: %q", through)
	}
	if !secretTarget(through) {
		t.Errorf("secretTarget(%q) = false, want true: it resolves into a credential directory", through)
	}
	if _, ok := ToolCall("Read", map[string]any{"file_path": through}); ok {
		t.Error("ToolCall(Read) through a symlink into .aws was allowed")
	}
	if _, ok := ToolCall("Grep", map[string]any{"pattern": "x", "path": link}); ok {
		t.Error("ToolCall(Grep) rooted at a symlink into .aws was allowed")
	}
	ordinary := filepath.Join(lease, "src")
	if err := os.MkdirAll(ordinary, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := ToolCall("Grep", map[string]any{"pattern": "x", "path": ordinary}); !ok {
		t.Error("an ordinary in-lease directory was denied")
	}
}

// TestToolPathArgsCoverTheGrantedSurface pins that every tool a headless
// session is given has an entry here, so adding one to the surface without
// telling the screen how it names files fails loudly rather than silently.
func TestToolPathArgsCoverTheGrantedSurface(t *testing.T) {
	for _, tool := range append(append([]string{}, Granted...), "Edit", "Write", "NotebookEdit") {
		if _, ok := toolPathArgs[tool]; !ok {
			t.Errorf("tool %q is in the session's surface but has no toolPathArgs entry", tool)
		}
	}
}

// TestToolCallDeniesUnreadableInput pins a fix from an earlier adversarial
// review: a known tool whose required argument is missing, or whose
// command or path argument is present with a type SecretPath cannot read,
// is denied rather than let through on a guess - a call the screen cannot
// read cannot be judged. These include the exact payloads that review
// found allowed against the real hook binary; cmd/jig's TestScreenDenyAllow
// drives the same shapes through the hook end to end.
func TestToolCallDeniesUnreadableInput(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input map[string]any
	}{
		{"Bash: command under the wrong key", "Bash", map[string]any{"cmd": "git push"}},
		{"Bash: empty tool_input", "Bash", map[string]any{}},
		{"Bash: nil tool_input", "Bash", nil},
		{"Bash: command is a list", "Bash", map[string]any{"command": []any{"git", "push"}}},
		{"Bash: command is a number", "Bash", map[string]any{"command": 7.0}},
		{"Bash: command is null", "Bash", map[string]any{"command": nil}},

		{"Read: no file_path", "Read", map[string]any{}},
		{"Read: file_path is a number", "Read", map[string]any{"file_path": 7.0}},
		{"Read: file_path is an object", "Read", map[string]any{"file_path": map[string]any{"path": "/repo/.env"}}},
		{"Read: file_path is null", "Read", map[string]any{"file_path": nil}},
		{"Read: file_path is a nested list", "Read", map[string]any{"file_path": []any{[]any{"/repo/.env"}}}},

		{"Write: no file_path", "Write", map[string]any{"content": "x"}},
		{"Write: file_path is a bool", "Write", map[string]any{"file_path": true, "content": "x"}},
		{"Edit: no file_path", "Edit", map[string]any{"old_string": "a", "new_string": "b"}},
		{"NotebookEdit: no notebook_path", "NotebookEdit", map[string]any{"new_source": "x"}},
		{"NotebookEdit: notebook_path is a number", "NotebookEdit", map[string]any{"notebook_path": 1.0}},
		{"Glob: no pattern", "Glob", map[string]any{"path": "."}},
		{"Glob: pattern is a number", "Glob", map[string]any{"pattern": 1.0}},

		{"Grep: path is a number, pattern present", "Grep", map[string]any{"pattern": "x", "path": 1.0}},
		{"Grep: glob is an object", "Grep", map[string]any{"pattern": "x", "glob": map[string]any{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := ToolCall(c.tool, c.input)
			if ok {
				t.Fatalf("ToolCall(%q, %v) allowed, want denied", c.tool, c.input)
			}
			if !strings.Contains(reason, "cannot be judged") {
				t.Errorf("ToolCall(%q, %v) reason = %q, want it to say the call cannot be judged", c.tool, c.input, reason)
			}
		})
	}
}

// TestToolCallAllowsOptionalArgsAndUnknownKeys pins the other side of the
// unreadable-input fix: an argument that is genuinely optional (Grep's
// glob and path, Glob's path) may be absent, and a key the screen has no
// rule for - an extra one the CLI adds, such as Bash's "description" - is
// not inspected and does not affect the decision.
func TestToolCallAllowsOptionalArgsAndUnknownKeys(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input map[string]any
	}{
		{"Grep with only pattern", "Grep", map[string]any{"pattern": "func main"}},
		{"Glob with only pattern", "Glob", map[string]any{"pattern": "**/*.go"}},
		{"Bash with an unknown extra key", "Bash", map[string]any{"command": "git status", "description": "check status"}},
		{"Bash with every benign extra key", "Bash", map[string]any{"command": "git status", "timeout": 5.0, "run_in_background": false, "dangerouslyDisableSandbox": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if reason, ok := ToolCall(c.tool, c.input); !ok {
				t.Fatalf("ToolCall(%q, %v) denied (%q), want allowed", c.tool, c.input, reason)
			}
		})
	}
}

// TestSecretTargetSkipsNetworkAndDevicePaths pins a fix from an earlier
// adversarial review: a UNC share or a device path is judged by its
// literal spelling only.
// Resolving it (Abs/EvalSymlinks) can dial a remote host - 192.0.2.1 is a
// TEST-NET-1 address, reserved and unroutable, and a live resolution
// attempt against it was measured at about 21 seconds - so secretTarget
// must return well within that, with no filesystem or network access for
// these spellings. Restoring resolution for them fails this test on
// Windows, where EvalSymlinks actually reaches out to try.
func TestSecretTargetSkipsNetworkAndDevicePaths(t *testing.T) {
	cases := []string{
		`\\192.0.2.1\share\x`,
		`//192.0.2.1/share/x`,
		`\\?\C:\some\path`,
		`\\.\PhysicalDrive0`,
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			start := time.Now()
			got := secretTarget(p)
			if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
				t.Fatalf("secretTarget(%q) took %v, want well under a second: resolution must be skipped for a network/device path", p, elapsed)
			}
			if got {
				t.Errorf("secretTarget(%q) = true, want false: it names no credential directory by spelling, and must not be resolved to find out otherwise", p)
			}
		})
	}
}

// TestUNCCredentialPathJudgedBySpellingNotResolution pins that a UNC token
// whose literal spelling names a credential directory is still denied - by
// SecretPath, not by resolving it - and that judging it costs no real
// time. Host 192.0.2.1 is unroutable; a resolution attempt against it
// would itself reproduce the bug this test guards against.
func TestUNCCredentialPathJudgedBySpellingNotResolution(t *testing.T) {
	tok := `\\192.0.2.1\share\.aws\credentials`
	start := time.Now()
	reason, ok := Command("cat " + tok)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Command(%q) took %v, want well under a second", tok, elapsed)
	}
	if ok {
		t.Fatalf("Command(%q) = (%q, true), want denied", tok, reason)
	}
	if !strings.Contains(reason, "credentials") {
		t.Errorf("Command(%q) reason = %q, want a credential denial", tok, reason)
	}
}

// TestCommandBashResolvesSymlinks pins the Bash side of "judge where a
// path lands": a token in a shell command that is a symlink pointing into
// a credential directory is denied by where it resolves, not only a
// tool's file_path/path argument. Removing `|| secretTarget(tok)` from
// Command leaves this the only failure in the suite.
func TestCommandBashResolvesSymlinks(t *testing.T) {
	creds := t.TempDir()
	if err := os.MkdirAll(filepath.Join(creds, ".aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(creds, ".aws", "credentials"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease := t.TempDir()
	link := filepath.Join(lease, "vendor")
	if err := os.Symlink(filepath.Join(creds, ".aws"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	target := filepath.Join(link, "credentials")
	if SecretPath(target) {
		t.Fatalf("this case is only meaningful when the literal path looks innocent: %q", target)
	}
	if reason, ok := Command("cat " + target); ok {
		t.Fatalf("Command(%q) = (%q, true), want denied: a Bash token through a symlink into .aws must resolve", target, reason)
	}
}

// TestCredentialFiles pins a fix from an earlier adversarial review: the
// credential stores the denylist still missed, most pointedly the two
// tools jig itself drives - git's own push token and the credential cache
// of the CLI the session runs in - plus, beside each one, a near-miss that
// must stay allowed: an ordinary file is not a credential store just
// because it shares a directory with one.
func TestCredentialFiles(t *testing.T) {
	cases := []struct {
		name     string
		denied   string
		nearMiss string
	}{
		{"git push token", "/home/op/.git-credentials", "/home/op/project/git-credentials-helper.go"},
		{"claude CLI token", "/home/op/.claude/.credentials.json", "/home/op/.claude/settings.json"},
		{"claude CLI state", "/home/op/.claude.json", "/home/op/project/claude.json"},
		{"git config credential store", "/home/op/.config/git/credentials", "/home/op/.config/git/config"},
		{"azure token cache", "/home/op/.azure/msal_token_cache.json", "/home/op/.azure/azureProfile.json"},
		{"gcloud credential db", "/home/op/.config/gcloud/credentials.db", "/home/op/.config/gcloud/active_config"},
		{"rubygems credentials", "/home/op/.gem/credentials", "/home/op/.gem/specs.4.8"},
		{"pypi credentials", "/home/op/.pypirc", "/home/op/project/pypirc-notes.md"},
		{"terraform credentials", "/home/op/.terraform.d/credentials.tfrc.json", "/home/op/.terraform.d/plugin-cache/registry.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !SecretPath(c.denied) {
				t.Errorf("SecretPath(%q) = false, want true", c.denied)
			}
			if _, ok := ToolCall("Read", map[string]any{"file_path": c.denied}); ok {
				t.Errorf("ToolCall(Read, %q) allowed, want denied", c.denied)
			}
			if SecretPath(c.nearMiss) {
				t.Errorf("SecretPath(%q) = true, want false: an ordinary file beside the credential store must stay readable", c.nearMiss)
			}
			if _, ok := ToolCall("Read", map[string]any{"file_path": c.nearMiss}); !ok {
				t.Errorf("ToolCall(Read, %q) denied, want allowed", c.nearMiss)
			}
		})
	}

	// A lease's own .claude directory carries readable files beside the
	// credential cache: settings.json and skills must never be swept in.
	leaseFiles := []string{
		"/wt/T-1/.claude/settings.json",
		"/wt/T-1/.claude/skills/plan-ticket/SKILL.md",
	}
	for _, p := range leaseFiles {
		if _, ok := ToolCall("Read", map[string]any{"file_path": p}); !ok {
			t.Errorf("ToolCall(Read, %q) denied, want allowed: an ordinary lease file", p)
		}
	}
}

// architectureScreenRow parses this package's row from ARCHITECTURE.md's
// "## Module responsibilities" table and returns its Entry points cell,
// split into names. It is the doc's own claim about this package's
// surface, read fresh on every run rather than copied into a second list a
// future edit could update on only one side.
func architectureScreenRow(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatalf("read ARCHITECTURE.md: %v", err)
	}
	const prefix = "| `internal/screen/` |"
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			t.Fatalf("ARCHITECTURE.md: internal/screen row has too few columns: %q", line)
		}
		var names []string
		for _, tok := range strings.Split(cells[2], ",") {
			if name := strings.Trim(strings.TrimSpace(tok), "`"); name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			t.Fatalf("ARCHITECTURE.md: internal/screen row's entry points cell is empty: %q", line)
		}
		return names
	}
	t.Fatalf("ARCHITECTURE.md: no internal/screen row in the Module responsibilities table")
	return nil
}

// TestExportedSurface pins a fix from an earlier adversarial review:
// SecretTarget was exported but had no caller outside this package. It
// parses the package's own non-test source and ARCHITECTURE.md's
// internal/screen row (architectureScreenRow), and fails if the two
// disagree in either direction - an export the row
// does not list, or a listed name the package no longer exports - so a
// doc/code mismatch is caught here instead of by a reviewer. There is
// deliberately no second, hand-written list to keep in step: the row is
// the one source both sides are checked against.
func TestExportedSurface(t *testing.T) {
	want := map[string]bool{}
	for _, name := range architectureScreenRow(t) {
		want[name] = true
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse internal/screen: %v", err)
	}
	pkg, ok := pkgs["screen"]
	if !ok {
		t.Fatalf("package %q not found among parsed packages %v", "screen", pkgs)
	}

	got := map[string]bool{}
	for _, f := range pkg.Files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					got[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.IsExported() {
								got[name.Name] = true
							}
						}
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							got[s.Name.Name] = true
						}
					}
				}
			}
		}
	}

	for name := range got {
		if !want[name] {
			t.Errorf("package screen exports %q, which ARCHITECTURE.md's screen row does not list - unexport it, or update the doc", name)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("package screen no longer exports %q, which ARCHITECTURE.md's screen row lists", name)
		}
	}
}
