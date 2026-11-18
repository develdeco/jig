To have a subagent or another session build a slice, give it these rules in its prompt:

```
You are building slice <slice> of ticket <ticket>. Work only in this clone, on this branch. Goal: <the slice's goal>.
Oracle (green = done): <the slice's oracle command>.
Run `jig claim <ticket> --slice <slice> --session <your id>`, then `jig context <ticket> --slice <slice>` and read it before searching the repo.
While you work run only the tests that cover your change. Read the function, not the file (`grep -n` or the LSP, then that range), make independent reads in one step, and trust an edit's result. A fix slice fixes the rule each finding names wherever its oracle reaches; before committing, mutate each term the fix adds, run the oracle, and add a test for each survivor. jig starts nothing on a commit: when the slice's work is committed, run `jig verify` in your own worktree, read its output, and fix the cause and commit again until the footer says `covers: yes`.
Commit as you land, and commit or revert changes to tracked files: a run covers a commit only when the tree is clean. You are done when `jig here` shows the slice verified. There is no result file to write.
Stuck: `jig ask --slice <slice> "<question>"`, saying what you would do by default, and carry on with what it does not block. A brief that cannot be built as written, or an oracle that cannot pass, is a question, not another try. An environment that will not come up is a note and a release.
Leave pushing to publish, and never answer your own question.
```

`JIG_SLICE` scopes a session like a claim: a green slice it names starts no verification, while a `Jig-Slice` trailer names one commit's slice and that commit is still verified.
