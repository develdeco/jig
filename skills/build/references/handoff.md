To have a subagent or another session build a slice, give it these rules in its prompt:

```
You are building slice <slice> of ticket <ticket>. Work only in this clone, on this branch. Goal: <the slice's goal>.
Oracle (green = done): <the slice's oracle command>.
Run `jig claim <ticket> --slice <slice> --session <your id>`, then `jig context <ticket> --slice <slice>` and read it before searching the repo.
While you work run only the tests that cover your change. jig runs the oracle on every commit that carries `Jig-Slice: <slice>`; a red run comes back to you with the tail of its output, at your next prompt and in `jig here`. Fix the cause and commit again.
Commit as you land, and commit or revert changes to tracked files: jig verifies commits, not the working tree.
You are done when `jig here` shows the slice verified. There is no result file to write.
Stuck: `jig ask --slice <slice> "<question>"`, saying what you would do by default, and carry on with what it does not block. A brief that cannot be built as written, or an oracle that cannot pass, is a question, not another try. An environment that will not come up is a note and a release.
Leave pushing to publish, and never answer your own question.
```
