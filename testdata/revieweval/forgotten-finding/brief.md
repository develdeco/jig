# Brief: inventory helpers

Add an `inventory` package with `CountAvailable(items []Item) int` that
returns how many items are not reserved, and `WriteReport(path string,
items []Item) error` that writes a one-line processed-count summary to
path.
