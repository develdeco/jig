# Brief: file helpers

Add a `store` package with `ReadAll(path string) ([]byte, error)` that reads
the full contents of a read-only file, and `WriteAll(path string, data
[]byte) error` that creates or truncates a file and writes data to it.
