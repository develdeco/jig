# BS-1: `jig brief show`, and the illustration counts in the approval

Chart decision 14: the brief carries an illustration (`illustration.html`,
optional, beside `brief.md`), hashed with the sections; jig ships a minimal
local viewer; any other viewer is the project's choice and never named in core
files. Branch `ph/BS-1`.

## What was built

- `internal/store/approval.go`: `store.IllustrationFile` and the new rule of
  `Store.BriefSHA256`.
- `internal/briefview` (new): the viewer. `Handler(Source)`, `Page`, `Title`,
  `Summary`, plus a small markdown-to-HTML renderer (`markdown.go`).
- `cmd/jig/brief.go` (new): the verb `brief show`, registered in its own
  `init()` (Order 410).
- Tests: `internal/store/approval_test.go`, `internal/briefview/briefview_test.go`,
  `cmd/jig/brief_test.go`, `e2e/brief_test.go`.

No other file changed but a doc comment in `cmd/jig/validate.go`.

## The verb

```
jig brief show [T] [--port n] [--path] [--text] [--store p] [--project n]
```

`T` is the ticket (an id or an alias), else the ticket the working branch is
for (the same rule as `jig review context`: `reviewTicket` in `review.go`, so a
branch that names none is refused with `Name the ticket: jig brief show T-1`).

| Form | What it does |
|---|---|
| default | Listens on `127.0.0.1:<port>` (`--port`, default 4719; 0 picks a free one), prints the `viewer` block, serves until the run's context ends (the first interrupt), exits 0. |
| `--path` | Prints the `paths` block: the absolute path of `brief.md` and, when it exists, of `illustration.html`. Serves nothing. |
| `CLAUDE_CODE_REMOTE` set (read through `e.getenv`) | The same as `--path`, with a help line saying why the viewer is not started. "Set" is any non-empty value. |
| `--text` | Prints the brief as text for the TUI's detail screen (form below). Works the same when remote. |

`--path` with `--text` is a `VALIDATION_ERROR` (exit 2), as is a port outside
0..65535. A ticket with no `brief.md` is a `VALIDATION_ERROR` with the intake
hint. A port that cannot be listened on is `VIEWER_UNAVAILABLE` (exit 1), with
the help `Pick another port with --port, or --port 0 for any free one`.

### Output shapes

Serving (printed before the first request is answered):

```
viewer:
  url: "http://127.0.0.1:4719/"
  ticket: JIG-1
  illustration: "http://127.0.0.1:4719/illustration.html"   <- only when the file exists
help[1]:
  Open the url in a browser and reload to see an edit; interrupt (Ctrl-C) to stop the viewer
```

Values with `:` are quoted by `axi.Quote`, so the urls are quoted. A script reads
the `  url: ` line and strips the quotes. With `--port 0` this line is the only
way to learn the port.

Paths (`--path`, and remote):

```
paths:
  brief: <absolute path>
  illustration: <absolute path>    <- only when the file exists
help[1]:
  ...
```

`--text` (the form DD-1 parses):

```
brief:
  ticket: JIG-1
  title: Fixture ticket brief        <- the first "# " line before the first section; omitted when there is none
  illustration: yes|no
sections[N]{heading,summary}:
  <heading>,<first two non-blank lines of the body joined by a space, cut at 160 runes with "...">
section: <heading>
  <the section's body, every line indented by two spaces; a blank line stays empty>
section: <next heading>
help[1]:
  ...
```

A `section:` block ends at the next line that is not indented (and is not
empty), so a body can hold any text. A section with an empty body is its
`section:` line alone. Trailing white space of a body line is dropped, leading
blank lines of a body are dropped, and the text before the first `## ` heading
is not shown (only its `# ` title is). Headings come from `store.BriefSections`,
so the table lists them in order, a repeated heading twice.

### The page and its safety

- `GET /` is the page: the ticket id and title, a link to `/illustration.html`
  when the file exists, the text before the first section, a contents list
  (two sections or more), and each section as `<section id="sN"><h2>...`. The
  body is rendered from the markdown subset briefs use: `###` headings,
  paragraphs, nested lists, quotes, fenced code, pipe tables (shown as
  written), code spans, bold and `http(s)` links. Everything is HTML-escaped.
- `GET /illustration.html` is the file as it is on disk. Nothing else of the
  ticket folder is reachable (`approval.yaml`, `ticket.yaml`, the journal).
- Both files are read again on every request (`Cache-Control: no-store`), so
  the person reloads to see an edit while a session is still drafting.
- The page has no script, no external stylesheet, font or image: one inline
  stylesheet that follows the viewer's light or dark preference.
- Only requests whose `Host` is `127.0.0.1`, `localhost` or `::1` are answered
  (403 otherwise), so a web page in the person's browser cannot read the brief
  by pointing a name at the loopback address (DNS rebinding).
- The illustration is project-authored HTML and is served unchanged, scripts
  included. The viewer does not sandbox it.

## The hash rule (decision taken)

`Store.BriefSHA256(ticket)` returns:

- **no `illustration.html`**: the hex sha256 of `brief.md` exactly as the file
  is. This is today's value, so every approval already recorded stays
  `current`. Proven by `TestBriefSHA256WithoutAnIllustrationIsTheFilesHash`
  (a literal digest) and, through the binary, by
  `TestIllustrationEditWithdrawsApproval` (the recorded hash equals
  `sha256(brief.md)`, and an approval written by hand with that value reads
  `current`).
- **an `illustration.html`**: the hex sha256 of the two lines
  `brief.md <hex sha256 of brief.md>\n` and
  `illustration.html <hex sha256 of illustration.html>\n`. Hashing the
  per-file digests means no byte can move from one file to the other without
  changing the result (`TestBriefSHA256KeepsTheTwoFilesApart`). An empty
  `illustration.html` counts as one.

Adding, editing or removing the illustration therefore changes the hash, so
`jig status` reads `approval: changed` and an unattended `jig claim` is refused
(`CLAIM_NOT_APPROVED`, "changed after it was approved") until `jig validate`
approves it again. An `illustration.html` that exists but cannot be read (a
directory, say) makes `BriefSHA256` fail rather than pass as no illustration.

Not changed on purpose: a slice's `from_brief` holds section hashes
(`BriefSections`), so an illustration edit does not requeue slices, and
`internal/correct`'s "brief changed under verified work" reads sections only.
The illustration is part of the approval, not of what a slice was cut from. No
journal shape changed (an `approval` line carries the new hash in the same
`brief_sha256` field).

## Help text and ARCHITECTURE

Help row (`jig` with no arguments, and `jig brief show -h`):

```
brief show   show a ticket's brief and its illustration: serve them on 127.0.0.1, or print their paths or text
--port       port the viewer listens on, on 127.0.0.1; 0 picks a free one
--path       print the absolute paths of brief.md and illustration.html instead of serving them
--text       print the brief's sections as plain text instead of serving them
--store      explicit store path
--project    project name, resolved via the machine mapping
```

ARCHITECTURE.md, the `cmd/jig/` row: add `brief show` to "reading and watching"
(`status`, `events`, `tui`, `brief show`, ...).

ARCHITECTURE.md, a new module row (between `internal/axi/` and
`internal/claudetest/`):

```
| `internal/briefview/` | `Handler`, `Source`, `Page`, `Title`, `Summary`, `IllustrationRoute` | a ticket's `brief.md` and optional `illustration.html` → jig's one viewer of a brief: a loopback-only handler that serves the "## " sections as plain HTML (no script, nothing fetched; the files read again on each request) and the illustration as it is, the page renderer, and the title and section summary `jig brief show --text` prints; backs `jig brief show` |
```

ARCHITECTURE.md, the store section where the approval is described: the
approval's `brief_sha256` is the hash above, and `illustration.html` beside
`brief.md` is part of the brief for it. CONTEXT.md (DOCS-1's call): a short
entry for **Illustration** ("an optional `illustration.html` beside a brief's
`brief.md`, covered by the brief's approval; `jig brief show` is the one viewer
jig ships"). ADR 0032's "Not built" list names `jig brief show`; it is built
now.

## Tests

- `internal/store`: `TestBriefSHA256WithoutAnIllustrationIsTheFilesHash`,
  `TestBriefSHA256CoversTheIllustration`, `TestBriefSHA256KeepsTheTwoFilesApart`,
  `TestBriefSHA256RefusesAnIllustrationItCannotRead`.
- `internal/briefview`: page, markdown lists and blocks, handler routes,
  no-illustration and no-brief cases, the loopback-only rule, title and summary.
- `cmd/jig` (in process, with `withContext` to stop the viewer):
  `TestBriefShowText`, `TestBriefShowTextIsTheSameWhereverTheHarnessRuns`,
  `TestBriefShowPath`, `TestBriefShowPrintsPathsWhenRemote`,
  `TestBriefShowServesIllustration`, `TestBriefShowWithNoIllustrationLinksNone`,
  `TestBriefShowTakesPortFromFlag`, `TestBriefShowRefusals`,
  `TestBriefShowTakesTheTicketTheBranchNames`.
- `e2e` (the built binary): `TestBriefShowServesIllustration` (fetches the page
  and the illustration, then interrupts the process and expects exit 0; on
  Windows it can only kill it), `TestBriefShowPrintsPathsWhenRemote`,
  `TestBriefShowText`, `TestIllustrationEditWithdrawsApproval`.

Test counts: no test was removed or renamed. `internal/store` gains 4,
`internal/briefview` has 8 (new package), `cmd/jig` gains 9 and `e2e` 4.

## Findings (not fixed here)

- `axi.Quote` quotes a value that holds a backslash but does not escape it, so
  a Windows path prints as `"C:\Users\..."`. A reader cannot tell a path with
  `\n` in it (`C:\new`) from the two-character escape `Quote` writes for a
  newline. Every verb that prints a path has this; `paths:` is no worse.
- `e2e`'s `jigEnv.environ` drops the harness session variables but not
  `CLAUDE_CODE_REMOTE`, so a suite run inside a remote session starts every
  jig subprocess as remote. The serving e2e test takes the variable out with
  `env.without` itself; the other e2e tests that may be affected in future
  should do the same, or `environ` should drop it for all.
- `reviewTicket` (`cmd/jig/review.go`) is a general "positional or located
  ticket" helper with a review-only name and comment; `brief show` calls it.
  PH-1 and later verbs that take an optional ticket may want it renamed.
- `jig status`'s brief block says nothing about the illustration (whether the
  brief has one). PH-1 owns status; `illustration: yes|no` beside `approval:`
  would let the TUI show it without running `brief show --text`.
- `internal/store`'s `TestReadQuestionWhileItIsRewritten` failed once in a full
  run of the package on a loaded Windows machine (a read of a question file
  that is being rewritten: "cannot find the file specified"); it passed alone
  and on a rerun of the package. Not touched by this ticket.

## Deviations

None from the brief. Choices the brief left open: the `--text` block form
(above), `Order: 410` for the verb, the loopback-only `Host` check, the
composite hash for a brief with an illustration, and `CLAUDE_CODE_REMOTE`
counting as set when non-empty.
