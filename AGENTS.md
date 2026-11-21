# jig

A CLI that a coding agent's own session uses to take a ticket from brief to merged PR, phase by phase: jig witnesses the builder's oracle runs, records the review and writes the publish.

- **Vocabulary** → `CONTEXT.md`
- **Structure and store schema** → `ARCHITECTURE.md`
- **Why a decision was made** → `docs/adr/`
- **Session skills** → `skills/`
- **CLI behavior** → run the `jig` binary's help; the environment is the source
- **Verification** → the touched packages while working; the full suite is CI's alone, and a ticket's last run is `jig verify --final` (`.github/CONTRIBUTING.md`)
- **Contribution workflow, pull request shape, testing rules** → `.github/CONTRIBUTING.md`
- **Reporting a security issue** → `.github/SECURITY.md`
