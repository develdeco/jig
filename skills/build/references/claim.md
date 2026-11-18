A condition the claim does not meet (a blocker not published or merged, a stacked parent with no clean gate round, a brief nobody approved) prints as a warning, and the claim goes through. **Stop on any claim warning**: tell the person what it says and give the prompt that clears it (`ask:` in the warning, such as `Plan <T> (approve its brief)`), and release the slice unless they say to go on. Pass `--unattended` when nobody is watching: the claim is then refused with `CLAIM_BLOCKED_MERGED`, `CLAIM_STACKED_PARENT_NOT_CLEAN` or `CLAIM_NOT_APPROVED`. A slice another session holds needs `--take`.

To give a slice up before it is green, so another session can take it:

```
jig claim <ticket> --slice <slice> --release   # queued again, unless it is green
```
