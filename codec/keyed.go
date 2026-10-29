package codec

import (
	"encoding"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/layout"
)

var (
	errCyclicConfigurationValue = errors.New("cannot encode cyclic configuration value")
	errMultilineInlineYAMLKey   = errors.New("inline YAML map keys cannot contain newlines")
	errSecretEmbedding          = errors.New("cannot omit secrets from an unexported custom-marshaled embedding")

	keyedFormatTags = []string{"toml", "yaml", "json"}

	keyedPassthroughTags = []string{"comment", "commented", "multiline"}

	// restTag writes an inline map's entries beside the struct's own keys. TOML
	// has no such field, so a struct with an inline map is written to TOML as a
	// table built from its values.
	restTag = reflect.StructTag(`toml:"-" yaml:",inline" json:",embed"`)

	keyedMirrors sync.Map

	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	yamlUnmarshalerType = reflect.TypeFor[yaml.Unmarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshalerType   = reflect.TypeFor[json.Marshaler]()
	yamlMarshalerType   = reflect.TypeFor[yaml.Marshaler]()
	timeType            = reflect.TypeFor[time.Time]()
)

// A nil typ means the source type encodes correctly as it is.
type keyedMirror struct {
	typ  reflect.Type
	mode mirrorMode
	// paths holds the source index path of each mirror field in order.
	paths [][]int
}

type mirrorMode struct {
	Format             string
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

type keyedVisit struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}

func (mode mirrorMode) needsValues() bool {
	return mode.OmitSecrets || mode.Format == "yaml"
}

// needsValuesFor reports whether typ must be converted value by value: TOML
// writes a struct with an inline map as a table its entries join, and the
// fields promoted through a nil pointer are left out.
func (mode mirrorMode) needsValuesFor(typ reflect.Type) bool {
	return mode.needsValues() || mode.Format == "toml" && holdsStruct(typ, nil, hasRest) || holdsStruct(typ, nil, promotesThroughPointer)
}

func (mode mirrorMode) preservesValue(value reflect.Value) bool {
	if describesItself(value.Type()) {
		return !mode.OmitSecrets || !defaulter.ContainsSecrets(value)
	}

	return !mode.needsValuesFor(value.Type()) && !isRecursive(value.Type(), nil)
}

func OmitSecrets(value any, format string) (any, error) {
	return keyedValueMode(value, mirrorMode{Format: format, OmitSecrets: true, StringifyDurations: false})
}

func keyedValue(value any, format string) (any, error) {
	return keyedValueMode(value, mirrorMode{Format: format, OmitSecrets: false, StringifyDurations: false})
}

func keyedTOMLValue(value any) (any, error) {
	return keyedValueMode(value, mirrorMode{Format: "toml", OmitSecrets: false, StringifyDurations: true})
}

func keyedValueMode(value any, mode mirrorMode) (any, error) {
	if value == nil {
		return value, nil
	}

	if err := layout.Validate(reflect.TypeOf(value)); err != nil {
		return nil, fmt.Errorf("encode %T: %w", value, err)
	}

	src := reflect.ValueOf(value)
	if mode.needsValuesFor(src.Type()) || isRecursive(src.Type(), nil) && !describesItself(src.Type()) {
		converted, err := convertRecursiveKeyed(src, mode, make(map[keyedVisit]bool))
		if err != nil {
			return nil, err
		}

		return converted.Interface(), nil
	}

	mirror := mirrorOfMode(src.Type(), mode)
	if mirror.typ == nil {
		return value, nil
	}

	return convertKeyed(src, mirror).Interface(), nil
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
	case mode.Format == "yaml" && typ.Kind() == reflect.String:
		mirror = &keyedMirror{typ: reflect.TypeFor[yamlString](), paths: nil, mode: mode}
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

		builder.collect(typ)

		return &keyedMirror{typ: reflect.StructOf(builder.fields), paths: builder.paths, mode: mode}
	}

	return &keyedMirror{typ: mirrored, paths: nil, mode: mode}
}

func (b *structMirrorBuilder) collect(typ reflect.Type) {
	structLayout := layout.Of(typ)

	for _, field := range structLayout.Fields {
		if b.mode.OmitSecrets && field.Secret {
			continue
		}

		b.add(field.Member.Field, field.Index, keyedTag(field.Member.Field, field.Key))
	}

	if rest := structLayout.Rest; rest != nil && (!b.mode.OmitSecrets || !rest.Secret) {
		b.add(rest.Member.Field, rest.Index, restTag)
	}
}

func (b *structMirrorBuilder) add(field reflect.StructField, path []int, tag reflect.StructTag) {
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}

	// Promoted fields may share a Go name under different keys.
	name := field.Name
	for suffix := 1; b.seen[name]; suffix++ {
		name = field.Name + strconv.Itoa(suffix)
	}

	b.seen[name] = true

	fieldType := field.Type
	if mirror := mirrorOfMode(fieldType, b.mode); mirror.typ != nil {
		fieldType = mirror.typ
	}

	b.fields = append(b.fields, reflect.StructField{
		Name:      name,
		PkgPath:   "",
		Type:      fieldType,
		Tag:       tag,
		Offset:    0,
		Index:     nil,
		Anonymous: false,
	})
	b.paths = append(b.paths, path)
}

func (b *structMirrorBuilder) valueAt(index int, src reflect.Value, active map[keyedVisit]bool) (reflect.Value, error) {
	field, err := src.FieldByIndexErr(b.paths[index])
	if err != nil {
		// A field promoted through a nil pointer is left out, so decoding the
		// output leaves the pointer nil.
		if b.fields[index].Tag != restTag {
			b.fields[index].Tag = keyedTag(b.fields[index], "-")
		}

		return reflect.Zero(b.fields[index].Type), nil
	}

	return b.convertField(index, field, active)
}

func (b *structMirrorBuilder) convertField(index int, src reflect.Value, active map[keyedVisit]bool) (reflect.Value, error) {
	field := &b.fields[index]

	tag, omitted := recursiveFieldTag(field.Tag, src, b.mode)
	if b.mode.OmitSecrets && secretKeyedValue(src) {
		tag = keyedTag(*field, "-")
		omitted = true
	}

	if omitted {
		field.Tag = tag

		return reflect.Zero(field.Type), nil
	}

	value, err := convertRecursiveKeyed(src, b.mode, active)
	if err != nil {
		return reflect.Value{}, err
	}

	field.Type = value.Type()
	if b.mode.Format == "yaml" && multilineInlineYAMLMap(*field) {
		return reflect.Value{}, errMultilineInlineYAMLKey
	}

	if value.Type() != src.Type() {
		field.Tag = tag

		if src.Kind() == reflect.Pointer && isRecursive(src.Type(), nil) && !describesItself(src.Type()) {
			field.Type = reflect.TypeFor[any]()
		}
	}

	return value, nil
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

	switch mirror.typ {
	case reflect.TypeFor[string]():
		return reflect.ValueOf(time.Duration(src.Int()).String())
	case reflect.TypeFor[yamlString]():
		return reflect.ValueOf(yamlString(src.String()))
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

func secretKeyedValue(value reflect.Value) bool {
	for value.Kind() == reflect.Interface && !value.IsNil() {
		value = value.Elem()
	}

	return layout.IsSecretType(value.Type())
}

func convertRecursiveKeyed(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	if mode.OmitSecrets && layout.IsSecretType(src.Type()) {
		return src, nil
	}

	if src.Kind() == reflect.Interface {
		return convertRecursiveInterface(src, mode, active)
	}

	if mode.preservesValue(src) {
		return convertKeyed(src, mirrorOfMode(src.Type(), mode)), nil
	}

	if src.Kind() == reflect.Pointer || src.Kind() == reflect.Map || src.Kind() == reflect.Slice {
		if src.IsNil() {
			return src, nil
		}

		visit := keyedVisit{typ: src.Type(), pointer: uintptr(src.UnsafePointer()), length: 0}
		if src.Kind() == reflect.Slice {
			visit.length = src.Len()
		}

		if active[visit] {
			return reflect.Value{}, errCyclicConfigurationValue
		}

		active[visit] = true
		defer delete(active, visit)
	}

	return convertRecursiveContainer(src, mode, active)
}

func convertRecursiveInterface(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	if src.IsNil() {
		return src, nil
	}

	value, err := convertRecursiveKeyed(src.Elem(), mode, active)
	if err != nil {
		return reflect.Value{}, err
	}

	typ := src.Type()
	if !value.Type().AssignableTo(typ) {
		typ = reflect.TypeFor[any]()
	}

	out := reflect.New(typ).Elem()
	out.Set(value)

	return out, nil
}

func convertRecursiveContainer(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	switch src.Kind() {
	case reflect.Pointer:
		value, err := convertRecursiveKeyed(src.Elem(), mode, active)
		if err != nil {
			return reflect.Value{}, err
		}

		out := reflect.New(value.Type())
		out.Elem().Set(value)

		return out, nil
	case reflect.Struct:
		return convertRecursiveStruct(src, mode, active)
	case reflect.Slice, reflect.Array:
		return convertRecursiveElements(src, mode, active)
	case reflect.Map:
		return convertRecursiveMap(src, mode, active)
	case reflect.String:
		return convertKeyed(src, mirrorOfMode(src.Type(), mode)), nil
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Interface, reflect.UnsafePointer:
		return src, nil
	}

	return src, nil
}

func convertRecursiveStruct(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	for field := range src.Type().Fields() {
		if mode.OmitSecrets && field.Anonymous && !field.IsExported() && !layout.IsSecret(field) && describesItself(derefType(field.Type)) {
			return reflect.Value{}, errSecretEmbedding
		}
	}

	if mode.Format == "toml" && layout.Of(src.Type()).Rest != nil {
		return convertTOMLTable(src, mode, active)
	}

	builder := structMirrorBuilder{mode: mode, fields: nil, paths: nil, seen: nil}
	builder.collect(src.Type())

	values := make([]reflect.Value, len(builder.paths))
	for index := range builder.paths {
		value, err := builder.valueAt(index, src, active)
		if err != nil {
			return reflect.Value{}, err
		}

		values[index] = value
	}

	out := reflect.New(reflect.StructOf(builder.fields)).Elem()
	for index, value := range values {
		out.Field(index).Set(value)
	}

	return out, nil
}

// convertTOMLTable writes a struct with an inline map as one table: the keys of
// its fields, then each inline entry no field claims.
func convertTOMLTable(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	structLayout := layout.Of(src.Type())

	table, err := tomlTableFields(src, structLayout, mode, active)
	if err != nil {
		return reflect.Value{}, err
	}

	rest := structLayout.Rest
	if rest == nil || mode.OmitSecrets && rest.Secret {
		return reflect.ValueOf(table), nil
	}

	// A nil embedded pointer on the way holds no inline map.
	entries, _ := src.FieldByIndexErr(rest.Index)
	if !entries.IsValid() {
		return reflect.ValueOf(table), nil
	}

	if err := addTOMLEntries(table, entries, mode, active); err != nil {
		return reflect.Value{}, err
	}

	return reflect.ValueOf(table), nil
}

func tomlTableFields(src reflect.Value, structLayout *layout.Struct, mode mirrorMode, active map[keyedVisit]bool) (map[string]any, error) {
	table := make(map[string]any, len(structLayout.Fields))

	for _, field := range structLayout.Fields {
		if mode.OmitSecrets && field.Secret || field.Member.Field.Tag.Get("toml") == "-" {
			continue
		}

		value, err := src.FieldByIndexErr(field.Index)
		if err != nil || (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && value.IsNil() {
			continue
		}

		if mode.OmitSecrets && secretKeyedValue(value) {
			continue
		}

		converted, err := convertRecursiveKeyed(value, mode, active)
		if err != nil {
			return nil, err
		}

		table[field.Key] = converted.Interface()
	}

	return table, nil
}

func addTOMLEntries(table map[string]any, entries reflect.Value, mode mirrorMode, active map[keyedVisit]bool) error {
	for iter := entries.MapRange(); iter.Next(); {
		name := iter.Key().String()
		if _, claimed := table[name]; claimed || mode.OmitSecrets && secretKeyedValue(iter.Value()) {
			continue
		}

		converted, err := convertRecursiveKeyed(iter.Value(), mode, active)
		if err != nil {
			return err
		}

		table[name] = converted.Interface()
	}

	return nil
}

func recursiveFieldTag(tag reflect.StructTag, value reflect.Value, mode mirrorMode) (reflect.StructTag, bool) {
	if value.Kind() == reflect.Interface || mirrorOfMode(value.Type(), mode).typ != nil || isRecursive(value.Type(), nil) && !describesItself(value.Type()) {
		tag = recursiveOmissionTag(tag, value, mode.Format)
	}

	name, _, _ := strings.Cut(tag.Get(mode.Format), ",")

	return tag, mode.Format != "" && name == "-"
}

func recursiveOmissionTag(tag reflect.StructTag, value reflect.Value, selectedFormat string) reflect.StructTag {
	var out strings.Builder

	for _, format := range keyedFormatTags {
		fieldTag := tag.Get(format)
		if selectedFormat == "" || selectedFormat == format {
			fieldTag = keyedOmissionTag(fieldTag, value, format)
		}

		writeTag(&out, format, fieldTag)
	}

	for _, name := range keyedPassthroughTags {
		if value, ok := tag.Lookup(name); ok {
			writeTag(&out, name, value)
		}
	}

	return reflect.StructTag(out.String())
}

func keyedOmissionTag(tag string, value reflect.Value, format string) string {
	name, options, _ := strings.Cut(tag, ",")
	if format == "toml" || name == "-" {
		return tag
	}

	option := "omitzero"
	if format == "yaml" {
		option = "omitempty"
	}

	parts := strings.Split(options, ",")
	if !slices.Contains(parts, option) {
		return tag
	}

	if format == "json" && value.Kind() == reflect.Interface && !value.Type().Implements(reflect.TypeFor[interface{ IsZero() bool }]()) {
		return tag
	}

	zero, custom := customKeyedZero(value, format == "json")
	if !custom {
		return tag
	}

	if zero {
		return "-"
	}

	parts = slices.DeleteFunc(parts, func(part string) bool { return part == option })
	if options := strings.Join(parts, ","); options != "" {
		name += "," + options
	}

	return name
}

func customKeyedZero(value reflect.Value, addressable bool) (bool, bool) {
	if zero, ok := reflect.TypeAssert[interface{ IsZero() bool }](value); ok {
		underlying := value
		for underlying.Kind() == reflect.Interface && !underlying.IsNil() {
			underlying = underlying.Elem()
		}

		if underlying.Kind() == reflect.Pointer && underlying.IsNil() {
			return true, true
		}

		return zero.IsZero(), true
	}

	if addressable {
		if !value.CanAddr() {
			addressableValue := reflect.New(value.Type()).Elem()
			addressableValue.Set(value)
			value = addressableValue
		}

		if zero, ok := reflect.TypeAssert[interface{ IsZero() bool }](value.Addr()); ok {
			return zero.IsZero(), true
		}
	}

	return false, false
}

func convertRecursiveElements(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	values := make([]reflect.Value, 0, src.Len())

	typ := src.Type()
	for index := range src.Len() {
		if mode.OmitSecrets && secretKeyedValue(src.Index(index)) {
			if src.Kind() == reflect.Array {
				values = append(values, reflect.Zero(src.Type().Elem()))
			}

			continue
		}

		value, err := convertRecursiveKeyed(src.Index(index), mode, active)
		if err != nil {
			return reflect.Value{}, err
		}

		values = append(values, value)
		if !value.Type().AssignableTo(src.Type().Elem()) {
			if src.Kind() == reflect.Slice {
				typ = reflect.TypeFor[[]any]()
			} else {
				typ = reflect.ArrayOf(src.Len(), reflect.TypeFor[any]())
			}
		}
	}

	out := reflect.New(typ).Elem()
	if src.Kind() == reflect.Slice {
		out = reflect.MakeSlice(typ, len(values), len(values))
	}

	for index, value := range values {
		out.Index(index).Set(value)
	}

	return out, nil
}

func convertRecursiveMap(src reflect.Value, mode mirrorMode, active map[keyedVisit]bool) (reflect.Value, error) {
	keyType := src.Type().Key()

	keyMirror := mirrorOfMode(keyType, mode)
	if mode.Format == "yaml" && keyType.Kind() == reflect.String && keyMirror.typ != nil && multilineYAMLKeys(src) {
		keyType = keyMirror.typ
	}

	typ := reflect.MapOf(keyType, reflect.TypeFor[any]())

	out := reflect.MakeMapWithSize(typ, src.Len())
	for iter := src.MapRange(); iter.Next(); {
		if mode.OmitSecrets && (secretKeyedValue(iter.Key()) || secretKeyedValue(iter.Value())) {
			continue
		}

		value, err := convertRecursiveKeyed(iter.Value(), mode, active)
		if err != nil {
			return reflect.Value{}, err
		}

		key := iter.Key()
		if key.Type() != keyType {
			key = convertKeyed(key, keyMirror)
		}

		out.SetMapIndex(key, value)
	}

	return out, nil
}

func multilineYAMLKeys(src reflect.Value) bool {
	for iter := src.MapRange(); iter.Next(); {
		if strings.Contains(iter.Key().String(), "\n") {
			return true
		}
	}

	return false
}

func multilineInlineYAMLMap(field reflect.StructField) bool {
	return field.Type.Kind() == reflect.Map && field.Type.Key() == reflect.TypeFor[yamlString]() &&
		slices.Contains(strings.Split(field.Tag.Get("yaml"), ","), "inline")
}

func decodesItself(typ reflect.Type) bool {
	for _, iface := range []reflect.Type{textUnmarshalerType, yamlUnmarshalerType} {
		if typ.Implements(iface) || reflect.PointerTo(typ).Implements(iface) {
			return true
		}
	}

	return false
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

func holdsStruct(typ reflect.Type, path []reflect.Type, match func(reflect.Type) bool) bool {
	if slices.Contains(path, typ) {
		return false
	}

	path = append(path, typ)

	//nolint:exhaustive // only container kinds can hold a struct
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return holdsStruct(typ.Elem(), path, match)
	case reflect.Struct:
		if describesItself(typ) {
			return false
		}

		if match(typ) {
			return true
		}

		for _, field := range layout.Of(typ).Fields {
			if holdsStruct(field.Member.Field.Type, path, match) {
				return true
			}
		}
	}

	return false
}

func hasRest(typ reflect.Type) bool {
	return layout.Of(typ).Rest != nil
}

func promotesThroughPointer(typ reflect.Type) bool {
	structLayout := layout.Of(typ)

	fields := structLayout.Fields
	if structLayout.Rest != nil {
		fields = append(slices.Clone(fields), *structLayout.Rest)
	}

	for _, field := range fields {
		owner := typ
		for _, step := range field.Index[:len(field.Index)-1] {
			member := owner.Field(step)
			if member.Type.Kind() == reflect.Pointer {
				return true
			}

			owner = member.Type
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
