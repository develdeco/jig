```
jig ask --slice <slice> --wait "<question>"   # waits (--timeout, 10m by default) and prints the answer
jig ask --slice <slice> --wait                # waits again on the question the slice has open
```

The answer says who it speaks for: `provenance: person` was typed by a person, `agent` is an agent relaying what a person said, so treat it as less sure. A `--wait` that runs out exits 2 and leaves the question open. Without it, carry on with what it does not block; the answer reaches you at your next prompt.
