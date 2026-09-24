package codec

import (
	"encoding"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/defaulter"
)

var (
	// keyedFormatTags are the struct tags a mirror type sets so every built-in
	// encoder writes a field under its configuration key.
	keyedFormatTags = []string{"toml", "yaml", "json"}

	// keyedPassthroughTags are tags the underlying encoders understand beyond the
	// field name. They are copied onto the mirror field unchanged.
	keyedPassthroughTags = []string{"comment", "commented", "multiline"}

	keyedMirrors sync.Map

	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	yamlMarshalerType = reflect.TypeFor[yaml.Marshaler]()
	timeType          = reflect.TypeFor[time.Time]()
)

// keyedMirror is the encoding shape of one source type. A nil typ means the
// source type encodes correctly as it is.
type keyedMirror struct {
	typ             reflect.Type
	omitSecrets     bool
	durationStrings bool
	// paths holds, for a struct mirror, the source index path of each mirror
	// field in order.
	paths [][]int
}

// structMirrorBuilder gathers the fields of a struct mirror, flattening embedded
// structs the way Go promotes their fields.
type structMirrorBuilder struct {
	omitSecrets     bool
	durationStrings bool
	fields          []reflect.StructField
	paths           [][]int
	seen            map[string]bool
}

type mirrorKey struct {
	typ             reflect.Type
	omitSecrets     bool
	durationStrings bool
}

func WithoutSecrets(value any) any { return keyedValueMode(value, true, false) }

// keyedValue maps struct fields to their configuration keys before encoding.
//
// It flattens embedded structs and omits excluded or unexported fields.
// Custom marshalers, [time.Time], and recursive types stay unchanged.
func keyedValue(value any) any { return keyedValueMode(value, false, false) }

func keyedTOMLValue(value any) any { return keyedValueMode(value, false, true) }

func keyedValueMode(value any, omitSecrets, durationStrings bool) any {
	if value == nil {
		return nil
	}

	src := reflect.ValueOf(value)

	mirror := mirrorOfMode(src.Type(), omitSecrets, durationStrings)
	if mirror.typ == nil {
		return value
	}

	return convertKeyed(src, mirror).Interface()
}

func mirrorOfMode(typ reflect.Type, omitSecrets, durationStrings bool) *keyedMirror {
	key := mirrorKey{typ: typ, omitSecrets: omitSecrets, durationStrings: durationStrings}
	if cached, ok := keyedMirrors.Load(key); ok {
		mirror, _ := cached.(*keyedMirror)
		return mirror
	}

	var mirror *keyedMirror

	switch {
	case durationStrings && typ == reflect.TypeFor[time.Duration]():
		mirror = &keyedMirror{typ: reflect.TypeFor[string](), paths: nil, omitSecrets: omitSecrets, durationStrings: durationStrings}
	case describesItself(typ) || isRecursive(typ, nil):
		mirror = &keyedMirror{typ: nil, paths: nil, omitSecrets: omitSecrets, durationStrings: durationStrings}
	default:
		mirror = buildMirror(typ, omitSecrets, durationStrings)
	}

	keyedMirrors.Store(key, mirror)

	return mirror
}

func buildMirror(typ reflect.Type, omitSecrets, durationStrings bool) *keyedMirror {
	var mirrored reflect.Type

	//nolint:exhaustive // only container kinds can hold struct fields that need renaming
	switch typ.Kind() {
	case reflect.Pointer:
		if elem := mirrorOfMode(typ.Elem(), omitSecrets, durationStrings); elem.typ != nil {
			mirrored = reflect.PointerTo(elem.typ)
		}
	case reflect.Slice:
		if elem := mirrorOfMode(typ.Elem(), omitSecrets, durationStrings); elem.typ != nil {
			mirrored = reflect.SliceOf(elem.typ)
		}
	case reflect.Array:
		if elem := mirrorOfMode(typ.Elem(), omitSecrets, durationStrings); elem.typ != nil {
			mirrored = reflect.ArrayOf(typ.Len(), elem.typ)
		}
	case reflect.Map:
		if elem := mirrorOfMode(typ.Elem(), omitSecrets, durationStrings); elem.typ != nil {
			mirrored = reflect.MapOf(typ.Key(), elem.typ)
		}
	case reflect.Struct:
		builder := structMirrorBuilder{omitSecrets: omitSecrets, durationStrings: durationStrings, fields: nil, paths: nil, seen: nil}

		builder.collect(typ, nil)

		return &keyedMirror{typ: reflect.StructOf(builder.fields), paths: builder.paths, omitSecrets: omitSecrets, durationStrings: durationStrings}
	}

	return &keyedMirror{typ: mirrored, paths: nil, omitSecrets: omitSecrets, durationStrings: durationStrings}
}

func (b *structMirrorBuilder) collect(typ reflect.Type, prefix []int) {
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}

	var embeds []reflect.StructField

	for field := range typ.Fields() {
		if b.omitSecrets && defaulter.IsSecret(field) {
			continue
		}

		if embedsStruct(field) {
			embeds = append(embeds, field)
			continue
		}

		b.add(field, prefix)
	}

	// Promoted fields rank below the enclosing struct's own fields, as in Go.
	for _, embed := range embeds {
		b.collect(derefType(embed.Type), appendIndex(prefix, embed.Index))
	}
}

func (b *structMirrorBuilder) add(field reflect.StructField, prefix []int) {
	if !field.IsExported() || b.seen[field.Name] {
		return
	}

	key := defaulter.FieldKey(field)
	if key == "-" {
		return
	}

	b.seen[field.Name] = true

	fieldType := field.Type
	if mirror := mirrorOfMode(fieldType, b.omitSecrets, b.durationStrings); mirror.typ != nil {
		fieldType = mirror.typ
	}

	b.fields = append(b.fields, reflect.StructField{
		Name:      field.Name,
		PkgPath:   "",
		Type:      fieldType,
		Tag:       keyedTag(field, key),
		Offset:    0,
		Index:     nil,
		Anonymous: false,
	})
	b.paths = append(b.paths, appendIndex(prefix, field.Index))
}

func embedsStruct(field reflect.StructField) bool {
	if !field.Anonymous {
		return false
	}

	embedType := derefType(field.Type)

	return embedType.Kind() == reflect.Struct && !describesItself(embedType)
}

func appendIndex(prefix, index []int) []int {
	path := make([]int, 0, len(prefix)+len(index))
	path = append(path, prefix...)

	return append(path, index...)
}

// keyedTag builds the tag of a mirror field: every format tag names the
// configuration key, keeping the options the source tag declared for that
// format, and the passthrough tags are copied.
func keyedTag(field reflect.StructField, key string) reflect.StructTag {
	var b strings.Builder

	for _, format := range keyedFormatTags {
		value := key

		if _, options, ok := strings.Cut(field.Tag.Get(format), ","); ok {
			value += "," + options
		}

		writeTag(&b, format, value)
	}

	for _, name := range keyedPassthroughTags {
		if value, ok := field.Tag.Lookup(name); ok {
			writeTag(&b, name, value)
		}
	}

	return reflect.StructTag(b.String())
}

func writeTag(b *strings.Builder, name, value string) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}

	b.WriteString(name)
	b.WriteByte(':')
	b.WriteString(strconv.Quote(value))
}

func convertKeyed(src reflect.Value, mirror *keyedMirror) reflect.Value {
	if mirror.typ == nil {
		return src
	}

	if mirror.durationStrings && src.Type() == reflect.TypeFor[time.Duration]() {
		return reflect.ValueOf(time.Duration(src.Int()).String())
	}

	dst := reflect.New(mirror.typ).Elem()

	//nolint:exhaustive // buildMirror produces mirrors for these kinds only
	switch src.Kind() {
	case reflect.Pointer:
		if !src.IsNil() {
			ptr := reflect.New(mirror.typ.Elem())
			ptr.Elem().Set(convertKeyed(src.Elem(), mirrorOfMode(src.Type().Elem(), mirror.omitSecrets, mirror.durationStrings)))
			dst.Set(ptr)
		}
	case reflect.Slice:
		if !src.IsNil() {
			dst.Set(reflect.MakeSlice(mirror.typ, src.Len(), src.Len()))
			convertElements(src, dst, mirror.omitSecrets, mirror.durationStrings)
		}
	case reflect.Array:
		convertElements(src, dst, mirror.omitSecrets, mirror.durationStrings)
	case reflect.Map:
		convertMap(src, dst, mirror.omitSecrets, mirror.durationStrings)
	case reflect.Struct:
		convertStruct(src, dst, mirror)
	}

	return dst
}

func convertMap(src, dst reflect.Value, omitSecrets, durationStrings bool) {
	if src.IsNil() {
		return
	}

	dst.Set(reflect.MakeMapWithSize(dst.Type(), src.Len()))

	elemMirror := mirrorOfMode(src.Type().Elem(), omitSecrets, durationStrings)
	for iter := src.MapRange(); iter.Next(); {
		dst.SetMapIndex(iter.Key(), convertKeyed(iter.Value(), elemMirror))
	}
}

func convertStruct(src, dst reflect.Value, mirror *keyedMirror) {
	for i, path := range mirror.paths {
		field, err := src.FieldByIndexErr(path)
		if err != nil {
			// The field sits behind a nil embedded pointer and has no value.
			continue
		}

		dst.Field(i).Set(convertKeyed(field, mirrorOfMode(field.Type(), mirror.omitSecrets, mirror.durationStrings)))
	}
}

func convertElements(src, dst reflect.Value, omitSecrets, durationStrings bool) {
	elemMirror := mirrorOfMode(src.Type().Elem(), omitSecrets, durationStrings)
	for i := range src.Len() {
		dst.Index(i).Set(convertKeyed(src.Index(i), elemMirror))
	}
}

// describesItself reports whether typ controls its own encoding, so its fields
// are not the keys a document holds.
func describesItself(typ reflect.Type) bool {
	if typ == timeType || typ.Kind() == reflect.Interface {
		return true
	}

	for _, iface := range []reflect.Type{textMarshalerType, jsonMarshalerType, yamlMarshalerType} {
		if typ.Implements(iface) || reflect.PointerTo(typ).Implements(iface) {
			return true
		}
	}

	return false
}

// isRecursive reports whether typ can reach itself through its fields or
// elements. [reflect.StructOf] cannot build a type that refers to itself.
func isRecursive(typ reflect.Type, path []reflect.Type) bool {
	if slices.Contains(path, typ) {
		return true
	}

	path = append(path, typ)

	//nolint:exhaustive // only container kinds can refer to another type
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return isRecursive(typ.Elem(), path)
	case reflect.Struct:
		if describesItself(typ) {
			return false
		}

		for field := range typ.Fields() {
			if isRecursive(field.Type, path) {
				return true
			}
		}
	}

	return false
}
