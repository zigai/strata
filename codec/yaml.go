package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

var errCyclicYAMLAlias = errors.New("cyclic YAML alias")

// YAMLCodec implements [Codec] for YAML documents using yaml.v3.
//
// A YAMLCodec is stateless and safe for concurrent use.
type YAMLCodec struct{}

// NewYAMLCodec returns a codec that decodes and encodes YAML documents.
func NewYAMLCodec() *YAMLCodec {
	return &YAMLCodec{}
}

// Decode overlays a YAML document onto target.
//
// A field the document omits keeps the value it already holds. A struct field
// is named by its configuration key, spelled exactly; any other key is ignored
// rather than reported. YAML merge keys are preserved, including merges from
// aliases whose source mapping is outside the configuration struct.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping the yaml.v3 failure, and a stream carrying more than one
// document returns [ErrMultipleDocuments]. An empty document is not an error.
func (c *YAMLCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		// An empty document has nothing to overlay and is not an error.
		if errors.Is(err, io.EOF) {
			return nil
		}

		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	var extra yaml.Node

	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		// Exactly one document, which is what a configuration tier must be.
	case err != nil:
		// The stream is malformed after the first document. That is a parse
		// failure, not a second document.
		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	default:
		return fmt.Errorf("yaml unmarshal: %w", ErrMultipleDocuments)
	}

	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer && val.Elem().Kind() == reflect.Struct {
		if hasYAMLAliasCycle(&doc, make(map[*yaml.Node]bool), make(map[*yaml.Node]bool)) {
			return fmt.Errorf("%w: %w", ErrMalformed, errCyclicYAMLAlias)
		}

		snapshotYAMLAliases(&doc)

		if err := rewriteYAMLNode(&doc, val.Elem().Type()); err != nil {
			return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
		}
	}

	if err := doc.Decode(target); err != nil {
		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

// yamlBindings binds each configuration key of typ to the name yaml.v3 matches:
// the field's yaml tag, or its lowercased Go name.
func yamlBindings(typ reflect.Type) map[string]fieldBinding {
	return keyBindings(typ, func(field reflect.StructField) (string, bool) {
		name, named := tagName(field, "yaml")
		if !named {
			return strings.ToLower(field.Name), true
		}

		return name, name != "-"
	})
}

func rewriteYAMLMapping(node *yaml.Node, targetType reflect.Type) error {
	if targetType.Kind() == reflect.Map {
		for i := 1; i < len(node.Content); i += 2 {
			if err := rewriteYAMLNode(node.Content[i], targetType.Elem()); err != nil {
				return err
			}
		}

		return nil
	}

	if targetType.Kind() != reflect.Struct {
		return nil
	}

	bindings := yamlBindings(targetType)
	content := node.Content[:0]

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		if keyNode.Value == "<<" && keyNode.Tag == "!!merge" {
			if err := rewriteYAMLMerge(valNode, targetType); err != nil {
				return err
			}

			content = append(content, keyNode, valNode)

			continue
		}

		binding, ok := bindings[keyNode.Value]
		if !ok {
			// Not a configuration key. Dropping it keeps yaml.v3 from matching it
			// to a field by its own naming rules.
			continue
		}

		keyNode.Value = binding.Name

		if err := rewriteYAMLNode(valNode, binding.Type); err != nil {
			return err
		}

		content = append(content, keyNode, valNode)
	}

	node.Content = content

	return nil
}

func rewriteYAMLMerge(node *yaml.Node, targetType reflect.Type) error {
	switch node.Kind {
	case yaml.AliasNode:
		if node.Alias == nil {
			return nil
		}

		return rewriteYAMLNode(node.Alias, targetType)
	case yaml.MappingNode:
		return rewriteYAMLMapping(node, targetType)
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if err := rewriteYAMLMerge(item, targetType); err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.ScalarNode:
		return nil
	}

	return nil
}

func snapshotYAMLAliases(node *yaml.Node) {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		node.Alias = cloneYAMLNode(node.Alias)
	}

	for _, child := range node.Content {
		snapshotYAMLAliases(child)
	}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))

	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}

	return &clone
}

func hasYAMLAliasCycle(node *yaml.Node, active, done map[*yaml.Node]bool) bool {
	if node == nil || done[node] {
		return false
	}

	if active[node] {
		return true
	}

	active[node] = true
	for _, child := range node.Content {
		if hasYAMLAliasCycle(child, active, done) {
			return true
		}
	}

	if node.Kind == yaml.AliasNode && hasYAMLAliasCycle(node.Alias, active, done) {
		return true
	}

	delete(active, node)
	done[node] = true

	return false
}

func rewriteYAMLSequence(node *yaml.Node, targetType reflect.Type) error {
	if targetType.Kind() == reflect.Slice || targetType.Kind() == reflect.Array {
		elemType := targetType.Elem()
		for _, content := range node.Content {
			if err := rewriteYAMLNode(content, elemType); err != nil {
				return err
			}
		}
	}

	return nil
}

func rewriteYAMLNode(node *yaml.Node, targetType reflect.Type) error {
	if node == nil {
		return nil
	}

	targetType = derefType(targetType)
	if targetType == nil {
		return nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, content := range node.Content {
			if err := rewriteYAMLNode(content, targetType); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		return rewriteYAMLMapping(node, targetType)
	case yaml.SequenceNode:
		return rewriteYAMLSequence(node, targetType)
	case yaml.ScalarNode, yaml.AliasNode:
		// Scalar and alias nodes contain no child nodes to rewrite.
	}

	return nil
}

func derefType(typ reflect.Type) reflect.Type {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}

// Encode encodes value as YAML bytes.
//
// Struct fields are written under their configuration keys, so the document
// uses the same keys as every other layer. The document is terminated by a
// newline.
//
// It returns an error wrapping the yaml.v3 failure if value cannot be
// represented in YAML.
func (c *YAMLCodec) Encode(value any) ([]byte, error) {
	data, err := yaml.Marshal(keyedValue(value))
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	return data, nil
}
