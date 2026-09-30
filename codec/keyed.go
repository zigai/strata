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
	keyedFormatTags = []string{"toml", "yaml", "json"}

	keyedPassthroughTags = []string{"comment", "commented", "multiline"}

	keyedMirrors sync.Map

	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	yamlMarshalerType = reflect.TypeFor[yaml.Marshaler]()
	timeType          = reflect.TypeFor[time.Time]()
)

// A nil typ means the source type encodes correctly as it is.
type keyedMirror struct {
	typ  reflect.Type
	mode mirrorMode
	// paths holds the source index path of each mirror field in order.
	paths [][]int
}

type mirrorMode struct {
	OmitSecrets        bool
	StringifyDurations bool
}

type structMirrorBuilder struct {
	mode   mirrorMode
	fields []reflect.StructField
	paths  [][]int
	seen   map[string]bool
}

type mirrorKey struct {
	typ  reflect.Type
	mode mirrorMode
}

func OmitSecrets(value any) any {
	return keyedValueMode(value, mirrorMode{OmitSecrets: true, StringifyDurations: false})
}

func keyedValue(value any) any {
	return keyedValueMode(value, mirrorMode{OmitSecrets: false, StringifyDurations: false})
}

func keyedTOMLValue(value any) any {
	return keyedValueMode(value, mirrorMode{OmitSecrets: false, StringifyDurations: true})
}

func keyedValueMode(value any, mode mirrorMode) any {
	if value == nil {
		return nil
	}

	src := reflect.ValueOf(value)

	mirror := mirrorOfMode(src.Type(), mode)
	if mirror.typ == nil {
		return value
	}

	return convertKeyed(src, mirror).Interface()
}

func mirrorOfMode(typ reflect.Type, mode mirrorMode) *keyedMirror {
	key := mirrorKey{typ: typ, mode: mode}
	if cached, ok := keyedMirrors.Load(key); ok {
		mirror, _ := cached.(*keyedMirror)
		return mirror
	}

	var mirror *keyedMirror

	switch {
	case mode.StringifyDurations && typ == reflect.TypeFor[time.Duration]():
		mirror = &keyedMirror{typ: reflect.TypeFor[string](), paths: nil, mode: mode}
	case describesItself(typ) || isRecursive(typ, nil):
		mirror = &keyedMirror{typ: nil, paths: nil, mode: mode}
	default:
		mirror = buildMirror(typ, mode)
	}

	keyedMirrors.Store(key, mirror)

	return mirror
}

func buildMirror(typ reflect.Type, mode mirrorMode) *keyedMirror {
	var mirrored reflect.Type

	//nolint:exhaustive // only container kinds can hold struct fields that need renaming
	switch typ.Kind() {
	case reflect.Pointer:
		if elem := mirrorOfMode(typ.Elem(), mode); elem.typ != nil {
			mirrored = reflect.PointerTo(elem.typ)
		}
	case reflect.Slice:
		if elem := mirrorOfMode(typ.Elem(), mode); elem.typ != nil {
			mirrored = reflect.SliceOf(elem.typ)
		}
	case reflect.Array:
		if elem := mirrorOfMode(typ.Elem(), mode); elem.typ != nil {
			mirrored = reflect.ArrayOf(typ.Len(), elem.typ)
		}
	case reflect.Map:
		if elem := mirrorOfMode(typ.Elem(), mode); elem.typ != nil {
			mirrored = reflect.MapOf(typ.Key(), elem.typ)
		}
	case reflect.Struct:
		builder := structMirrorBuilder{mode: mode, fields: nil, paths: nil, seen: nil}

		builder.collect(typ, nil)

		return &keyedMirror{typ: reflect.StructOf(builder.fields), paths: builder.paths, mode: mode}
	}

	return &keyedMirror{typ: mirrored, paths: nil, mode: mode}
}

func (b *structMirrorBuilder) collect(typ reflect.Type, prefix []int) {
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}

	var embeds []reflect.StructField

	for field := range typ.Fields() {
		if b.mode.OmitSecrets && defaulter.IsSecret(field) {
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
	if mirror := mirrorOfMode(fieldType, b.mode); mirror.typ != nil {
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

// A field whose tag for a format is "-" stays excluded from that format,
// as its own decoder excludes it.
func keyedTag(field reflect.StructField, key string) reflect.StructTag {
	var b strings.Builder

	for _, format := range keyedFormatTags {
		value := key

		switch tag := field.Tag.Get(format); {
		case tag == "-":
			value = "-"
		case strings.Contains(tag, ","):
			_, options, _ := strings.Cut(tag, ",")
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

	if mirror.mode.StringifyDurations && src.Type() == reflect.TypeFor[time.Duration]() {
		return reflect.ValueOf(time.Duration(src.Int()).String())
	}

	dst := reflect.New(mirror.typ).Elem()

	//nolint:exhaustive // buildMirror produces mirrors for these kinds only
	switch src.Kind() {
	case reflect.Pointer:
		if !src.IsNil() {
			ptr := reflect.New(mirror.typ.Elem())
			ptr.Elem().Set(convertKeyed(src.Elem(), mirrorOfMode(src.Type().Elem(), mirror.mode)))
			dst.Set(ptr)
		}
	case reflect.Slice:
		if !src.IsNil() {
			dst.Set(reflect.MakeSlice(mirror.typ, src.Len(), src.Len()))
			convertElements(src, dst, mirror.mode)
		}
	case reflect.Array:
		convertElements(src, dst, mirror.mode)
	case reflect.Map:
		convertMap(src, dst, mirror.mode)
	case reflect.Struct:
		convertStruct(src, dst, mirror)
	}

	return dst
}

func convertMap(src, dst reflect.Value, mode mirrorMode) {
	if src.IsNil() {
		return
	}

	dst.Set(reflect.MakeMapWithSize(dst.Type(), src.Len()))

	elemMirror := mirrorOfMode(src.Type().Elem(), mode)
	for iter := src.MapRange(); iter.Next(); {
		dst.SetMapIndex(iter.Key(), convertKeyed(iter.Value(), elemMirror))
	}
}

func convertStruct(src, dst reflect.Value, mirror *keyedMirror) {
	for i, path := range mirror.paths {
		field, err := src.FieldByIndexErr(path)
		if err != nil {
			continue
		}

		dst.Field(i).Set(convertKeyed(field, mirrorOfMode(field.Type(), mirror.mode)))
	}
}

func convertElements(src, dst reflect.Value, mode mirrorMode) {
	elemMirror := mirrorOfMode(src.Type().Elem(), mode)
	for i := range src.Len() {
		dst.Index(i).Set(convertKeyed(src.Index(i), elemMirror))
	}
}

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

// [reflect.StructOf] cannot build a type that refers to itself.
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
