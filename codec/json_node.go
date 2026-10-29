package codec

import (
	"bytes"
	"encoding"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"
)

var (
	errUnsupportedMapKey = errors.New("unsupported map key type")
	errOptionValue       = errors.New("cannot decode")

	jsonOptionHolders sync.Map // jsonOptionHolderKey -> reflect.Type

	jsonDecoderInterfaces = [...]reflect.Type{
		reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[json.UnmarshalerFrom](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	jsonDurationUnmarshalers = json.WithUnmarshalers(json.UnmarshalFunc(func(data []byte, value *time.Duration) error {
		if jsontext.Value(data).Kind() != '"' {
			return errDurationString
		}

		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("decode duration string: %w", err)
		}

		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}

		*value = parsed

		return nil
	}))
)

type jsonNode struct {
	document []byte
	offset   int64
	value    jsontext.Value
}

type jsonOptionHolderKey struct {
	typ     reflect.Type
	options string
}

// textKey is a map key that a format spells as a string, as JSON and TOML do.
type textKey struct {
	name   string
	parent overlayNode
}

func (n jsonNode) kind() nodeKind {
	//nolint:exhaustive // jsontext.Kind also names tokens that are not values; every other value is a scalar
	switch n.value.Kind() {
	case jsontext.KindBeginObject:
		return nodeMapping
	case jsontext.KindBeginArray:
		return nodeSequence
	case jsontext.KindNull:
		return nodeNull
	default:
		return nodeScalar
	}
}

func (n jsonNode) line() int {
	return 1 + bytes.Count(n.document[:n.offset], []byte("\n"))
}

func (n jsonNode) decodesItself(typ reflect.Type) bool { return jsonDecodesItself(typ) }

func (n jsonNode) decode(target reflect.Value) error {
	return json.Unmarshal(n.value, target.Addr().Interface(), jsonDurationUnmarshalers) //nolint:wrapcheck // the overlay attributes it to a key
}

// decodeWithOptions decodes the value as a struct field carrying the json tag
// options, so encoding/json applies them exactly as it does to the field.
func (n jsonNode) decodeWithOptions(target reflect.Value, options string) error {
	holder := reflect.New(jsonOptionHolder(target.Type(), options)).Elem()
	holder.Field(0).Set(target)

	document := make([]byte, 0, len(n.value)+len(`{"value":}`))
	document = append(append(append(document, `{"value":`...), n.value...), '}')

	if err := json.Unmarshal(document, holder.Addr().Interface(), jsonDurationUnmarshalers); err != nil {
		semantic, ok := errors.AsType[*json.SemanticError](err)
		if !ok {
			return fmt.Errorf("decode with %q option: %w", options, err)
		}

		// The holder's field is an implementation detail, so the message is
		// rebuilt from what the value and the field's type were.
		if semantic.Err == nil {
			return fmt.Errorf("%w: JSON %s into %s with %q option", errOptionValue, semantic.JSONKind, target.Type(), options)
		}

		return fmt.Errorf("%w: JSON %s into %s with %q option: %w", errOptionValue, semantic.JSONKind, target.Type(), options, semantic.Err)
	}

	target.Set(holder.Field(0))

	return nil
}

func (n jsonNode) items() []overlayNode {
	var items []overlayNode

	n.walk(func(_ string, value jsonNode) { items = append(items, value) })

	return items
}

func (n jsonNode) entries(_ *overlay, _ jsontext.Pointer) []overlayEntry {
	var entries []overlayEntry

	n.walk(func(name string, value jsonNode) {
		entries = append(entries, overlayEntry{name: name, key: textKey{name: name, parent: value}, value: value})
	})

	return entries
}

// walk visits the members of an object or the elements of an array. The
// document was validated before decoding, so reading it cannot fail.
func (n jsonNode) walk(visit func(name string, value jsonNode)) {
	decoder := jsontext.NewDecoder(bytes.NewReader(n.value))
	if _, err := decoder.ReadToken(); err != nil {
		return
	}

	object := n.value.Kind() == jsontext.KindBeginObject

	for decoder.PeekKind() != jsontext.KindEndObject && decoder.PeekKind() != jsontext.KindEndArray {
		var name string

		if object {
			token, err := decoder.ReadToken()
			if err != nil {
				return
			}

			name = token.String()
		}

		value, err := decoder.ReadValue()
		if err != nil {
			return
		}

		// The decoder reuses its buffer, so the value is sliced from the input.
		end := decoder.InputOffset()
		start := end - int64(len(value))
		visit(name, jsonNode{document: n.document, offset: n.offset + start, value: n.value[start:end]})
	}
}

func (k textKey) kind() nodeKind                                    { return nodeScalar }
func (k textKey) line() int                                         { return k.parent.line() }
func (k textKey) entries(*overlay, jsontext.Pointer) []overlayEntry { return nil }
func (k textKey) items() []overlayNode                              { return nil }
func (k textKey) decodesItself(reflect.Type) bool                   { return true }

func (k textKey) decode(target reflect.Value) error {
	if unmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](target.Addr()); ok {
		if err := unmarshaler.UnmarshalText([]byte(k.name)); err != nil {
			return fmt.Errorf("decode map key %q: %w", k.name, err)
		}

		return nil
	}

	//nolint:exhaustive // the remaining kinds cannot be spelled as a string key
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(k.name, 10, target.Type().Bits())
		if err != nil {
			return fmt.Errorf("decode map key %q: %w", k.name, err)
		}

		target.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(k.name, 10, target.Type().Bits())
		if err != nil {
			return fmt.Errorf("decode map key %q: %w", k.name, err)
		}

		target.SetUint(parsed)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedMapKey, target.Type())
	}

	return nil
}

func jsonOptionHolder(typ reflect.Type, options string) reflect.Type {
	key := jsonOptionHolderKey{typ: typ, options: options}
	if cached, ok := jsonOptionHolders.Load(key); ok {
		holder, _ := cached.(reflect.Type)
		return holder
	}

	holder := reflect.StructOf([]reflect.StructField{{
		Name:      "Value",
		PkgPath:   "",
		Type:      typ,
		Tag:       reflect.StructTag(`json:"value,` + options + `"`),
		Offset:    0,
		Index:     nil,
		Anonymous: false,
	}})
	jsonOptionHolders.Store(key, holder)

	return holder
}

func jsonDecodesItself(typ reflect.Type) bool {
	for _, iface := range jsonDecoderInterfaces {
		if typ.Implements(iface) {
			return true
		}

		if typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(iface) {
			return true
		}
	}

	return false
}
