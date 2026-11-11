Slices ready in several repos (`jig next <T>`): add this paragraph to the ticket subagent's prompt after its "Build it" line:

```
Slices ready in several repos (`jig next <T>`): build one repo yourself; start a nested builder per other repo in the background with this prompt, told to build only its repo's slices, never gate or nest (a hold saying to gate is yours: it hands back), and wait for them before the gate. Relay a builder's question: end your turn with the question, its id and default; resume it when resumed. No Agent tool: build repos in turn, hand back at green; I start the reviewer.
```

A nested builder's question reaches the person through its ticket subagent: put it to the person with its default, record it with `jig answer`, and resume the ticket subagent, which resumes the builder.
