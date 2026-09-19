package edit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/codec"
)

// defaultYAMLIndent is used when a document declares no indentation of its own,
// as an empty one does, or declares one that cannot be reproduced.
const defaultYAMLIndent = 2

// ErrRootNotMapping is returned when the document's root node is not a mapping.
var ErrRootNotMapping = errors.New("root yaml node is not a mapping")

// ErrEmptyEncodedValue is returned when the value being written encodes to an
// empty document.
//
// The key would otherwise be left with no value to set.
var ErrEmptyEncodedValue = errors.New("encoded value produced an empty document")

// UpdateYAML returns data with value written at the dotted key, keeping the
// document's comments and its surrounding formatting.
//
// An existing key is updated in place and keeps its comments. A key that is
// absent is created, along with any missing parent mappings. An intermediate key
// that holds a value other than a mapping is replaced by a mapping.
//
// Keys are matched exactly first, with a case-insensitive fallback. The document
// keeps the indentation it was written with, which is measured from the source.
//
// It returns [codec.ErrMultipleDocuments] if the input carries more than one document,
// [ErrRootNotMapping] if the root node is not a mapping, and
// [ErrEmptyEncodedValue] if value encodes to an empty document.
func UpdateYAML(data []byte, dottedKey string, value any) ([]byte, error) {
	if err := ensureSingleYAMLDocument(data); err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml ast: %w", err)
	}

	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{
			{
				Kind: yaml.MappingNode,
			},
		}
	}

	rootMapping := doc.Content[0]
	if rootMapping.Kind != yaml.MappingNode {
		return nil, ErrRootNotMapping
	}

	valBytes, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value to yaml: %w", err)
	}

	var newValDoc yaml.Node

	if err := yaml.Unmarshal(valBytes, &newValDoc); err != nil {
		return nil, fmt.Errorf("parse encoded value: %w", err)
	}

	// NB: a value whose MarshalYAML emits an empty document encodes to nothing.
	if len(newValDoc.Content) == 0 {
		return nil, fmt.Errorf("%w: encoding %T produced an empty document", ErrEmptyEncodedValue, value)
	}

	newValNode := newValDoc.Content[0]

	parts := strings.Split(dottedKey, ".")
	if err := updateMappingNode(rootMapping, parts, newValNode); err != nil {
		return nil, err
	}

	detachMergeTags([]*yaml.Node{&doc})

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlSourceIndent(doc.Content, data))

	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode updated yaml: %w", err)
	}

	return buf.Bytes(), nil
}

// yamlSourceIndent reports the indentation step the document was written with, so
// an edit reproduces it rather than imposing one.
//
// The parser records the column each nested mapping starts at, so the step is
// readable from the tree. The step used most often is the one the document is
// written with; a section indented differently from the rest does not change it.
//
// A document that nests nothing, or that indents with tabs, reports the default.
func yamlSourceIndent(content []*yaml.Node, data []byte) int {
	counts := make(map[int]int)
	collectMappingColumns(content, 1, counts)
	// A step of one is a tab, which the encoder cannot emit as indentation, and a
	// step wider than the document is not an indentation at all. Map iteration is
	// unordered, so a tie is broken toward the narrower step.
	best, bestCount := 0, 0

	for step, count := range counts {
		if step <= 1 || step > len(data) {
			continue
		}

		if count > bestCount || (count == bestCount && best != 0 && step < best) {
			best, bestCount = step, count
		}
	}

	if best == 0 {
		return defaultYAMLIndent
	}

	return best
}

// detachMergeTags clears the tag the parser attaches to a YAML merge key.
//
// A parsed merge key carries an explicit "!!merge" tag, which the encoder then
// writes back out, turning "<<" into "!!merge <<". The tag tells a reader
// nothing and is not what the author wrote. Only a node the parser tagged as a
// merge is touched; a quoted "<<" is an ordinary string key and keeps its type.
func detachMergeTags(nodes []*yaml.Node) {
	for _, node := range nodes {
		if node == nil {
			continue
		}

		if node.Tag == "!!merge" {
			node.Tag = ""
			node.Style = 0
		}

		detachMergeTags(node.Content)
	}
}

// collectMappingColumns records the column every nested mapping starts at, keyed
// by the indentation step that column implies.
func collectMappingColumns(nodes []*yaml.Node, parentCol int, counts map[int]int) {
	for _, node := range nodes {
		if node == nil {
			continue
		}

		if node.Kind != yaml.MappingNode {
			collectMappingColumns(node.Content, parentCol, counts)

			continue
		}

		if parentCol > 0 && node.Column > parentCol {
			counts[node.Column-parentCol]++
		}

		collectMappingColumns(node.Content, node.Column, counts)
	}
}

// ensureSingleYAMLDocument rejects input carrying more than one YAML document.
//
// It returns [codec.ErrMultipleDocuments] when a second document decodes, or when the
// second decode fails for a reason other than end of input. A parse failure in
// the first document is wrapped and returned as is.
func ensureSingleYAMLDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		// Empty input decodes as no document at all; [UpdateYAML] treats that as
		// an empty mapping.
		if errors.Is(err, io.EOF) {
			return nil
		}

		return fmt.Errorf("parse yaml ast: %w", err)
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return codec.ErrMultipleDocuments
	}

	return nil
}

func updateMappingNode(mapping *yaml.Node, parts []string, newVal *yaml.Node) error {
	if len(parts) == 0 {
		return nil
	}

	currentKey := parts[0]
	isLeaf := len(parts) == 1

	matchIdx := findMatchingKeyIndex(mapping, currentKey)
	if matchIdx != -1 {
		vNode := mapping.Content[matchIdx+1]

		if isLeaf {
			newVal.HeadComment = vNode.HeadComment
			newVal.LineComment = vNode.LineComment
			newVal.FootComment = vNode.FootComment
			newVal.Anchor = vNode.Anchor
			mapping.Content[matchIdx+1] = newVal

			return nil
		}

		if vNode.Kind == yaml.MappingNode {
			return updateMappingNode(vNode, parts[1:], newVal)
		}

		// NB: the alias target's entries are copied into a new mapping; the
		// aliased mapping keeps its fields.
		if vNode.Kind == yaml.AliasNode && vNode.Alias != nil && vNode.Alias.Kind == yaml.MappingNode {
			newMap := cloneYAMLNode(vNode.Alias)
			mapping.Content[matchIdx+1] = newMap

			return updateMappingNode(newMap, parts[1:], newVal)
		}

		newMap := &yaml.Node{Kind: yaml.MappingNode}
		mapping.Content[matchIdx+1] = newMap

		return updateMappingNode(newMap, parts[1:], newVal)
	}

	kNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: currentKey,
	}

	if isLeaf {
		mapping.Content = append(mapping.Content, kNode, newVal)
		return nil
	}

	subMap := &yaml.Node{Kind: yaml.MappingNode}
	mapping.Content = append(mapping.Content, kNode, subMap)

	return updateMappingNode(subMap, parts[1:], newVal)
}

func findMatchingKeyIndex(mapping *yaml.Node, key string) int {
	// An exact match wins: YAML keys are case sensitive.
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}

	// Only when no key matches exactly is the comparison repeated
	// case-insensitively.
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if strings.EqualFold(mapping.Content[i].Value, key) {
			return i
		}
	}

	return -1
}

// cloneYAMLNode returns a deep copy of node and of every child it carries.
func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}

	clone := &yaml.Node{
		Kind:        node.Kind,
		Style:       node.Style,
		Tag:         node.Tag,
		Value:       node.Value,
		Anchor:      node.Anchor,
		Alias:       node.Alias,
		HeadComment: node.HeadComment,
		LineComment: node.LineComment,
		FootComment: node.FootComment,
	}

	if len(node.Content) > 0 {
		clone.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			clone.Content[i] = cloneYAMLNode(child)
		}
	}

	return clone
}
