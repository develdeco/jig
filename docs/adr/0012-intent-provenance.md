# Intent has a provenance, and the gate no longer needs a brief

The gate reviewer used to be pointed at `brief.md` unconditionally: `Gate`
built `brief_path` from the ticket's own brief file, or, in `--branch`
mode, from `--doc` directly - the reviewer read that file itself, per
round - and the prompt told the reviewer to judge the diff "against the
brief". A `--branch` ticket adopted with no brief - a developer already
iterating by hand on a working branch, gating each round against a plain
statement of intent rather than an upfront brief - pointed the reviewer
at a file that was never there, and outside `--branch`, `--doc` was
ignored outright: there was no way to hand a brief-less ticket an intent
at all. `--doc`'s content was also copied, after a successful round, into
that round's own `gate/round-N/spec-input.md` - an archival record
nothing ever read back. Neither shape survives a ticket that starts from
a branch instead of a brief.

What every round actually needs is not a brief specifically but a
statement of intent: what was asked for, so the reviewer can judge a fix
by whether it changes that. A brief already carries that statement, but
it is not the only way to state it, and a review with nothing to compare
against - a ticket with neither - is a real, ordinary state, not an error.
So intent gets a small type of its own, `verifydeliver.Intent{Source,
Path}`, resolved once per round in `Gate`, before the round's own source
runs, by one fixed precedence: the ticket's `brief.md` (source `"brief"`),
else its `intent.md` and the source recorded there, else `"none"`.
`"brief"` and `"explicit"` are binding - the human's own statement of what
was asked for, the reviewer's fix definition turns on it. `"none"` is not
a missing value jig quietly falls back from; it is itself information -
nothing states what this change is for - and the reviewer is told exactly
that, in the same place it would otherwise be told what does. A later
source, `"inferred"` (a planned addition, not yet built: reading Claude
Code transcripts for a file-overlap match), is a hint rather than
binding: it can shape a fix judgment but a human never handed jig those
words, so it never overrides an ask the way a brief or an explicit
intent would.

A ticket has one intent, not one per round: `intent.md` is a file,
ticket-scoped like `brief.md` already is, and `jig gate --intent <text>` or
`--doc <path>` writes it - a small YAML front matter (`source: explicit`)
plus the text, replacing whatever was there before, never appending. A
ticket that already has a `brief.md` always resolves to it regardless of
what `intent.md` might say, so writing one there would record a
provenance jig would never actually read; both flags are refused with
`INTENT_CONFLICT` in that case, with help pointing at amending the brief
instead. This is the same shape review's own precedence already argues
for structurally (`writeExplicitIntent` checks brief.md's existence, not
its content, and refuses before writing intent.md) rather than a rule
enforced only by convention. `--doc`'s old behavior - copying a file's
content into a round-scoped `spec-input.md` - is retired outright:
`intent.md` is ticket-scoped like `brief.md` always was, and a round no
longer needs a spec-axis input distinct from its resolved intent.

`report.yaml` records the round's resolved source and the sha256 of the
exact bytes of the file at `intent.path` (empty for `"none"`), not the
text itself: the text already lives in `brief.md` or `intent.md`, both
already in the store's trust domain, and a hash is enough to prove a
round did resolve to a particular statement without duplicating it into
every round's own report. `review.json` carries the same `{source, path}`
pair the resolver produced, replacing `brief_path` outright - no dead
field survives beside it - and the reviewer reads the file at that path
itself, exactly as it read `brief_path` before. The prompt names what
each source means once, generically, rather than branching on which
source this round actually resolved to: it already reads `review.json`'s
own `intent` block, so the prompt's job is only to say what `"brief"`,
`"explicit"` and `"none"` each mean, not to restate this round's
particular value.

What that later addition brings: a fourth source, `"inferred"`, from a
summarizer session reading local Claude Code transcripts for a
file-overlap match with this round's own scope diff, dispatched only
when no intent is already recorded. It is a hint, not binding, and it
still goes through `intent.md` - the same file, a different source
value. `resolveIntent` accepts only the source values jig itself ever
records to `intent.md` - today, only `"explicit"` - and refuses anything
else with `INTENT_INVALID_SOURCE`, so this later addition is not free:
landing it means adding `"inferred"` to that accepted set, the one place
the precedence's own source check lives, not a change to the precedence
itself.
