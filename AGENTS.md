# jig

A CLI that a coding agent's own session uses to take a ticket from brief to merged PR, phase by phase: jig verifies each commit, records the review and writes the publish.

- **Vocabulary** → `CONTEXT.md`
- **Structure and store schema** → `ARCHITECTURE.md`
- **Why a decision was made** → `docs/adr/`
- **Session skills** → `skills/`
- **CLI behavior** → run the `jig` binary's help; the environment is the source
- **Build log** → `DECISIONS.md`
- **Verification** → the touched packages while working; the full suite is CI's and the gate's (`.github/CONTRIBUTING.md`)
- **Contribution workflow, pull request shape, testing rules** → `.github/CONTRIBUTING.md`
- **Reporting a security issue** → `.github/SECURITY.md`
