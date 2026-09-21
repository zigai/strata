package codec

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/strata/internal/defaulter"
)

// TOMLCodec implements [Codec] for TOML documents using go-toml/v2.
//
// A TOMLCodec is stateless and safe for concurrent use.
type TOMLCodec struct{}

type tomlFieldBinding struct {
	Name string
	Type reflect.Type
}

// NewTOMLCodec returns a codec that decodes and encodes TOML documents.
func NewTOMLCodec() *TOMLCodec {
	return &TOMLCodec{}
}

// Decode overlays a TOML document onto target.
//
// A field the document omits keeps the value it already holds, and a key the
// target does not declare is ignored rather than reported.
//
// It returns [ErrNilTarget] if target is nil. A malformed document returns an
// error wrapping the go-toml/v2 failure.
func (c *TOMLCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return ErrNilTarget
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.Elem().Kind() != reflect.Struct {
		if err := toml.Unmarshal(data, target); err != nil {
			return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
		}

		return nil
	}

	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
	}

	if doc == nil {
		return nil
	}

	rewritten, err := rewriteTOMLValue(doc, val.Elem().Type())
	if err != nil {
		return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
	}

	encoded, err := toml.Marshal(rewritten)
	if err != nil {
		return fmt.Errorf("%w: toml marshal: %w", ErrMalformed, err)
	}

	if err := toml.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("%w: toml unmarshal: %w", ErrMalformed, err)
	}

	return nil
}

func tomlTagName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("toml")
	if tag == "" {
		return "", false
	}

	name, _, _ := strings.Cut(tag, ",")
	name = strings.TrimSpace(name)

	return name, name != ""
}

func tomlBindings(typ reflect.Type) map[string]tomlFieldBinding {
	bindings := make(map[string]tomlFieldBinding)
	collectTOMLBindings(bindings, typ, make(map[reflect.Type]bool))

	return bindings
}

func collectTOMLBindings(bindings map[string]tomlFieldBinding, typ reflect.Type, path map[reflect.Type]bool) {
	if path[typ] {
		return
	}

	path[typ] = true
	defer delete(path, typ)

	for field := range typ.Fields() {
		if field.Anonymous {
			collectTOMLBindings(bindings, derefType(field.Type), path)
			continue
		}

		name, named := tomlTagName(field)
		if !field.IsExported() || (named && name == "-") {
			continue
		}

		key := defaulter.FieldKey(field)
		if key == "-" {
			continue
		}

		if !named {
			name = field.Name
		}

		binding := tomlFieldBinding{Name: name, Type: field.Type}
		bindings[name] = binding
		bindings[field.Name] = binding
		bindings[key] = binding
		bindings[defaulter.ToSnakeCase(field.Name)] = binding
		bindings[strings.ToLower(field.Name)] = binding
	}
}

func rewriteTOMLStruct(m map[string]any, targetType reflect.Type) (any, error) {
	bindings := tomlBindings(targetType)
	rewritten := make(map[string]any, len(m))
	named := make(map[string]string, len(m))

	for k, v := range m {
		binding, ok := bindings[k]
		if !ok {
			rewritten[k] = v
			continue
		}

		if first, seen := named[binding.Name]; seen && first != k {
			return nil, fmt.Errorf("%w: %q and %q", errMemberConflict, first, k)
		}

		named[binding.Name] = k

		nested, err := rewriteTOMLValue(v, binding.Type)
		if err != nil {
			return nil, err
		}

		rewritten[binding.Name] = nested
	}

	return rewritten, nil
}

func rewriteTOMLSlice(slice []any, elemType reflect.Type) (any, error) {
	rewritten := make([]any, len(slice))
	for i, item := range slice {
		nested, err := rewriteTOMLValue(item, elemType)
		if err != nil {
			return nil, err
		}

		rewritten[i] = nested
	}

	return rewritten, nil
}

func rewriteTOMLMap(m map[string]any, elemType reflect.Type) (any, error) {
	rewritten := make(map[string]any, len(m))
	for k, v := range m {
		nested, err := rewriteTOMLValue(v, elemType)
		if err != nil {
			return nil, err
		}

		rewritten[k] = nested
	}

	return rewritten, nil
}

func rewriteTOMLValue(val any, targetType reflect.Type) (any, error) {
	targetType = derefType(targetType)
	if targetType == nil {
		return val, nil
	}

	//nolint:exhaustive // reflect.Kind is an external standard-library enum; non-container kinds require no rewriting
	switch targetType.Kind() {
	case reflect.Struct:
		if m, ok := val.(map[string]any); ok {
			return rewriteTOMLStruct(m, targetType)
		}
	case reflect.Slice, reflect.Array:
		if slice, ok := val.([]any); ok {
			return rewriteTOMLSlice(slice, targetType.Elem())
		}
	case reflect.Map:
		if m, ok := val.(map[string]any); ok {
			return rewriteTOMLMap(m, targetType.Elem())
		}
	default:
		return val, nil
	}

	return val, nil
}

// Encode encodes value as TOML bytes.
//
// The document is terminated by a newline.
//
// It returns an error wrapping the go-toml/v2 failure if value cannot be
// represented in TOML.
func (c *TOMLCodec) Encode(value any) ([]byte, error) {
	data, err := toml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("toml marshal: %w", err)
	}

	return data, nil
}
