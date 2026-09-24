package screen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !SecretTarget(through) {
		t.Errorf("SecretTarget(%q) = false, want true: it resolves into a credential directory", through)
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
