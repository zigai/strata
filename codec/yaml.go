package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/defaulter"
)

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
// A field the document omits keeps the value it already holds, and a key the
// target does not declare is ignored rather than reported.
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
		if err := rewriteYAMLNode(&doc, val.Elem().Type()); err != nil {
			return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
		}
	}

	if err := doc.Decode(target); err != nil {
		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

type yamlFieldBinding struct {
	Name string
	Type reflect.Type
}

func yamlTagName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("yaml")
	if tag == "" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	name = strings.TrimSpace(name)
	return name, name != ""
}

func yamlBindings(typ reflect.Type) map[string]yamlFieldBinding {
	bindings := make(map[string]yamlFieldBinding)
	collectYAMLBindings(bindings, typ, make(map[reflect.Type]bool))
	return bindings
}

func collectYAMLBindings(bindings map[string]yamlFieldBinding, typ reflect.Type, path map[reflect.Type]bool) {
	if path[typ] {
		return
	}
	path[typ] = true
	defer delete(path, typ)

	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Anonymous {
			collectYAMLBindings(bindings, derefType(field.Type), path)
			continue
		}
		name, named := yamlTagName(field)
		if !field.IsExported() || (named && name == "-") {
			continue
		}
		key := defaulter.FieldKey(field)
		if key == "-" {
			continue
		}
		if !named {
			name = strings.ToLower(field.Name)
		}
		binding := yamlFieldBinding{Name: name, Type: field.Type}
		bindings[name] = binding
		bindings[field.Name] = binding
		bindings[key] = binding
		bindings[defaulter.ToSnakeCase(field.Name)] = binding
		bindings[strings.ToLower(field.Name)] = binding
	}
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
		if targetType.Kind() != reflect.Struct {
			return nil
		}
		bindings := yamlBindings(targetType)
		named := make(map[string]string)

		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]

			binding, ok := bindings[keyNode.Value]
			if !ok {
				continue
			}

			if first, seen := named[binding.Name]; seen && first != keyNode.Value {
				return fmt.Errorf("two members name the same field: %q and %q", first, keyNode.Value)
			}
			named[binding.Name] = keyNode.Value
			keyNode.Value = binding.Name

			if err := rewriteYAMLNode(valNode, binding.Type); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if targetType.Kind() == reflect.Slice || targetType.Kind() == reflect.Array {
			elemType := targetType.Elem()
			for _, content := range node.Content {
				if err := rewriteYAMLNode(content, elemType); err != nil {
					return err
				}
			}
		}
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
// The document is terminated by a newline.
//
// It returns an error wrapping the yaml.v3 failure if value cannot be
// represented in YAML.
func (c *YAMLCodec) Encode(value any) ([]byte, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("yaml marshal: %w", err)
	}

	return data, nil
}
