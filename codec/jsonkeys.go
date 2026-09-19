package codec

import (
	"encoding"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/defaulter"
)

// A JSON document names configuration keys the way every other layer does: a
// field is named by the first of its strata, toml, yaml, and json tags that
// names it, or by the snake_case form of its Go name. encoding/json matches
// neither of those on its own, so a document is rewritten before it is decoded.
// Each member that names a field is renamed to the name encoding/json matches,
// which is the field's json tag when it declares one and its Go name otherwise.
//
// The rewritten document is then handed to encoding/json, so type conversion,
// custom decoders, and the errors they report are unchanged. A member that names
// no field is carried through untouched and stays ignored, and a document that
// needs no renaming is decoded from the bytes it was read from, byte for byte.
//
// Two members that name one field are refused rather than resolved by picking
// one, matching the duplicate keys the other formats reject.

// jsonDecoderInterfaces are the interfaces whose implementations decode a JSON
// value themselves. A type listed here names its own members, so the rewrite
// stops at it.
var jsonDecoderInterfaces = [...]reflect.Type{
	reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[json.UnmarshalerFrom](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

// errMemberConflict is the base of the failure reported when two members of one
// JSON object name the same field. Which of the two the field takes is not
// something a document decides, and picking one would discard the other without
// reporting it. The condition is the JSON spelling of the duplicate key the
// other formats reject.
var errMemberConflict = errors.New("two members name the same field")

// jsonBindingsCache holds the binding sets already derived, keyed by the struct
// type they describe. A Codec is safe for concurrent use, hence the lock; a set
// itself is immutable once stored.
var (
	jsonBindingsCache   map[reflect.Type]map[string]jsonBinding
	jsonBindingsCacheMu sync.RWMutex
)

// jsonBinding is the name one member uses for a struct field, together with the
// type the member's value decodes into.
type jsonBinding struct {
	// Name is the name encoding/json matches for the field: its json tag, or its
	// Go name when the tag does not name it.
	Name string

	// Type is the field's type, which a nested value is rewritten against.
	Type reflect.Type
}

// rewriteJSONDocument renames the members of a parsed JSON document that name a
// field of target, and reports whether the document changed.
//
// A target that is not a pointer names no fields, and its document is left for
// encoding/json to reject. The document arrives here already parsed, so a
// syntactic failure, a duplicate member, and invalid UTF-8 stay the parse
// failures they are, whatever the rewrite would have done with the members
// around them.
//
// A failure of the rewrite is reported as [ErrMalformed] because that is what
// the document has become: one that cannot be decoded into the target.
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

	return rewriteJSONObject(value, func(member string) (jsonBinding, bool) {
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
	return rewriteJSONObject(value, func(member string) (jsonBinding, bool) {
		return jsonBinding{Name: member, Type: elemType}, true
	})
}

// rewriteJSONObject rewrites the members of a JSON object, renaming each member
// the bind function resolves.
//
// A member bind does not resolve is carried through untouched. Two members that
// resolve to one name are refused: the document names one field twice, and
// resolving it by picking a member would discard the other without reporting it.
func rewriteJSONObject(value jsontext.Value, bind func(member string) (jsonBinding, bool)) (jsontext.Value, bool, error) {
	if value.Kind() != jsontext.KindBeginObject {
		return value, false, nil
	}

	var members map[string]jsontext.Value

	if err := json.Unmarshal(value, &members); err != nil {
		return nil, false, fmt.Errorf("read object members: %w", err)
	}

	rewritten := make(map[string]jsontext.Value, len(members))
	named := make(map[string]string, len(members))

	changed := false

	for member, raw := range members {
		binding, ok := bind(member)
		if !ok {
			rewritten[member] = raw

			continue
		}

		if first, seen := named[binding.Name]; seen {
			return nil, false, fmt.Errorf("%w: %q and %q", errMemberConflict, first, member)
		}

		named[binding.Name] = member

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

// jsonBindings maps every member name that may name a field of typ to the
// binding that name selects.
//
// A field accepts the name encoding/json matches and the configuration key
// [defaulter.FieldKey] derives for it: the first of its strata, toml, yaml, and
// json tags that names the field, else the snake_case form of its Go name. A
// field that declares no tag therefore accepts both its Go name and its
// snake_case form, which is the key style provenance reports and the environment
// tier binds.
//
// Fields declared on typ itself are collected before the fields an embedded
// struct promotes, and the first binding for a name wins, so a field here
// shadows a promoted field of the same name.
//
// The set depends on the type alone, so it is computed once and reused: a
// document holding many values of one type walks that type's fields once. The
// returned map is shared and MUST NOT be modified.
func jsonBindings(typ reflect.Type) map[string]jsonBinding {
	jsonBindingsCacheMu.RLock()

	bindings, cached := jsonBindingsCache[typ]

	jsonBindingsCacheMu.RUnlock()

	if cached {
		return bindings
	}

	bindings = make(map[string]jsonBinding)

	collectJSONBindings(bindings, typ, make(map[reflect.Type]bool))

	jsonBindingsCacheMu.Lock()
	if jsonBindingsCache == nil {
		jsonBindingsCache = make(map[reflect.Type]map[string]jsonBinding)
	}

	jsonBindingsCache[typ] = bindings
	jsonBindingsCacheMu.Unlock()

	return bindings
}

// collectJSONBindings adds the bindings of typ, and of the structs embedded in
// it, to bindings.
//
// A field its json tag excludes, as in `json:"-"`, and a field its configuration
// key excludes, as in `strata:"-"`, contribute nothing: the first is invisible to
// encoding/json, and the second names no configuration key. An unexported field
// contributes nothing either, unless it is an embedded struct, whose exported
// fields are promoted.
//
// path holds the struct types visited on this path. A type that repeats on one
// path contributes nothing further, so an embedded pointer to the enclosing type
// terminates the walk.
func collectJSONBindings(bindings map[string]jsonBinding, typ reflect.Type, path map[reflect.Type]bool) {
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

		binding := jsonBinding{Name: name, Type: field.Type}

		addJSONBinding(bindings, name, binding)
		addJSONBinding(bindings, key, binding)
	}

	for _, field := range embedded {
		collectJSONBindings(bindings, jsonStructType(field.Type), path)
	}
}

// addJSONBinding records a name for a binding unless an earlier field claimed
// the name.
func addJSONBinding(bindings map[string]jsonBinding, name string, binding jsonBinding) {
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
