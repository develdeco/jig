# The GATE-7 run's narrow test gaps are closed by tests, one behavior fix and a shared bash helper (DEV-11)

Ambiguous: the low findings of the GATE-7 run and chart wave 1 were notes, not fix slices, and several could be read as needing a design decision. All the calls below were defaulted in the brief.

- Scope widened to the wave 1 notes that need no design decision (every DEV-9, DEV-10 and TRACK-4 test gap, the WSL bash lookup, and three docs slips). Left out: anything needing a design decision.
- `gitx.BlobAt` returns an error for a path whose tree entry is a blob git cannot read (an object missing or damaged), and still reports found false with no error for an absent path, a directory and a submodule. The doc comment already promised an error for "a failure of git itself", and `reviewedTrailer` already warns and falls back on an error, so the visible change is a warning where there was silence. Rejected: a third return value, a wider API for one caller.
- One shared bash helper for the lint tests (`requireBash`): Git for Windows' bash on Windows, found from the PATH entries and the standard install directories with WSL's launcher (System32, WindowsApps) rejected, and `exec.LookPath("bash")` elsewhere. It never looks up git, which `lint/gitspawn_test.go` forbids.
- Two build-log entries whose names are equal once case is folded fail the lint, since a ticket id and its lowercase slug are one path on Windows and macOS. A ticket key may hold digits (`I18N-1`).
- Each gap's test sits beside the existing test of the same seam, not in a new file, except the shared bash helper. Each was checked by applying its named mutant once by hand and seeing it fail.
