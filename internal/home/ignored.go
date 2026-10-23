package home

import "path/filepath"

// IgnoredPath returns the file under the jig home root that lists the
// directories jig must not track (`jig ignore`): a clone of a known project
// that this machine does not want mapped.
func IgnoredPath(root string) string {
	return filepath.Join(root, "ignored.yaml")
}
