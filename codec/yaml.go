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

var (
	errCyclicYAMLAlias       = errors.New("cyclic YAML alias")
	errInvalidYAMLMappingKey = errors.New("invalid compound mapping key")
)

// YAML implements [Codec] for YAML documents using yaml.v3.
//
// YAML is stateless and safe for concurrent use.
type YAML struct{}

type yamlString string

// NewYAML returns a codec that decodes and encodes YAML documents.
func NewYAML() *YAML {
	return &YAML{}
}

// Decode overlays a YAML document onto target.
//
// A field the document omits keeps the value it already holds. A struct field
// is named by its configuration key, spelled exactly; any other key is ignored
// rather than reported. YAML merge keys are preserved, including merges from
// aliases whose source mapping is outside the configuration struct.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping [ErrMalformed] and the yaml.v3 failure or a [KeyError] for each
// value that does not fit its field. A stream carrying more than one document
// returns [ErrMultipleDocuments]. An empty document is not an error.
func (c *YAML) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	doc, ok, err := readOneYAMLDocument(data)
	if !ok || err != nil {
		return err
	}

	if hasYAMLAliasCycle(doc, make(map[*yaml.Node]bool), make(map[*yaml.Node]bool)) {
		return fmt.Errorf("%w: %w", ErrMalformed, errCyclicYAMLAlias)
	}

	if len(doc.Content) == 0 {
		return nil
	}

	// A document holding only null sets nothing.
	root := newYAMLNode(doc.Content[0])
	if root.kind() == nodeNull {
		return nil
	}

	return decodeOverlay("yaml", root, target)
}

func readOneYAMLDocument(data []byte) (*yaml.Node, bool, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	var extra yaml.Node

	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return &doc, true, nil
	case err != nil:
		// The stream is malformed after the first document. That is a parse
		// failure, not a second document.
		return nil, false, fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	default:
		return nil, false, fmt.Errorf("yaml unmarshal: %w", ErrMultipleDocuments)
	}
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
func (c *YAML) Encode(value any) ([]byte, error) {
	keyed, err := keyedValue(value, "yaml")
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	data, err := yaml.Marshal(keyed)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	return data, nil
}

func (value yamlString) MarshalYAML() (any, error) {
	text := string(value)
	if !strings.Contains(text, "\n") {
		return text, nil
	}

	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text, Style: yaml.DoubleQuotedStyle}, nil
}
