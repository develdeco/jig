# Brief: user lookup

Add a `users` package with a `Lookup(id string) string` function that returns
the display name for a known id, and an empty string for an unknown id. The
lookup table is a small in-memory map populated at startup; no persistence
or network calls are needed.
