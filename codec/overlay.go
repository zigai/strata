package codec

import (
	"encoding"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/keypath"

	"github.com/zigai/strata/internal/layout"
)

const (
	nodeScalar nodeKind = iota
	nodeNull
	nodeMapping
	nodeSequence
)

var (
	errArrayLength    = errors.New("wrong number of array elements")
	errDurationString = errors.New("a duration must be a string such as \"1m30s\" or \"7d\"")
	errNilInterface   = errors.New("no concrete type to decode into for nil interface")

	structKeyFields sync.Map // keyFieldsKey -> *structFields

	jsonNameFolder = strings.NewReplacer("_", "", "-", "")
)

type nodeKind uint8

// overlayNode is one value of a parsed document. The walker owns the shape of
// the configuration; a node decodes only leaves, through its format's library.
type overlayNode interface {
	kind() nodeKind
	line() int
	entries(o *overlay, key jsontext.Pointer) []overlayEntry
	items() []overlayNode
	decode(target reflect.Value) error
	decodesItself(typ reflect.Type) bool
}

type overlayEntry struct {
	name  string
	key   overlayNode
	value overlayNode
}

type keyFieldsKey struct {
	typ    reflect.Type
	format string
}

// structFields are the fields of a struct type that a format's documents name.
// inline is the path of the inline map, which receives every entry that no
// field claims; folded indexes the fields whose json tag says case:ignore.
type structFields struct {
	byKey  map[string]keyField
	folded map[string]string
	inline []int
}

// options are the json tag options, such as "string", that change how
// encoding/json reads the whole value, so the field is decoded by it.
type keyField struct {
	path    []int
	options string
}

// optionDecoder is a node whose format library decodes a value under struct
// tag options.
type optionDecoder interface {
	decodeWithOptions(target reflect.Value, options string) error
}

// overlay decodes a document onto a value that holds every layer below it.
// Struct fields and map entries the document names are decoded onto their
// current values, so nested structs and maps merge. A sequence replaces the
// current value, an array must have exactly its length, and a null resets the
// target to its zero value. Failures are collected as [KeyError] values.
type overlay struct {
	format string
	errs   []error
}

func decodeOverlay(format string, root overlayNode, target any) error {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer {
		return fmt.Errorf("%w: %s unmarshal: decode target %T is not a pointer", ErrMalformed, format, target)
	}

	if err := layout.Validate(val.Elem().Type()); err != nil {
		return fmt.Errorf("%s unmarshal: %w", format, err)
	}

	o := overlay{format: format, errs: nil}
	o.apply(root, val.Elem(), "")

	if len(o.errs) > 0 {
		return fmt.Errorf("%w: %s unmarshal: %w", ErrMalformed, format, errors.Join(o.errs...))
	}

	return nil
}

func (o *overlay) apply(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if node.kind() == nodeNull {
		target.SetZero()
		return
	}

	typ := target.Type()
	if node.decodesItself(typ) {
		o.decodeLeaf(node, target, key)
		return
	}

	//nolint:exhaustive // other kinds are scalars that the format library decodes
	switch typ.Kind() {
	case reflect.Pointer:
		if target.IsNil() {
			target.Set(reflect.New(typ.Elem()))
		}

		o.apply(node, target.Elem(), key)
	case reflect.Struct:
		if !layout.IsNestedStructType(typ) {
			o.decodeLeaf(node, target, key)
			return
		}

		o.applyStruct(node, target, key)
	case reflect.Map:
		o.applyMap(node, target, key)
	case reflect.Slice:
		o.applySlice(node, target, key)
	case reflect.Array:
		o.applyArray(node, target, key)
	case reflect.Interface:
		o.applyInterface(node, target, key)
	default:
		o.decodeLeaf(node, target, key)
	}
}

func (o *overlay) applyStruct(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if node.kind() != nodeMapping {
		o.decodeLeaf(node, target, key)
		return
	}

	fields := keyFields(target.Type(), o.format)

	for _, entry := range node.entries(o, key) {
		entryKey := key.AppendToken(entry.name)

		info, ok := fields.lookup(entry.name)
		if !ok {
			if fields.inline != nil {
				if inline, found := settableField(target, fields.inline); found {
					o.applyMapEntry(entry, inline, entryKey)
				}
			}

			continue
		}

		field, ok := settableField(target, info.path)
		if !ok {
			continue
		}

		if info.options != "" && entry.value.kind() != nodeNull {
			o.decodeWithOptions(entry.value, field, info.options, entryKey)
			continue
		}

		o.apply(entry.value, field, entryKey)
	}
}

func (o *overlay) applyMap(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if node.kind() != nodeMapping {
		o.decodeLeaf(node, target, key)
		return
	}

	for _, entry := range node.entries(o, key) {
		o.applyMapEntry(entry, target, key.AppendToken(entry.name))
	}
}

func (o *overlay) applyMapEntry(entry overlayEntry, target reflect.Value, key jsontext.Pointer) {
	typ := target.Type()
	if target.IsNil() {
		target.Set(reflect.MakeMap(typ))
	}

	mapKey := reflect.New(typ.Key()).Elem()
	if err := decodeMapKey(entry, mapKey); err != nil {
		o.fail(key, entry.key.line(), err)
		return
	}

	elem := reflect.New(typ.Elem()).Elem()
	if current := target.MapIndex(mapKey); current.IsValid() {
		elem.Set(current)
	}

	o.apply(entry.value, elem, key)
	target.SetMapIndex(mapKey, elem)
}

func (o *overlay) applySlice(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if node.kind() != nodeSequence {
		o.decodeLeaf(node, target, key)
		return
	}

	nodes := node.items()

	items := reflect.MakeSlice(target.Type(), len(nodes), len(nodes))
	for index, item := range nodes {
		o.apply(item, items.Index(index), key.AppendToken(strconv.Itoa(index)))
	}

	target.Set(items)
}

func (o *overlay) applyArray(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if node.kind() != nodeSequence {
		o.decodeLeaf(node, target, key)
		return
	}

	nodes := node.items()
	if len(nodes) != target.Len() {
		o.fail(key, node.line(), fmt.Errorf("%w: want %d, got %d", errArrayLength, target.Len(), len(nodes)))
		return
	}

	items := reflect.New(target.Type()).Elem()
	for index, item := range nodes {
		o.apply(item, items.Index(index), key.AppendToken(strconv.Itoa(index)))
	}

	target.Set(items)
}

func (o *overlay) applyInterface(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if target.NumMethod() > 0 {
		o.applyImplementation(node, target, key)
		return
	}

	var decoded any
	if !o.decodeLeaf(node, reflect.ValueOf(&decoded).Elem(), key) {
		return
	}

	current, currentIsMap := reflect.TypeAssert[map[string]any](target)
	if update, ok := decoded.(map[string]any); ok && currentIsMap {
		decoded = mergeDynamic(current, update)
	}

	if decoded == nil {
		target.SetZero()
		return
	}

	target.Set(reflect.ValueOf(decoded))
}

// applyImplementation decodes onto a copy of the value an interface with
// methods holds and stores it back, as encoding/json does. Nothing names a
// concrete type for a nil interface, so a value other than null fails.
func (o *overlay) applyImplementation(node overlayNode, target reflect.Value, key jsontext.Pointer) {
	if target.IsNil() {
		o.fail(key, node.line(), fmt.Errorf("%w %s", errNilInterface, target.Type()))
		return
	}

	current := target.Elem()
	value := reflect.New(current.Type()).Elem()
	value.Set(current)
	o.apply(node, value, key)
	target.Set(value)
}

func (o *overlay) decodeLeaf(node overlayNode, target reflect.Value, key jsontext.Pointer) bool {
	if err := node.decode(target); err != nil {
		if keyErr, ok := errors.AsType[*KeyError](err); ok {
			keyErr.Key = keypath.Format(key)
			o.errs = append(o.errs, keyErr)

			return false
		}

		o.fail(key, node.line(), err)

		return false
	}

	return true
}

func (o *overlay) decodeWithOptions(node overlayNode, target reflect.Value, options string, key jsontext.Pointer) {
	decoder, ok := node.(optionDecoder)
	if !ok {
		o.apply(node, target, key)
		return
	}

	if err := decoder.decodeWithOptions(target, options); err != nil {
		o.fail(key, node.line(), err)
	}
}

func (o *overlay) fail(key jsontext.Pointer, line int, err error) {
	o.errs = append(o.errs, &KeyError{Key: keypath.Format(key), Line: line, Err: err})
}

func mergeDynamic(current, update map[string]any) map[string]any {
	merged := make(map[string]any, len(current)+len(update))
	maps.Copy(merged, current)

	for name, value := range update {
		nestedCurrent, currentIsMap := merged[name].(map[string]any)
		nestedUpdate, updateIsMap := value.(map[string]any)

		if currentIsMap && updateIsMap {
			merged[name] = mergeDynamic(nestedCurrent, nestedUpdate)
			continue
		}

		merged[name] = value
	}

	return merged
}

func decodeMapKey(entry overlayEntry, target reflect.Value) error {
	if target.Kind() != reflect.String {
		return entry.key.decode(target)
	}

	if unmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](target.Addr()); ok {
		if err := unmarshaler.UnmarshalText([]byte(entry.name)); err != nil {
			return fmt.Errorf("decode map key %q: %w", entry.name, err)
		}

		return nil
	}

	target.SetString(entry.name)

	return nil
}

// keyFields describes the fields of a struct type that format names: the keys
// of its layout, less any field whose tag for format is "-".
func keyFields(typ reflect.Type, format string) *structFields {
	cacheKey := keyFieldsKey{typ: typ, format: format}
	if cached, ok := structKeyFields.Load(cacheKey); ok {
		fields, _ := cached.(*structFields)
		return fields
	}

	structLayout := layout.Of(typ)
	fields := &structFields{byKey: make(map[string]keyField), folded: make(map[string]string), inline: nil}

	for _, field := range structLayout.Fields {
		tag := field.Member.Field.Tag.Get(format)
		if tag == "-" {
			continue
		}

		_, options, _ := strings.Cut(tag, ",")
		info := keyField{path: field.Index, options: ""}

		if format == "json" {
			info.options = jsonValueOptions(options)

			if slices.Contains(strings.Split(options, ","), "case:ignore") {
				fields.folded[foldJSONName(field.Key)] = field.Key
			}
		}

		fields.byKey[field.Key] = info
	}

	if rest := structLayout.Rest; rest != nil && rest.Member.Field.Tag.Get(format) != "-" {
		fields.inline = rest.Index
	}

	structKeyFields.Store(cacheKey, fields)

	return fields
}

func (f *structFields) lookup(name string) (keyField, bool) {
	if field, ok := f.byKey[name]; ok {
		return field, true
	}

	if key, ok := f.folded[foldJSONName(name)]; ok {
		return f.byKey[key], true
	}

	return keyField{path: nil, options: ""}, false
}

// jsonValueOptions keeps the json tag options that change how a value is read.
// The others name the member or only affect encoding.
func jsonValueOptions(options string) string {
	if slices.Contains(strings.Split(options, ","), "string") {
		return "string"
	}

	return ""
}

// foldJSONName matches names the way a json case:ignore tag does: without
// letter case, dashes, or underscores.
func foldJSONName(name string) string {
	return strings.ToLower(jsonNameFolder.Replace(name))
}

// settableField walks path from target, allocating embedded struct pointers
// on the way. A nil pointer to an unexported embedded struct cannot be
// allocated, so its promoted fields are skipped.
func settableField(target reflect.Value, path []int) (reflect.Value, bool) {
	current := target

	for depth, index := range path {
		current = current.Field(index)

		if depth == len(path)-1 {
			break
		}

		if current.Kind() == reflect.Pointer {
			if current.IsNil() {
				if !current.CanSet() {
					return reflect.Value{}, false
				}

				current.Set(reflect.New(current.Type().Elem()))
			}

			current = current.Elem()
		}
	}

	return current, current.CanSet()
}
