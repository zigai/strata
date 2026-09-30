package codec

import (
	"encoding"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/defaulter"
)

var jsonDecoderInterfaces = [...]reflect.Type{
	reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[json.UnmarshalerFrom](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

var (
	jsonBindingsCache   map[reflect.Type]map[string]jsonFieldBinding
	jsonBindingsCacheMu sync.RWMutex
)

type jsonFieldBinding struct {
	Name string

	Type reflect.Type
}

func rewriteJSONDocument(document jsontext.Value, target any) (jsontext.Value, bool, error) {
	typ := reflect.TypeOf(target)
	if typ == nil || typ.Kind() != reflect.Pointer {
		return document, false, nil
	}

	rewritten, changed, err := rewriteJSONValue(document, typ.Elem())
	if err != nil {
		return nil, false, fmt.Errorf("%w: json unmarshal: %w", ErrMalformed, err)
	}

	return rewritten, changed, nil
}

// An unchanged subtree keeps the exact bytes of its numbers and strings
// on the way to encoding/json.
func rewriteJSONValue(value jsontext.Value, targetType reflect.Type) (jsontext.Value, bool, error) {
	targetType = jsonValueType(targetType)
	if targetType == nil {
		return value, false, nil
	}

	//nolint:exhaustive // a kind not listed here carries no member to rename: a scalar, an interface, or a function
	switch targetType.Kind() {
	case reflect.Struct:
		return rewriteJSONStruct(value, targetType)
	case reflect.Slice, reflect.Array:
		return rewriteJSONElements(value, targetType.Elem())
	case reflect.Map:
		return rewriteJSONMapValues(value, targetType.Elem())
	default:
		return value, false, nil
	}
}

func rewriteJSONStruct(value jsontext.Value, typ reflect.Type) (jsontext.Value, bool, error) {
	bindings := jsonBindings(typ)

	return rewriteJSONObject(value, func(member string) (jsonFieldBinding, bool) {
		binding, ok := bindings[member]

		return binding, ok
	})
}

func rewriteJSONElements(value jsontext.Value, elemType reflect.Type) (jsontext.Value, bool, error) {
	if value.Kind() != jsontext.KindBeginArray {
		return value, false, nil
	}

	var elements []jsontext.Value

	if err := json.Unmarshal(value, &elements); err != nil {
		return nil, false, fmt.Errorf("read array elements: %w", err)
	}

	changed := false

	for i, element := range elements {
		rewritten, elementChanged, err := rewriteJSONValue(element, elemType)
		if err != nil {
			return nil, false, err
		}

		if elementChanged {
			elements[i] = rewritten
			changed = true
		}
	}

	if !changed {
		return value, false, nil
	}

	encoded := appendJSONArray(nil, elements)

	return jsontext.Value(encoded), true, nil
}

func rewriteJSONMapValues(value jsontext.Value, elemType reflect.Type) (jsontext.Value, bool, error) {
	return rewriteJSONObject(value, func(member string) (jsonFieldBinding, bool) {
		return jsonFieldBinding{Name: member, Type: elemType}, true
	})
}

func rewriteJSONObject(value jsontext.Value, bind func(member string) (jsonFieldBinding, bool)) (jsontext.Value, bool, error) {
	if value.Kind() != jsontext.KindBeginObject {
		return value, false, nil
	}

	var members map[string]jsontext.Value

	if err := json.Unmarshal(value, &members); err != nil {
		return nil, false, fmt.Errorf("read object members: %w", err)
	}

	rewritten := make(map[string]jsontext.Value, len(members))

	changed := false

	for member, raw := range members {
		binding, ok := bind(member)
		if !ok {
			changed = true

			continue
		}

		nested, nestedChanged, err := rewriteJSONValue(raw, binding.Type)
		if err != nil {
			return nil, false, err
		}

		if member == binding.Name && !nestedChanged {
			rewritten[member] = raw

			continue
		}

		rewritten[binding.Name] = nested
		changed = true
	}

	if !changed {
		return value, false, nil
	}

	encoded, err := appendJSONObject(nil, rewritten)
	if err != nil {
		return nil, false, err
	}

	return jsontext.Value(encoded), true, nil
}

func appendJSONObject(dst []byte, members map[string]jsontext.Value) ([]byte, error) {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}

	sort.Strings(names)

	dst = append(dst, '{')

	var err error

	for i, name := range names {
		if i > 0 {
			dst = append(dst, ',')
		}

		dst, err = jsontext.AppendQuote(dst, name)
		if err != nil {
			return nil, fmt.Errorf("quote member name %q: %w", name, err)
		}

		dst = append(dst, ':')
		dst = append(dst, members[name]...)
	}

	return append(dst, '}'), nil
}

func appendJSONArray(dst []byte, elements []jsontext.Value) []byte {
	dst = append(dst, '[')

	for i, element := range elements {
		if i > 0 {
			dst = append(dst, ',')
		}

		dst = append(dst, element...)
	}

	return append(dst, ']')
}

// Direct fields shadow promoted fields with the same key.
// The returned map is shared and must not be changed.
func jsonBindings(typ reflect.Type) map[string]jsonFieldBinding {
	jsonBindingsCacheMu.RLock()

	bindings, cached := jsonBindingsCache[typ]

	jsonBindingsCacheMu.RUnlock()

	if cached {
		return bindings
	}

	bindings = make(map[string]jsonFieldBinding)

	collectJSONBindings(bindings, typ, make(map[reflect.Type]bool))

	jsonBindingsCacheMu.Lock()
	if jsonBindingsCache == nil {
		jsonBindingsCache = make(map[reflect.Type]map[string]jsonFieldBinding)
	}

	jsonBindingsCache[typ] = bindings
	jsonBindingsCacheMu.Unlock()

	return bindings
}

func collectJSONBindings(bindings map[string]jsonFieldBinding, typ reflect.Type, path map[reflect.Type]bool) {
	if path[typ] {
		return
	}

	path[typ] = true
	defer delete(path, typ)

	var embedded []reflect.StructField

	for field := range typ.Fields() {
		if jsonEmbeds(field) {
			embedded = append(embedded, field)

			continue
		}

		name, named := jsonTagName(field)
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

		addJSONBinding(bindings, key, jsonFieldBinding{Name: name, Type: field.Type})
	}

	for _, field := range embedded {
		collectJSONBindings(bindings, jsonStructType(field.Type), path)
	}
}

func addJSONBinding(bindings map[string]jsonFieldBinding, name string, binding jsonFieldBinding) {
	if name == "" || name == "-" {
		return
	}

	if _, taken := bindings[name]; taken {
		return
	}

	bindings[name] = binding
}

// An embedded field of unexported struct type is promoted too, because
// the fields it carries may be exported.
func jsonEmbeds(field reflect.StructField) bool {
	if !field.Anonymous {
		return false
	}

	if _, named := jsonTagName(field); named {
		return false
	}

	return jsonStructType(field.Type) != nil
}

func jsonStructType(typ reflect.Type) reflect.Type {
	typ = jsonValueType(typ)
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil
	}

	return typ
}

func jsonValueType(typ reflect.Type) reflect.Type {
	if typ == nil {
		return nil
	}

	for typ.Kind() == reflect.Pointer {
		if jsonDecodesItself(typ) {
			return nil
		}

		typ = typ.Elem()
	}

	if jsonDecodesItself(typ) {
		return nil
	}

	return typ
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

func jsonTagName(field reflect.StructField) (string, bool) {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return "", false
	}

	name, _, _ := strings.Cut(tag, ",")

	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}

	return name, true
}
