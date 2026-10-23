# Digester build notes

One file per ticket of the digester build, written by the ticket that did the
work and read by the ticket that writes the docs (D1). A note is the record
that outlives the session: the code shows what exists, the note says what a
later reader cannot see from it.

## Naming

`S0a.md` and `S0b.md` for the two halves of the scaffold, then `D<n>.md` for
ticket D<n>. A ticket writes only its own file, so notes never conflict.

## What a note holds

- **Verbs and flags**: each verb the ticket added or changed, its flags, its
  exit codes, what it prints.
- **Line shapes**: the journal and ledger lines it writes or reads, with the
  fields it sets and the ones it relies on.
- **Decisions**: each choice the brief left open, with the reason, and every
  detail a later ticket needs to build on the work (an order, a name, a lock).
- **Deviations**: where the work differs from the brief, and why.
- **ADR impact**: what the architecture decision record has to say because of
  this ticket, if anything.
- **ARCHITECTURE row text**: the module-table row for any package dir the
  ticket added, in the table's columns (package, entry points, input to
  output). The scaffold already added a row for every package dir the build
  plans; a ticket that fills a stub gives its replacement text here.
- **Tests**: the tests it added and the ones it must keep green.

## Who touches what

A ticket does not edit README.md, ARCHITECTURE.md, CONTEXT.md, DECISIONS.md or
the ADRs. D1 writes all of that prose from these notes, and is the one ticket
that does. The exceptions are the module-table rows of a package dir a ticket
deletes, which lint requires to go with the dir.

Keep a note short and exact. It is read once, by a writer who has the code
open beside it.
