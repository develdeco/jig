package screen

import (
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

func TestPowerShellToolScreened(t *testing.T) {
	if _, ok := ToolCall("PowerShell", map[string]any{"command": `git -C D:\wt push`}); ok {
		t.Fatalf("expected PowerShell git push to be denied")
	}
	if _, ok := ToolCall("PowerShell", map[string]any{"command": `git -C D:\wt commit -m x`}); !ok {
		t.Fatalf("expected PowerShell git commit to be allowed")
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
