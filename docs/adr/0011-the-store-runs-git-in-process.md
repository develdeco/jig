# The store runs git in process; users' repositories run the git program

jig's store is a git repository jig writes at every checkpoint: a slice
queued or changing state, a gate round, a publish. Through the git
program, one store write is nine processes (the checks for an unfinished
rebase or merge, the branch, `add`, `commit`, the remote, `push`, and
maintenance), and a push to a local remote starts four or five more on the
far side. A process is cheap on Linux and expensive on Windows: starting
an empty program takes 6-8 ms there against about 1 ms, and every git
operation costs 10-25 times what it costs on Linux. jig is meant to behave
the same on Windows, Linux and macOS, so the store stops paying for
processes.

gitx runs git on the store in process (`gitx.Repo`). go-git reads and
writes its objects and refs; staging, committing and moving refs are
gitx's own, done the way the git program does them, because go-git's
work-tree layer re-reads the whole HEAD tree on every status and checks
every path it touches for symbolic links, so its cost grew with the
store's history and size. A commit works from the index: a tracked file
whose size and modification time still match its entry is not read, and
one whose time is not safely before the index's is read again, as the git
program's racy-git check does; only changed and new files are hashed;
untracked files are filtered through the store's own `.gitignore` files
and `info/exclude`; and trees are built from the index entries, written
only along the paths that changed. A push to or fetch from a bare remote
at a local path walks both histories back only to where they last met and
copies what each new commit changed. So a store write costs what changed
and one listing of each directory, never the length of the history, and
no pack protocol runs.

It writes the way the git program does wherever another process could be
writing too. The index and the branch are written into git's own lock
files and renamed over the originals, never rewritten in place; a ref on
the remote moves the same way, and only from the value it was read at, so
a push that raced another git process is rejected rather than lost; and a
lock another process holds hands that step to the git program, which
reports it as it always has. After a push it starts the remote's
automatic maintenance, as the git program's receive-pack would. gitx stays
the single owner of git execution: no other package may import go-git,
which a lint test enforces as it does for spawning the git program.

The git program keeps everything where a user's own git setup must apply,
or where the git program would do something different. That covers the
working clones and leases jig builds in and the pushes publish makes,
which depend on hooks, clean and smudge filters (Git LFS), credential
helpers and the user's config. It covers network remotes, with their
credentials and SSH configuration. It covers rebasing a store whose
history diverged, with its conflict handling, and checking out a
fast-forward, where the git program keeps untracked and ignored files and
refuses to overwrite them. Each in-process method returns `gitx.ErrUseCLI`
when its case is one of these, and the store runs the git program for
that step alone.

What the store and its remote declare themselves is honored the same way,
by handing the step to the git program: hooks of their own (anything but
git's samples in the hooks directory, or a `core.hooksPath` in their own
config), a shallow or grafted history, alternates, a repository format
extension, a split or sparse index, their own excludes or attributes
file, config included from another file, group-shared permissions, a push
URL or custom receive-pack, URL rewriting or a fetch refspec other than
the default, objects checked on transfer, a remote that is not bare, a
merge in the history, and a work tree or index the git program would
stage differently from a plain file's bytes: a symbolic link, an
executable where the store's `core.fileMode` counts it, an embedded
repository, a name `core.ignoreCase` or `core.precomposeUnicode` would
match differently, or an index go-git cannot read (the git program writes
one when a machine's config sets `index.skipHash` or a split index).

Conversion of file contents is part of this. A store is worked on in
process only when its root `.gitattributes` is exactly
`* -text -filter -ident -working-tree-encoding`, which jig now writes into
every store it creates: it unsets every attribute that changes the bytes
stored or checked out, and a repository's own attributes win over the
machine's attributes file, so the git program converts nothing either. A
`.gitattributes` further down hands the commit to the git program. None of
these decisions reads the machine's own git config, so a store behaves the
same on every host. An existing store without the attributes keeps the git
program, as before.

What the machine's config would add is the one accepted difference,
because the store's commits are jig's bookkeeping, not the user's work.
In-process store writes do not run a machine-wide `core.hooksPath`, are
not signed, and honor the store's own `.gitignore` files and
`info/exclude`, not a user's global excludes file. They write no reflog.
