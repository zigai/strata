package origins

import (
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type yamlWalker struct {
	emit func(Record)

	// active guards against alias cycles by holding the mappings on the current
	// path.
	active map[*yaml.Node]bool

	referenced map[*yaml.Node]bool
}

func readYAML(data []byte, emit func(Record)) {
	var document yaml.Node

	if err := yaml.Unmarshal(data, &document); err != nil {
		return
	}

	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return
	}

	walker := &yamlWalker{
		emit:       emit,
		active:     make(map[*yaml.Node]bool),
		referenced: make(map[*yaml.Node]bool),
	}
	collectAliasTargets(&document, walker.referenced)
	walker.walk(document.Content[0], nil, false)
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
func (w *yamlWalker) walk(node *yaml.Node, prefix []string, template bool) {
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

		segments := append(slices.Clone(prefix), keyNode.Value)
		valueTemplate := template || w.referenced[valueNode]
		w.value(valueNode, segments, keyNode.Line, valueTemplate)
	}
}

func (w *yamlWalker) value(node *yaml.Node, segments []string, line int, template bool) {
	if node == nil || w.active[node] {
		return
	}

	if node.Kind == yaml.AliasNode {
		w.active[node] = true
		defer delete(w.active, node)

		w.value(node.Alias, segments, line, template)

		return
	}

	if node.Kind == yaml.MappingNode && len(node.Content) > 0 {
		w.walk(node, segments, template)
		return
	}

	w.emit(Record{Key: strings.Join(segments, "."), Segments: segments, Line: line, RawValue: yamlRawValue(node), IsTemplate: template})

	if node.Kind != yaml.SequenceNode {
		return
	}

	w.active[node] = true
	defer delete(w.active, node)

	for index, item := range node.Content {
		if item.Kind == yaml.MappingNode || item.Kind == yaml.SequenceNode || item.Kind == yaml.AliasNode {
			w.value(item, append(slices.Clone(segments), strconv.Itoa(index)), item.Line, template)
		}
	}
}

func yamlRawValue(node *yaml.Node) string {
	if node.Kind != yaml.SequenceNode && node.Kind != yaml.MappingNode {
		return node.Value
	}

	encoded, err := yaml.Marshal(node)
	if err != nil {
		return node.Value
	}

	return strings.TrimSpace(string(encoded))
}

func (w *yamlWalker) merge(node *yaml.Node, prefix []string, template bool) {
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
