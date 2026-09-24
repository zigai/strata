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

// JSON members use configuration keys: the first named strata, toml, yaml, or
// json tag, then the snake_case Go name. Before decoding, matching members are
// renamed for encoding/json and unknown members are dropped. encoding/json
// still handles conversion, custom decoders, and errors.

// jsonDecoderInterfaces are the interfaces whose implementations decode a JSON
// value themselves. A type listed here names its own members, so the rewrite
// stops at it.
var jsonDecoderInterfaces = [...]reflect.Type{
	reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[json.UnmarshalerFrom](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

// jsonBindingsCache holds the binding sets already derived, keyed by the struct
// type they describe. A Codec is safe for concurrent use, hence the lock; a set
// itself is immutable once stored.
var (
	jsonBindingsCache   map[reflect.Type]map[string]jsonFieldBinding
	jsonBindingsCacheMu sync.RWMutex
)

// jsonFieldBinding is the name one member uses for a struct field, together with the
// type the member's value decodes into.
type jsonFieldBinding struct {
	// Name is the name encoding/json matches for the field: its json tag, or its
	// Go name when the tag does not name it.
	Name string

	// Type is the field's type, which a nested value is rewritten against.
	Type reflect.Type
}

// rewriteJSONDocument renames parsed members for target and reports changes.
//
// Non-pointer targets are left to encoding/json to reject. Rewrite failures
// wrap [ErrMalformed].
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

// rewriteJSONValue renames the members of a JSON value that name a field of
// targetType, and reports whether the value changed.
//
// A value that needs no renaming is returned as it was read, so an unchanged
// subtree keeps the exact bytes of its numbers and strings on the way to
// encoding/json.
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

// rewriteJSONStruct renames the members of a JSON object to the names
// encoding/json matches for the fields of typ.
func rewriteJSONStruct(value jsontext.Value, typ reflect.Type) (jsontext.Value, bool, error) {
	bindings := jsonBindings(typ)

	return rewriteJSONObject(value, func(member string) (jsonFieldBinding, bool) {
		binding, ok := bindings[member]

		return binding, ok
	})
}

// rewriteJSONElements renames the members of every element of a JSON array.
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

// rewriteJSONMapValues renames the members of the values of a JSON object that
// decodes into a map.
//
// The keys of the map name the caller's own domains rather than fields of a
// struct, so they are carried through as written.
func rewriteJSONMapValues(value jsontext.Value, elemType reflect.Type) (jsontext.Value, bool, error) {
	return rewriteJSONObject(value, func(member string) (jsonFieldBinding, bool) {
		return jsonFieldBinding{Name: member, Type: elemType}, true
	})
}

// rewriteJSONObject rewrites the members of a JSON object, renaming each member
// the bind function resolves.
//
// A member bind does not resolve is dropped.
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

// appendJSONObject appends a JSON object to dst, quoting each member name and
// carrying each member value through as the text it was read from.
//
// A value reaches the decoder as the document wrote it, so an untouched number
// keeps its digits, an untouched string keeps its escapes, and a value handed to
// a type that decodes itself arrives as it was written. Members are written in
// name order, which keeps the text stable for the same document.
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

// appendJSONArray appends a JSON array to dst, carrying each element through as
// the text it was read from.
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

// jsonBindings maps each configuration key of typ to its JSON field binding.
//
// Direct fields shadow promoted fields with the same key.
//
// Bindings are cached by type. The returned map is shared and must not be changed.
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

// collectJSONBindings adds bindings for typ and its embedded structs.
//
// Excluded and unexported fields are skipped; exported fields of embedded
// structs are promoted.
//
// path prevents recursion through repeated embedded types.
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

// addJSONBinding records a name for a binding unless an earlier field claimed
// the name.
func addJSONBinding(bindings map[string]jsonFieldBinding, name string, binding jsonFieldBinding) {
	if name == "" || name == "-" {
		return
	}

	if _, taken := bindings[name]; taken {
		return
	}

	bindings[name] = binding
}

// jsonEmbeds reports whether a struct field is promoted rather than named.
//
// An anonymous struct field that its json tag does not name is inlined into the
// struct around it, matching encoding/json and the defaults walk. An embedded
// field of unexported struct type is promoted too, because the fields it carries
// may be exported.
func jsonEmbeds(field reflect.StructField) bool {
	if !field.Anonymous {
		return false
	}

	if _, named := jsonTagName(field); named {
		return false
	}

	return jsonStructType(field.Type) != nil
}

// jsonStructType returns the struct type a value of typ promotes, or nil when
// typ is not a struct or names its own members.
func jsonStructType(typ reflect.Type) reflect.Type {
	typ = jsonValueType(typ)
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil
	}

	return typ
}

// jsonValueType reports the type a JSON value decodes into, or nil when nothing
// about the value's members can be rewritten.
//
// A pointer is followed to the type it points at. An interface names no shape at
// all: the document decides what it holds. A type that decodes itself names its
// own members and is left alone.
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

// jsonDecodesItself reports whether typ decodes a JSON value without help from
// the field names around it.
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

// jsonTagName returns the name a json tag gives a field, and reports whether the
// tag names one.
//
// The options a tag carries after its first comma are dropped, and a tag that
// carries no name, as in `json:",omitempty"`, names nothing and leaves the field
// matched by its Go name.
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
