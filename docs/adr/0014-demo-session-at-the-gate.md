# A demo session at the gate records the change working

What a person reviewing a change trusts most is seeing it work: a screenshot
when one frame shows it, a GIF or a video when it is a flow. A reviewer
session reads a diff and reports what is wrong with it; it does not produce
that. jig owns structure, bookkeeping and enforcement, and judgment belongs
to a model or a human, so the split is the one the reviewer already has: a
session decides what shows this change working and how to record it, and jig
decides where its output goes, what it may contain, and whether it is
accepted.

After a clean reviewer round, `jig gate` dispatches a demo session and
records what it produced. This ADR fixes that contract, what makes it best
effort, and where the media live. Publishing the media on the pull request is
the next change: nothing here attaches anything to a PR yet.

## When a demo runs

A demo runs after a reviewer round whose verdict is clean, once everything
the round wrote is committed and pushed, unless a demo is already recorded
for that reviewed head. `jig gate --no-demo` skips it, and `jig solve` passes
the flag through. The scripted source (`--scenario` with no `--backend`)
never runs one, and structurally: the demo dispatch is a `Demo` method only
the reviewer source has, and `Gate` asks for it by interface, so a source
that cannot dispatch a session is never asked.

**One demo per reviewed head.** A round looks at every earlier round's
`gate/round-N/demo.yaml` and runs no demo when one with status `recorded`
names the same head sha; its report says `demo: existing` and which round
holds it. A head is what a demo is about: a rerun of `jig gate` on an
unchanged head, or a round that reviewed nothing new, does not pay for a
second session, and a new head gets its own demo. The demo shows the whole
change, not the round's own delta: its base is the merge base with the
target, whatever scope the review used.

**Best effort.** A demo never makes a clean round unclean. `Gate` treats it as
a step after the round, not part of it: the round's verdict, report,
findings and journal are already written and pushed, and nothing the demo
does can change them. A demo that fails, or whose output jig refuses, is
recorded as `status: refused` with a one-line reason in the round's
`demo.yaml`, shown in the gate report beside the verdict (`demo: refused`,
`demo_reason: ...`), and never raised as an error. When the demo session or its
backend failed, that reason is a failure code, and the failure's own text is in
the report alone (`demo_detail: ...`), as "What jig verifies" says. A refused demo is not the
head's demo: the next clean round on that head runs it again, so a transient
failure is retried by simply gating again. Only a recorded demo, even one
with no media, ends the retries for its head.

## The contract

Disk only, like the reviewer's (ADR 0004 and 0007). jig writes `demo.json`:
the ticket, the round, the base and head sha, the round's resolved intent
(`{source, path}`, ADR 0012: the very one the reviewer was handed, an inferred
one included), `media_dir` (an absolute path), and `limits`: the
image extensions (png, jpg, jpeg, gif, webp, svg) and video extensions (mp4,
mov, webm) that `gh ... --attach` accepts, images at most 10 MiB, videos at
most 100 MiB, at most 50 files. It writes it beside the media directory,
`<jig home>/evidence/<store id>/<ticket>/<head sha>.demo.json`, not in the
store's `work/` with the reviewer's input: `media_dir` is a path of this
machine that names the operator's jig home, and the store is committed,
pushed and often shared. Nothing jig itself writes for a demo in the store
names the jig home: its files and hashes are names and numbers, and its
reasons name the directories by role (see below), except that a result
key jig does not recognize is quoted as the session wrote it: that key is
the session's own text, like the result file that holds it. The session writes
`work/gate.round-N.demo.result.json`, which holds names, captions and a summary,
its own words:

```json
{"media": [{"file": "<name in media_dir>", "caption": "..."}], "summary": "..."}
```

An empty `media` list with a `summary` saying why nothing is visible is a
valid result, and a recorded one. The result is parsed as strictly as the
reviewer's: exactly one JSON object, no repeated or case-variant key, only
the recognized keys, `media` present and a list, a non-empty `summary`, and a
file and a caption on every entry.

The session's own words are recorded as it wrote them: the result file, and the
`summary` and captions that `demo.yaml` copies from it. jig does not filter or
rewrite model prose, so a session that repeats a path it was told, `media_dir`'s
for one (it sits under the jig home), has it recorded, the same way the
reviewer's `result.json` summary already is. What jig guarantees about host
paths is about what jig writes, not about what a model says. Those words are
not bounded either, unlike a reason, which is jig's own text and is capped at
400 characters: the store's history carries the reviewer's words the same way.
The one file jig does not keep there is a backend's: when a session wrote no
result, headless and herdr write one of their own in its place, with the
slice result's shape, and it is removed (see the reason below).

The prompt states the job (show a person reviewing this change that it
works, in whatever form shows it best, or record nothing and say why), the
output contract, and what jig verifies. It names no tool and lists no kind of
change. A repo documents its own demo tooling in its own `CLAUDE.md`, which a
headless session already receives (ADR 0008); jig has no opinion on how a
screenshot is taken. The sentence that says what each intent source means is
the reviewer prompt's own (`intentSourcesPrompt`), written once, so a source jig
gains is described to both sessions or to neither.

## What jig verifies

After the dispatch, jig checks, and refuses the whole result on the first
check that fails. It never partially accepts: a result with one bad file
records none of its files.

- **The result is complete.** A non-empty `summary`, and a non-empty caption
  on every file (the parse rules above); the prompt says so.
- **The lease is unchanged.** The gate lease's HEAD and tracked tree are as
  they were, by the same guard the reviewer round uses (`leaseChanged`).
  Untracked scratch is allowed, and the lease is restored pristine afterwards
  whatever happened. A lease that moved is a refused demo.
- **`media_dir` is the directory jig made,** a plain directory, not a link or
  junction a session swapped in. jig reads the identity of the directory it
  made before the session starts, and the check compares it with what
  `media_dir` names afterwards, so a directory above it swapped for a link,
  which a plain-directory check follows without noticing, is refused too. A
  swap of one of those directories that stays is refused before the next attempt
  touches anything: it checks that the store id and ticket directories above
  the head's, which jig made, are plain directories before clearing or making
  anything below them, and refuses the demo, without dispatching a session,
  when one is a link or junction. `<jig home>/evidence` itself is the
  operator's own and is trusted as such: an operator may link it elsewhere on
  purpose, so a link there is followed.
- **Every listed file is a non-empty regular file directly inside
  `media_dir`.** The name is a plain file name (no separator of either
  spelling, no drive or stream colon, no Windows reserved device name, listed
  once, and not differing from another only by case), and `Lstat` must call
  it regular: a symlink is a symlink, a subdirectory a directory, and a
  Windows junction reads as `ModeIrregular`, so none is ever followed. It is
  not empty, since `gh ... --attach` refuses an empty file. The bytes are
  hashed from the very file `Lstat` saw, and a swap or growth in between is
  refused. Both identity checks read the file id when the `Lstat` is taken
  (`lstatPinned`), because on Windows `os.SameFile` otherwise reads it from
  the path when it compares, after the swap it exists to catch. Hard links are
  not detected: one is a regular file, the same as a copy the session could
  have made itself.
- **Type and size.** The extension, lowercased, is an allowed image or video
  type, and the size is within that kind's limit; at most 50 files.

jig then renames the accepted files `demo-<n>.<ext>`, `n` from 1 in the order
the session listed them, so the names that later reach a PR are jig's, not
the session's. It stages every file under a temporary name first, so a
session that used the final names for other files cannot make one rename
overwrite another's source, and it refuses beforehand when an unlisted file
already holds a final name. Everything else in `media_dir` is then removed (a
file the result did not list, a subdirectory, a link, which is removed as
itself and never followed), so the directory holds exactly the files
`demo.yaml` lists. A passing result becomes `gate/round-N/demo.yaml`:

```yaml
status: recorded
head_sha: <the reviewed head>
summary: <the session's summary>
media:
  - name: demo-1.png
    sha256: <hex>
    size: 1234
    caption: <the session's caption>
```

and anything else becomes `status: refused`, `head_sha`, and a `reason`: one
line of at most 400 characters. There is no separate failed status. What the
reason holds depends on who refused the demo, and the store is committed and
pushed, so the two are kept apart:

- **The demo session or its backend failed** (the dispatch returned an error,
  or the session wrote no demo result): the reason is `the demo session failed:`
  and the failure's code, and nothing else. The code is the one a store commit
  subject carries for a failed gate round (`failureCode`): the error's own code
  where it has one (`SESSION_TIMEOUT`, `SCREEN_UNAVAILABLE`), `INTERNAL` where
  it has none, and `DEMO_NO_RESULT` for a session that wrote no result. The
  text of that failure is not jig's, and can hold anything: the arguments a
  backend echoed (for herdr on Windows, the whole prompt with every path of the
  dispatch spelled as a WSL mount), a stderr tail, a path of its own. So it goes
  to the terminal only: the gate report prints it once (`demo_detail`), and
  nothing records it. When a session wrote no result, headless and herdr write
  one of their own in its place, a JSON object with an `outcome` field (the
  slice result's shape), and its summary can name the paths of the tool calls
  that were denied. That file is not a demo result: jig takes the field, not any
  wording, to mean the session wrote none, so a demo result whose words mention
  an outcome is unaffected (and a file a session wrote with such a field is
  indistinguishable from the backend's, and taken the same way), and it removes
  the file from the store's `work/` before the demo's own store push. A file
  a failed dispatch left there is removed the same way. (A backend that wrote nothing in place of a result is the same
  failure, without the file to remove.)
- **jig refused what the session wrote**: a result that is not the contract, a
  lease that moved, a file that is not what the contract says. The reason is
  jig's own words, composed from names and numbers, and it names no path the
  session chose: the session is told `media_dir`'s absolute path and may list a
  file by it, in whatever spelling its backend gave it (a herdr session on
  Windows was told the WSL one), and jig cannot know every spelling to leave out,
  so a reason names a listed entry by its index and the last element of the
  string, and a name that is not a plain file name is refused as `media entry 1
  ("shot.png") is not a plain file name directly inside media_dir`. It can also
  quote the operating system's or git's message about an operation of jig's own
  (a directory it could not clear or make, the lease it could not restore), which
  names the path it failed on. So before a reason is recorded or printed jig
  replaces the media directory, the jig home and the store's own directory with
  `media_dir`, `<jig home>` and `<store>` (`leaveOutHostPaths`), for every message
  alike, in the spelling as printed, with forward slashes and Go-quoted. Those
  are the paths jig chose; jig cannot promise more of a message it did not write.

The record is what makes the media discoverable and verifiable on a machine that
has them: the hashes let a later reader tell a file that is the recorded one from
one that is not.

## Where the media live

Under the jig home, at `<jig home>/evidence/<store id>/<ticket>/<head sha>/`
(`home.EvidenceDir`): not in the store's git, and not in a lease.

This is a deliberate fifth home, beside the four `ARCHITECTURE.md` names, and
the reason is size. The store is a long-lived repo every clone carries whole,
history included; one 100 MiB video committed to it stays in every clone
forever, and the store's push after every command would carry it. A lease is
rewound and cleaned between rounds, and lives per role and ticket, not per
head. What invalidates a demo is a new reviewed head, and what can lose it is
the machine, so it lives with the machine, like the pool. What must survive
across machines is small and lives in the store: `demo.yaml`.

The store id is the first 16 hex digits of the sha256 of the store's root,
absolute and with symlinks resolved (`Store.ID`; Windows spells the resolved
path in its on-disk case), so one clone reached by a relative path, a symlink
or another spelling names one id. A Windows junction is not resolved, so a
clone reached through one has its own id. It is derived from the path rather
than stored or taken from a name: a project name is neither unique nor a safe
path component, a remote URL does not exist for a standalone store, and a
recorded id would have to travel with a clone that does not want to carry
machine-local state. The cost is that a clone moved to another path, or
reached through a junction, is a new id, and its media are not found under the
old one; a reader finding the manifest but not the files must say so rather
than drop them silently. The evidence directory is jig-owned scratch until a
demo is accepted: an attempt starts by clearing it (which removes a link a
session left, never what it pointed at), so a retried head never mixes files
from two attempts. Once a demo is accepted it holds exactly the files
`demo.yaml` lists.

## Backends

`session.Dispatch` gains one field, `ExtraWriteDir`: one absolute directory
outside the worktree that the session may also write in. Media come from
shell tools, which the screen governs wherever one attaches (headless alone;
herdr sessions are not screened), so the field widens only the edit tools.

- **headless:** one more path-scoped edit rule, `Edit(<dir>/**)`, in every
  spelling the CLI may check (the resolved form, and the long spelling of a
  Windows 8.3 short name), beside the rules for the worktree and the result
  file. Nothing else changes; every other path stays closed.
- **fake:** plays back the scenario's `gate/round-N/demo-result.json` verbatim
  and copies every file in `gate/round-N/demo-media/` into the directory, as a
  real session writes its media there. A round the scenario did not script is
  an error, never a silent "nothing to show".
- **herdr:** scopes no edits of its own, so the field needs no grant and is
  not passed to herdr as an argument of its own. Off Windows the path reads
  the same inside and out. On Windows the session runs in WSL, so herdr
  creates the workspace at the worktree's `/mnt` spelling and rewrites the
  prompt's own mentions of every path of the dispatch (the worktree, the input
  file, the result file and this directory) to their `/mnt` spelling, the way
  headless rewrites its prompt to the long spelling of a path. The same
  rewrite applies to every herdr dispatch on Windows, not only a demo. The
  files jig itself wrote keep the host spelling of the paths they hold
  (`demo.json`'s `media_dir`, like `review.json`'s paths), and jig reads the
  result at its host path. A herdr command that fails is named by its
  subcommand and herdr's own stderr (`herdr agent prompt: exit status 1: ...`),
  never by its operands, which are the prompt and every path in it, spelled as
  `/mnt` paths on Windows.

## Known limit

Env classes are torn down after the oracles, before the reviewer or the demo
dispatch (`runGateOracles`), so a demo that needs one cannot bring it up: it
reports that it cannot record, and the demo is recorded with no media and
that summary. Keeping an env class up for a demo is a follow-up.

## Consequences

- A gate on a clean round can run a second session, so `--no-demo` exists,
  and the demo does not hold the round's verdict hostage: it runs after the
  round is pushed, and a failed one costs at most one session per clean gate
  on the same head.
- The store gains two small files per demoed round (`demo.yaml` and the
  session's result), and the machine gains media it never garbage-collects,
  with the `demo.json` beside each. Old heads' directories are the operator's
  to prune.
- The session's summary and captions, and its result file, are recorded as
  written and unbounded, like the reviewer's `result.json`: a session that
  writes a long summary, or a path it was told, has both in the store's
  history. jig bounds and cleans what it writes, not what a model says.
- Publishing will read `demo.yaml` for the head it ships, check the hashes, and
  attach the media, and a machine without them will report that. That is the
  next change; nothing attaches yet.
