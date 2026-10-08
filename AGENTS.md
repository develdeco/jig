# jig

A CLI that runs a ticket from brief to merged PR as a dispatch loop over slices.

- **Vocabulary** → `CONTEXT.md`
- **Structure and store schema** → `ARCHITECTURE.md`
- **Why a decision was made** → `docs/adr/`
- **Session skills** → `skills/`
- **CLI behavior** → run the `jig` binary's help; the environment is the source
- **Build log** → `DECISIONS.md`
- **Verification** → while working, `go build ./...`, `go vet ./...` and `go test` on the packages you touched; the full suite (`go test -timeout 30m ./...`) runs in CI on every pull request, and a jig build's oracle runs when the builder reports green
- **Contribution workflow, pull request shape, testing rules** → `.github/CONTRIBUTING.md`
- **Reporting a security issue** → `.github/SECURITY.md`
