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

gitx runs git on the store in process, through go-git (`gitx.Repo`):
status, staging and committing, and pushing to and fetching from a bare
remote at a local path. A local push or fetch walks both histories back
only to where they last met and copies what each new commit changed, so
its cost follows the commits since the last sync, not the length of the
store's history, and no pack protocol runs. It writes the way the git
program does where another process could be writing too: every ref moves
through git's own lock file, renamed over the ref, and only from the value
it was read at, so a push that raced another git process is rejected
rather than lost; a commit holds the index's and the branch's lock files
while it runs; and a lock another process holds hands that step to the
git program, which reports it as it always has. After a push it starts
the remote's automatic maintenance, as the git program's receive-pack
would. gitx stays the single owner of git execution: no other package may
import go-git, which a lint test enforces as it does for spawning the git
program.

The git program keeps everything where a user's own git setup must apply,
or where go-git would do something different. That covers the working
clones and leases jig builds in and the pushes publish makes, which
depend on hooks, clean and smudge filters (Git LFS), credential helpers
and the user's config. It covers network remotes, with their credentials
and SSH configuration. It covers the steps go-git lacks or does
differently: rebasing a store whose history diverged, with its conflict
handling, and checking out a fast-forward, where the git program keeps
untracked and ignored files and refuses to overwrite them (go-git's reset
removes them). Each in-process method returns `gitx.ErrUseCLI` when its
case is one of these, and the store runs the git program for that step
alone.

What the store and its remote declare themselves is honored the same way,
by handing the step to the git program: hooks of their own (anything but
git's samples in the hooks directory, or a `core.hooksPath` in their own
config), a shallow or grafted history, alternates, a repository format
extension, group-shared permissions, a push URL or custom receive-pack,
URL rewriting or a fetch refspec other than the default, objects checked
on transfer, a merge in the history, or an index that tracks anything but
regular files. Line endings are part of this: a store is worked on in
process only when it rules out conversion itself, with a root
`.gitattributes` of exactly `* -text`, which jig now writes into every
store it creates, or with no attributes and its own `core.autocrlf=false`.
go-git converts nothing, and Git for Windows installs with
`core.autocrlf=true`, so a store that says nothing could be committed
with CRLF working files the git program would have normalized. None of
these decisions reads the machine's own git config, so a store behaves the
same on every host. An existing store without the attributes keeps the git
program, as before.

What the machine's config would add is the one accepted difference,
because the store's commits are jig's bookkeeping, not the user's work.
In-process store writes do not run a machine-wide `core.hooksPath`, are
not signed, and honor the store's own `.gitignore` and `info/exclude`,
not a user's global excludes file. They write no reflog.
