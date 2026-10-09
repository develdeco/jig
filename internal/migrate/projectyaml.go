package migrate

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// RewriteProjectYAML rewrites data, a v1 project.yaml, onto schema 2
// (brief.md#The migration step 6): schema_version becomes 2; ticket_format
// is removed, replaced in its own slot by keys: (sorted, for a stable
// diff); tracker: becomes trackers: in its own slot, "local" becoming []
// and any other value (a store that had already adopted a trackers: list
// of its own, with no tracker: key, is untouched here - this branch covers
// only a project.yaml that still carries the older key) carried over as it
// was; every other key is left exactly as it is, comments included, since
// this edits the node tree rather than re-marshaling a Go struct.
func RewriteProjectYAML(data []byte, keys map[string]string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("migrate: parse project.yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("migrate: project.yaml is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("migrate: project.yaml's root is not a mapping")
	}

	keysNode := keysMappingNode(keys)
	ticketFormatIdx := findKey(root, "ticket_format")
	if ticketFormatIdx >= 0 {
		setPairAt(root, ticketFormatIdx, "keys", keysNode)
	} else if existing := findKey(root, "keys"); existing >= 0 {
		setPairAt(root, existing, "keys", keysNode)
	} else {
		appendPair(root, "keys", keysNode)
	}

	if trackerIdx := findKey(root, "tracker"); trackerIdx >= 0 {
		valueNode := root.Content[trackerIdx+1]
		newValue := valueNode
		if valueNode.Kind == yaml.ScalarNode && valueNode.Value == "local" {
			newValue = emptySequenceNode()
		}
		setPairAt(root, trackerIdx, "trackers", newValue)
	}

	if schemaIdx := findKey(root, "schema_version"); schemaIdx >= 0 {
		root.Content[schemaIdx+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "2"}
	} else {
		appendPair(root, "schema_version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "2"})
	}

	return yaml.Marshal(&doc)
}

// findKey returns the index of key's own node in mapping's Content (its
// value sits right after, at index+1), or -1 when mapping has no such key.
func findKey(mapping *yaml.Node, key string) int {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// setPairAt replaces the key and value node at mapping.Content[i:i+2].
func setPairAt(mapping *yaml.Node, i int, key string, value *yaml.Node) {
	mapping.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	mapping.Content[i+1] = value
}

// appendPair appends a fresh key/value pair to mapping's Content.
func appendPair(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
}

// emptySequenceNode is a flow-style empty sequence, which yaml.v3 renders
// as "[]".
func emptySequenceNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
}

// keysMappingNode builds the keys: mapping node for keys, sorted so the
// written file is deterministic.
func keysMappingNode(keys map[string]string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Value: keys[k]},
		)
	}
	return node
}
