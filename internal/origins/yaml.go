package origins

import (
	"gopkg.in/yaml.v3"
)

// readYAML reports whether data parsed as a YAML mapping and, if so, emits one
// Record per leaf key.
//
// YAML is the only format whose reader can report a line, because a decoded
// node carries the position it was read from.
func readYAML(data []byte, emit func(Record)) bool {
	var document yaml.Node

	if err := yaml.Unmarshal(data, &document); err != nil {
		return false
	}

	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return false
	}

	walkYAMLNode(document.Content[0], "", emit)

	return true
}

// walkYAMLNode emits one Record per leaf of a YAML mapping, recursing into
// nested mappings.
func walkYAMLNode(node *yaml.Node, prefix string, emit func(Record)) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		fullKey := joinKey(prefix, keyNode.Value)

		if valueNode.Kind == yaml.MappingNode {
			walkYAMLNode(valueNode, fullKey, emit)

			continue
		}

		emit(Record{Key: fullKey, Line: keyNode.Line, RawValue: valueNode.Value})
	}
}
