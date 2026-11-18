# A repo with no declaration has no named oracle

[ADR 0027](0027-jigs-testing-method-and-what-a-project-owns.md) said a repo with no `.claude/jig.yaml` still works because jig fills in what it can: a `go.mod` gave it a `test` oracle (`go test ./...`) and a `package.json` gave it one oracle per script (`npm run <script>`). That was a guess per language. It covers two languages, each new one needs code in jig, and both guesses are whole suites. The agent that builds in a repo reads the repo and its instructions and finds the real test commands; jig adds nothing to gather them.

## Decision

**A repo with no declaration has no named oracle.** `manifest.Resolve` of a directory with no `.claude/jig.yaml` returns the root workspace, no envs and no oracle, whatever the directory holds. It does not stat `go.mod` and never reads `package.json`, so a malformed `package.json` can no longer make a manifest unreadable.

**What stays.** A declared `oracles:` resolves as before: a declared name maps to its command and `{path}` is replaced by the workspace's path. A slice's `oracle:` that names no declared oracle is run as a literal command, as `OracleCmd` always did. `workspaces:`, `envs:`, `invariants:` and `timeout:` are unchanged.

**What a slice naming an undeclared `test` shows.** In a repo that declares no `test` oracle the slice's `oracle: test` is the literal command `test`. The shell's `test` builtin with no argument exits 1 at once, so `jig verify` ends in a fail, no suite runs, and the header printed before the run names `test` as the command that ran. That is the intended failure: the slice names a command the repo never declared, and the person or agent writes the real one. No refusal is added: jig records what ran and does not judge a command it cannot prove wrong.

**The `light:` key goes with it.** Nothing has acted on `light:` since `jig verify` stopped substituting a lighter run. Its type and validation were all that was left, so they are removed, and jig's own declaration, which held only a `test:` oracle and a `light:` rule that nothing read, is deleted. A `light:` or `heavy:` key in a declaration is ignored like any key the reader does not know, so older files still load and their oracles resolve. The journal fields that describe a light pass in journals written before (`ran`, `reused_from`) stay readable.

## Consequences

- A repo that relied on a detected oracle declares it in `.claude/jig.yaml`, or its slices write the command out.
- A project of several repos is checked only against what repos declare: a slice naming an oracle that no repo declares is a literal command, which a manifest cannot judge, so nothing is flagged.
- The review context's list of oracle names is empty for a repo that declares none.
- ADR 0027's other claims stand.
