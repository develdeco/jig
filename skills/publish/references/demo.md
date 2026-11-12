When the ticket's own tests recorded scenarios and nobody picked, `--body` and `jig publish` report `picks: pending` and name a `picks_request` file: the candidates, each with an id, and the intent. Pick the recordings that show the change, compose them into flows, and write the answer:

```
{"flows": [{"title": "...", "items": [{"id": "r2"}, {"id": "r3", "caption": "..."}]}], "summary": "..."}
```

then run `jig publish T-1 --body --picks answer.json` (or the bare publish with `--picks`). Pick a few; an id is used once; flows keep their step order. A refused pick publishes with no demo and says why; a pick that stood is reused for the same head and recordings.
