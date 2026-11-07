# The build log is one file per ticket under docs/build-log/ (DEV-9)

Two branches that each appended an entry to the one DECISIONS.md conflicted on it.
The brief left four things open; each was user-confirmed on 2026-11-07.

- File name and shape: `docs/build-log/<TICKET>.md`, named by the ticket id exactly as
  the store spells it, so two tickets can never share a file. The first line is
  `# <what was decided> (<TICKET>)`, then what was ambiguous, what was chosen and why,
  and "Rejected:" where it applies. A ticket with several judgment calls adds a `## `
  subsection per call to its one file.
- A change with no ticket: a dependabot bump or routine hand PR writes no entry, because
  the log records judgment calls, not changes. A hand PR that resolves something
  ambiguous names its file by a lowercase slug of its branch, which cannot collide with
  a ticket id.
- No index: `ls` is the index, and a shared index file would bring the conflict back.
  README.md links the folder.
- This ticket writes its own entry (this file), which proves the form and leaves the
  folder non-empty. DECISIONS.md keeps every entry as history and gains one pointer line.

Rejected: appending to DECISIONS.md with a merge driver (hides the conflict instead of
removing it), and a lint that checks prose for a ban on appending (the owner's rule is
concepts, not phrase checks).
