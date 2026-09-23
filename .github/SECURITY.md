# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security vulnerability. Report it
privately instead: open this repository's Security tab and choose "Report a
vulnerability". The report reaches the maintainer directly and stays private
until a fix ships.

## Supported versions

jig is pre-1.0. Only the latest tagged release gets security fixes - update
before reporting, since the issue may already be fixed.

## Scope

jig runs sessions against a real git checkout (a "lease": a pooled worktree
checked out for one phase's work). The screens below are a denylist, not a
lease sandbox: they deny specific destructive or history-rewriting git
subcommands and credential-shaped paths for the `headless` backend's tool
calls; they do not confine a session's shell to its lease, and ordinary git
commands (commit, checkout, fetch, and the like) are still allowed. A
`headless` session's shell and file-read tools are granted only by a passing
screen, and its file-edit tools only inside the lease and on its own
`result.json`. jig checks that the screen still answers before each screened
session starts, because the CLI's own classifier would otherwise leave a
read-only shell behind a screen that had stopped working. The lease's own
`.claude` settings are not loaded, since they are part of the code under
review. See the Safety section of
[ARCHITECTURE.md](../ARCHITECTURE.md#safety) for how each one works.

In scope:

- A way to make the structural command screen (`internal/screen.Command`)
  allow a `git push`, or any other banned subcommand, that it's meant to
  deny - disguised through quoting, path tricks, or otherwise.
- A path shaped like a live credential (`.env*`, `*_key*`, `id_rsa*`,
  `*.pem`, `~/.aws/**`, `~/.config/gh/**`, `~/.ssh/**`, `.netrc`, `.npmrc`,
  and the like) that the secret-path screen
  (`internal/screen.SecretPath`) fails to deny, including one named in a
  tool-call argument the screen does not check.
- A `headless` session that runs its shell or file-read tools without the
  screen hook passing the call (for example, when the hook stops working
  after the pre-dispatch check), or whose file-edit tools write outside its
  lease and its own `result.json`, through jig's generated settings rather
  than the operator's own Claude Code settings.
- Anything in the code under review that reaches the session or the machine
  through jig's own dispatch: a file in the lease that is loaded as
  configuration, a hook it can cause to run, or a way to widen the
  session's grants.
- A guarded-push bypass: a push of the ticket branch to a non-local remote
  that goes through without the `publish` command's confirmed step (its
  interactive confirm, or `--yes`) having run. (Every jig command pushes
  the ticket store's own bookkeeping commits to the store's remote with no
  confirm - that is normal behavior, not in scope here.)
- Anything that lets a session reach another ticket's worktree, the store's
  own git history, or the host machine itself, through jig's own machinery
  rather than through git or the OS directly.

Out of scope:

- Vulnerabilities in Go itself, the `git` binary, or a session backend
  (`claude`, `herdr`) that jig shells out to - report those upstream.
- Anything that requires an attacker to already control the machine jig
  runs on, or to have write access to the ticket branch or store repo they
  are attacking.
- A `headless` session doing something unwanted with the tools it was
  actually granted - its screens deny specific banned subcommands and
  paths, not everything a granted tool can do. `herdr` sessions run with no
  screening at all yet; that is a known gap, not something to report here.
