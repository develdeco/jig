# Every oracle runs through sh, on every OS

jig ran a slice's oracle, a manifest oracle and an env class's up, check and down as `cmd /C <command>` on Windows and `sh -c <command>` elsewhere (`internal/envrun`). The two shells read one command differently. The 2026-11-02 chart run built seven tickets whose oracles all used sh quoting, such as `go test ./internal/x -run 'A|B'`: cmd reads the `|` as a pipe into a command named `B'`, so every one of those oracles failed `jig verify` on Windows while it passed on Linux and macOS. The run verified them only because the session put a stand-in for cmd.exe first on PATH, one that handed such commands to sh. A Windows gate would have failed the slices as written.

Who writes and runs oracles decides which reading is the right one. Intake writes them, and builders test with them, from an agent session's own shell, and on Windows that shell is Git Bash (Claude Code needs Git for Windows there). An oracle is sh before jig ever runs it.

## Decision

**Every command jig runs from a manifest or a slice runs through sh, on every OS:** a slice's oracle, a manifest oracle, and an env class's up, check and down, at `jig verify`, at the gate and at publish's revalidation. One command reads the same wherever jig runs it, as the agent that wrote it read it.

**Which sh** (`envrun.Sh`). Off Windows it is the sh on PATH, as before. On Windows it is Git for Windows' `bin\sh.exe`, the launcher that gives a command Git Bash's environment (`/mingw64/bin` and `/usr/bin` first on its PATH, so `rm` or `grep` in an oracle is found): it is found from the git on PATH, in the nearest of git's directory and the three above it that holds `bin\sh.exe` (an install keeps git in `cmd`, `bin`, `mingw64\bin` or `mingw64\libexec\git-core`). A default install puts only `cmd` on PATH, so sh itself is not found there from PowerShell. Without Git's sh, jig takes the sh on PATH; with neither, the run fails with an error that says jig runs these commands with sh and how to get one. `jig verify` records that as a `verify-error`, a run that could not reach its oracle, not as a failed commit; the gate reports it as its oracle failure, with the reason.

**The command reaches sh through the environment.** sh runs a fixed script, `eval "unset JIG_SHELL_COMMAND; $JIG_SHELL_COMMAND"`, with the command in `JIG_SHELL_COMMAND`, on every OS. On Windows a command line is a single string that the program parses itself, and the runtime under Git's sh parses it by rules of its own before sh sees it: handed as `-c <command>`, `'a\\b'` reached sh as `a\b`. An environment variable reaches it byte for byte, and the script unsets it before the command runs, so neither the command nor what it starts sees it.

**The cmd workaround goes.** `envrun.ShortenQuotedPath`, which turned a leading quoted path into its 8.3 short form so `cmd /C` would not mangle it, is removed with its calls: sh reads a quoted path with spaces as written, and the unquoted backslashes of a short path would be escapes to sh.

**Nothing validates an oracle's quoting.** An oracle is an sh command; `jig validate` is unchanged, and the intake skill says a literal oracle is written for sh on every OS.

## Rejected alternatives

- **Keep cmd on Windows and have intake and validation require quoting both shells read alike**, such as double quotes in place of single. The subset cmd and sh agree on is small (no single quotes, no `$VAR`, no `VAR=x cmd`, no `;`, no redirection to `/dev/null`), and nothing parses it, so validation would be phrase rules over command text. A builder on Windows would still test in Git Bash and be gated by cmd, a manifest's own oracles and env classes would still differ, and cmd's quoting had already needed the short-path workaround.
- **A shell interpreter inside jig**, such as a Go implementation of POSIX sh. It parses the same everywhere without Git's sh, but it is not the shell the agent tested with, it runs no `rm` or `grep` on Windows without Git's tools on PATH anyway, and it brings a dependency whose edge cases differ from a real sh.
- **The command on sh's command line**, `sh -c <command>`, as jig did off Windows. It is exact on Linux and macOS, where argv is passed as it is, but on Windows the command is re-parsed by Git's sh before sh reads it, so the two OSes would again differ in what reaches the shell.
- **The sh on PATH first on Windows.** In an agent session's own shell, PATH holds Git's `usr\bin\sh.exe`, a POSIX-mode bash with no launcher; from PowerShell, the git-derived launcher would be found instead. One machine would run oracles in two environments depending on who started jig. The launcher first gives every caller the same one.

## Consequences

- An oracle written with sh quoting verifies the same on Windows as on Linux and macOS (`e2e`'s `TestVerifyRunsAnOracleWrittenForSh`, which CI's Windows leg runs). A repo that wrote cmd syntax into its `.claude/jig.yaml` for Windows (`if exist`, `%VAR%`, `del`) must write sh instead.
- jig on Windows now needs Git for Windows (or another sh) for oracles and env classes, beside the git it already needs.
- Git's sh on Windows is bash, not in POSIX mode, where Ubuntu's sh is dash: a bash-only construct can pass on Windows and fail on Linux. An oracle written for sh reads the same on both.
- The shell a headless builder session uses (`internal/session`) is the harness's, and unchanged.
- `CONTEXT.md`'s Oracle and Env class entries, `ARCHITECTURE.md`'s `internal/envrun/` row, `.github/CONTRIBUTING.md` and the intake skill say so.
