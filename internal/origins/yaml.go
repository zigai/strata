package origins

import (
	"slices"

	"gopkg.in/yaml.v3"
)

type yamlWalker struct {
	emit func(Record)

	// active guards against alias cycles by holding the mappings on the current
	// path.
	active map[*yaml.Node]bool

	referenced map[*yaml.Node]bool
}

func readYAML(data []byte, emit func(Record)) bool {
	var document yaml.Node

	if err := yaml.Unmarshal(data, &document); err != nil {
		return false
	}

	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return false
	}

	walker := &yamlWalker{
		emit:       emit,
		active:     make(map[*yaml.Node]bool),
		referenced: make(map[*yaml.Node]bool),
	}
	collectAliasTargets(&document, walker.referenced)
	walker.walk(document.Content[0], "", false)

	return true
}

// Aliases are not followed, so ancestor aliases cannot make this walk recurse forever.
func collectAliasTargets(node *yaml.Node, targets map[*yaml.Node]bool) {
	if node.Kind == yaml.AliasNode {
		targets[node.Alias] = true

		return
	}

	for _, child := range node.Content {
		collectAliasTargets(child, targets)
	}
}

// Merge sources are expanded before explicit keys so the latter win.
// Template status is inherited by nested keys but not passed into merge sources:
// a merged key is a real setting at its new location.
func (w *yamlWalker) walk(node *yaml.Node, prefix string, template bool) {
	if node == nil || node.Kind != yaml.MappingNode || w.active[node] {
		return
	}

	w.active[node] = true
	defer delete(w.active, node)

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Tag == "!!merge" {
			w.merge(node.Content[i+1], prefix, template)
		}
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		if keyNode.Tag == "!!merge" {
			continue
		}

		fullKey := joinKey(prefix, keyNode.Value)
		valueTemplate := template || w.referenced[valueNode]

		if valueNode.Kind == yaml.MappingNode {
			w.walk(valueNode, fullKey, valueTemplate)

			continue
		}

		w.emit(Record{Key: fullKey, Line: keyNode.Line, RawValue: valueNode.Value, IsTemplate: valueTemplate})
	}
}

func (w *yamlWalker) merge(node *yaml.Node, prefix string, template bool) {
	switch node.Kind {
	case yaml.AliasNode:
		w.walk(node.Alias, prefix, template)
	case yaml.MappingNode:
		w.walk(node, prefix, template)
	case yaml.SequenceNode:
		for _, item := range slices.Backward(node.Content) {
			w.merge(item, prefix, template)
		}
	case yaml.DocumentNode, yaml.ScalarNode:
		return
	}
}
