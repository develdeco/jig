package project

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// FindProjectMarker is FindMarker for the verbs that act on the project as a
// whole: it walks up from start the same way, but passes over a member's
// marker (kind: member), which points up at the project and does not describe
// it, and goes on to the orchestrator's above.
func FindProjectMarker(start string) (dir string, m Marker, ok bool, err error) {
	for {
		dir, m, ok, err = FindMarker(start)
		if err != nil || !ok || m.Kind != KindMember {
			return dir, m, ok, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", Marker{}, false, nil
		}
		start = parent
	}
}

// SetMarkerRepos replaces the repos: list of dir's jig.yaml (removing the key
// when repos is empty), and touches nothing else in the file: other keys,
// comments and keys this jig version does not know stay as they were.
func SetMarkerRepos(dir string, repos []MarkerRepo) error {
	return editMarker(dir, func(root *yaml.Node) error {
		if len(repos) == 0 {
			removeKey(root, "repos")
			return nil
		}
		var n yaml.Node
		if err := n.Encode(repos); err != nil {
			return fmt.Errorf("project: encode repos: %w", err)
		}
		setKey(root, "repos", &n, "kind", "store", "project", "jig")
		return nil
	})
}

// SetMarkerStore sets the store: key of dir's jig.yaml, the store's remote
// (removing the key when store is empty), and touches nothing else in the file.
func SetMarkerStore(dir, storeRemote string) error {
	return editMarker(dir, func(root *yaml.Node) error {
		if storeRemote == "" {
			removeKey(root, "store")
			return nil
		}
		setKey(root, "store", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: storeRemote}, "project", "jig")
		return nil
	})
}

// editMarker reads dir's jig.yaml as a YAML tree, lets fn change it, and writes
// it back atomically. Editing the tree rather than decoding into Marker and
// encoding again keeps what Marker does not hold: a newer jig's keys, and the
// person's comments.
func editMarker(dir string, fn func(root *yaml.Node) error) error {
	path := filepath.Join(dir, MarkerFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("project: read marker: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("project: parse marker %s: %w", path, err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("project: marker %s is not a YAML mapping", path)
	}
	if err := fn(doc.Content[0]); err != nil {
		return err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("project: marshal marker: %w", err)
	}
	if err := store.AtomicWrite(path, out); err != nil {
		return fmt.Errorf("project: write marker: %w", err)
	}
	return nil
}

// keyIndex returns the index of key's own node in mapping's Content (its value
// sits right after), or -1.
func keyIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// setKey sets mapping's key to value: in place when the key is there, else
// right after the first of after that is, else at the end.
func setKey(mapping *yaml.Node, key string, value *yaml.Node, after ...string) {
	if i := keyIndex(mapping, key); i >= 0 {
		mapping.Content[i+1] = value
		return
	}
	pair := []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value}
	for _, a := range after {
		if i := keyIndex(mapping, a); i >= 0 {
			at := i + 2
			mapping.Content = append(mapping.Content[:at], append(pair, mapping.Content[at:]...)...)
			return
		}
	}
	mapping.Content = append(mapping.Content, pair...)
}

// removeKey takes key and its value out of mapping.
func removeKey(mapping *yaml.Node, key string) {
	if i := keyIndex(mapping, key); i >= 0 {
		mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
	}
}
