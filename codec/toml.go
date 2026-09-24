package codec

import (
	"fmt"
	"reflect"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// TOMLCodec implements [Codec] for TOML documents using go-toml/v2.
//
// A TOMLCodec is stateless and safe for concurrent use.
type TOMLCodec struct{}

// NewTOMLCodec returns a codec that decodes and encodes TOML documents.
func NewTOMLCodec() *TOMLCodec {
	return &TOMLCodec{}
}

// Decode overlays a TOML document onto target.
//
// A field the document omits keeps the value it already holds. A struct field
// is named by its configuration key, spelled exactly; any other key is ignored
// rather than reported.
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

// tomlBindings binds each configuration key of typ to the name go-toml matches:
// the field's toml tag, or its Go name.
func tomlBindings(typ reflect.Type) map[string]fieldBinding {
	return keyBindings(typ, func(field reflect.StructField) (string, bool) {
		name, named := tagName(field, "toml")
		if !named {
			return field.Name, true
		}

		return name, name != "-"
	})
}

func rewriteTOMLStruct(m map[string]any, targetType reflect.Type) (any, error) {
	bindings := tomlBindings(targetType)
	rewritten := make(map[string]any, len(m))

	for k, v := range m {
		binding, ok := bindings[k]
		if !ok {
			// Not a configuration key. Dropping it keeps go-toml from matching
			// it to a field by its own, case-insensitive rules.
			continue
		}

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

	if targetType == reflect.TypeFor[time.Duration]() {
		if raw, ok := val.(string); ok {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("parse duration: %w", err)
			}

			return int64(d), nil
		}
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
// Struct fields are written under their configuration keys, so the document
// uses the same keys as every other layer. Plain [time.Duration] values are
// written as strings, such as "30s". The document ends with a newline.
//
// It returns an error wrapping the go-toml/v2 failure if value cannot be
// represented in TOML.
func (c *TOMLCodec) Encode(value any) ([]byte, error) {
	data, err := toml.Marshal(keyedTOMLValue(value))
	if err != nil {
		return nil, fmt.Errorf("toml marshal: %w", err)
	}

	return data, nil
}
