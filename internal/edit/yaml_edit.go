package edit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/codec"
)

const (
	defaultYAMLIndent = 2

	quotedStyles = yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle
	blockStyles  = yaml.LiteralStyle | yaml.FoldedStyle
)

var ErrRootNotMapping = errors.New("root yaml node is not a mapping")

var ErrEmptyEncodedValue = errors.New("encoded value produced an empty document")

type scalarPosition struct {
	InFlow bool
	IsKey  bool
}

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

	newValNode, err := yamlValueNode(value)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(dottedKey, ".")
	if err := updateMappingNode(rootMapping, parts, newValNode); err != nil {
		return nil, err
	}

	detachMergeTags([]*yaml.Node{&doc})

	indent := yamlSourceIndent(doc.Content, data)
	settleScalarStyles(&doc, indent)

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)

	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode updated yaml: %w", err)
	}

	return buf.Bytes(), nil
}

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

// yaml.v3 writes an explicit merge tag back out as "!!merge <<". Only merge
// keys lose the tag; a quoted "<<" is an ordinary string key and keeps its type.
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

func ensureSingleYAMLDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
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
			newMap := copyAliasedMapping(vNode.Alias)
			mapping.Content[matchIdx+1] = newMap

			return updateMappingNode(newMap, parts[1:], newVal)
		}

		// The replaced value keeps its anchor, as a replaced leaf does, so an
		// alias of it still names a node.
		newMap := &yaml.Node{Kind: yaml.MappingNode, Anchor: vNode.Anchor}
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

	// A mapping inherited through a merge key is copied in whole, so the edit
	// overrides one key and the inherited siblings survive.
	subMap := &yaml.Node{Kind: yaml.MappingNode}
	if inherited := mergedMappingValue(mapping, currentKey, map[*yaml.Node]bool{}); inherited != nil {
		subMap = copyAliasedMapping(inherited)
	}

	mapping.Content = append(mapping.Content, kNode, subMap)

	return updateMappingNode(subMap, parts[1:], newVal)
}

// Merge precedence is own keys before merges, then earlier sequence entries
// before later ones.
func mergedMappingValue(mapping *yaml.Node, key string, seen map[*yaml.Node]bool) *yaml.Node {
	for _, source := range mergeSources(mapping) {
		found, ok := inheritedValue(source, key, seen)
		if !ok {
			continue
		}

		if found.Kind == yaml.AliasNode {
			found = found.Alias
		}

		if found == nil || found.Kind != yaml.MappingNode {
			return nil
		}

		return found
	}

	return nil
}

func inheritedValue(source *yaml.Node, key string, seen map[*yaml.Node]bool) (*yaml.Node, bool) {
	if source.Kind == yaml.AliasNode {
		source = source.Alias
	}

	if source == nil || source.Kind != yaml.MappingNode || seen[source] {
		return nil, false
	}

	seen[source] = true

	if idx := findMatchingKeyIndex(source, key); idx != -1 {
		return source.Content[idx+1], true
	}

	for _, next := range mergeSources(source) {
		if found, ok := inheritedValue(next, key, seen); ok {
			return found, true
		}
	}

	return nil, false
}

func mergeSources(mapping *yaml.Node) []*yaml.Node {
	var sources []*yaml.Node

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].ShortTag() != "!!merge" {
			continue
		}

		if value := mapping.Content[i+1]; value.Kind == yaml.SequenceNode {
			sources = append(sources, value.Content...)
		} else {
			sources = append(sources, value)
		}
	}

	return sources
}

// The copy defines no anchors: re-declaring one would redirect later aliases
// to the edited copy. Its aliases still name the original anchors.
func copyAliasedMapping(mapping *yaml.Node) *yaml.Node {
	clone := cloneYAMLNode(mapping)
	dropAnchors(clone)

	return clone
}

func dropAnchors(node *yaml.Node) {
	node.Anchor = ""
	for _, child := range node.Content {
		dropAnchors(child)
	}
}

func findMatchingKeyIndex(mapping *yaml.Node, key string) int {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}

	return -1
}

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

// yaml.v3 drops a leading line break when it round-trips a multi-line string,
// so strings never pass through the encoder text. settleScalarStyles picks
// a style that preserves the value.
func yamlValueNode(value any) (*yaml.Node, error) {
	if s, ok := value.(string); ok {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}, nil
	}

	valBytes, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value to yaml: %w", err)
	}

	var newValDoc yaml.Node

	if err := yaml.Unmarshal(valBytes, &newValDoc); err != nil {
		return nil, fmt.Errorf("parse encoded value: %w", err)
	}

	if len(newValDoc.Content) == 0 {
		return nil, fmt.Errorf("%w: encoding %T produced an empty document", ErrEmptyEncodedValue, value)
	}

	return newValDoc.Content[0], nil
}

// yaml.v3 changes empty nulls in flow collections or keys into empty strings.
// Some block scalars also gain or lose line breaks. Rewrite those scalars into
// a style that preserves their value, probing the whole document in batches.
func settleScalarStyles(doc *yaml.Node, indent int) {
	var blocks []*yaml.Node

	collectBlockScalars(doc, scalarPosition{InFlow: false, IsKey: false}, &blocks)

	changed := changedBlockScalars(blocks, false, indent)
	if len(changed) == 0 {
		return
	}

	stillChanged := make(map[*yaml.Node]bool)
	for _, node := range changedBlockScalars(changed, true, indent) {
		stillChanged[node] = true
	}

	for _, node := range changed {
		style := yaml.LiteralStyle
		if stillChanged[node] {
			style = yaml.DoubleQuotedStyle
		}

		node.Style = node.Style&^blockStyles | style
	}
}

func collectBlockScalars(node *yaml.Node, position scalarPosition, blocks *[]*yaml.Node) {
	switch node.Kind {
	case yaml.ScalarNode:
		if collectBlockScalar(node, position) {
			*blocks = append(*blocks, node)
		}
	case yaml.MappingNode:
		position.InFlow = position.InFlow || node.Style&yaml.FlowStyle != 0
		for i, child := range node.Content {
			collectBlockScalars(child, scalarPosition{InFlow: position.InFlow, IsKey: i%2 == 0}, blocks)
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		position.InFlow = position.InFlow || node.Style&yaml.FlowStyle != 0
		for _, child := range node.Content {
			collectBlockScalars(child, scalarPosition{InFlow: position.InFlow, IsKey: false}, blocks)
		}
	case yaml.AliasNode:
	}
}

func collectBlockScalar(node *yaml.Node, position scalarPosition) bool {
	if node.Value == "" && node.Style&quotedStyles == 0 && node.ShortTag() == "!!null" {
		if position.InFlow || position.IsKey {
			node.Value = "null"
		}

		return false
	}

	// The encoder writes a multi-line scalar with no style as a literal block.
	block := node.Style&blockStyles != 0 || node.Style&quotedStyles == 0 && strings.Contains(node.Value, "\n")

	return !position.InFlow && block
}

func changedBlockScalars(nodes []*yaml.Node, literal bool, indent int) []*yaml.Node {
	if len(nodes) == 0 {
		return nil
	}

	probe := &yaml.Node{Kind: yaml.MappingNode}

	for i, node := range nodes {
		style := node.Style
		if literal {
			style = yaml.LiteralStyle
		}

		probe.Content = append(probe.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(i)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: node.Tag, Style: style, Value: node.Value},
		)
	}

	back, ok := roundTripYAML(probe, indent)
	if !ok || len(back.Content) != len(probe.Content) {
		if len(nodes) == 1 {
			return nodes
		}

		var changed []*yaml.Node
		for _, node := range nodes {
			changed = append(changed, changedBlockScalars([]*yaml.Node{node}, literal, indent)...)
		}

		return changed
	}

	var changed []*yaml.Node

	for i, node := range nodes {
		if back.Content[2*i+1].Value != node.Value {
			changed = append(changed, node)
		}
	}

	return changed
}

func roundTripYAML(mapping *yaml.Node, indent int) (*yaml.Node, bool) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)

	if enc.Encode(mapping) != nil || enc.Close() != nil {
		return nil, false
	}

	var back yaml.Node
	if yaml.Unmarshal(buf.Bytes(), &back) != nil || len(back.Content) != 1 || back.Content[0].Kind != yaml.MappingNode {
		return nil, false
	}

	return back.Content[0], true
}
