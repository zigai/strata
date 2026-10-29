package env

import (
	"encoding"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/duration"
	"github.com/zigai/strata/internal/keypath"
	"github.com/zigai/strata/internal/layout"
)

var (
	ErrUnsupportedType = errors.New("unsupported field type for environment binding")

	ErrInvalidValue = errors.New("invalid environment variable value")

	ErrInvalidBool = errors.New("cannot parse boolean value")
)

type Options struct {
	Prefix string
	Lookup func(string) (string, bool)
	OnBind func(key jsontext.Pointer, envVar, rawVal string)
}

type Var struct {
	Key      jsontext.Pointer
	Name     string
	Names    []string
	IsSecret bool
}

// scope is where a struct value sits: the key it is reached by, and the layout
// whose keys it contributes to, with the index path from that layout's struct.
// A promoted struct shares the scope of the struct that holds it.
type scope struct {
	key    jsontext.Pointer
	layout *layout.Struct
	index  []int
}

func Apply(target any, opts Options) error {
	if target == nil {
		return defaulter.ErrInvalidTarget
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return defaulter.ErrInvalidTarget
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return defaulter.ErrInvalidTarget
	}

	lookup := opts.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}

	return bindEnvStruct(elem, normalizePrefix(opts.Prefix), rootScope(elem.Type()), lookup, opts.OnBind, false, nil, nil)
}

func Describe(typ reflect.Type, prefix string) []Var {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return nil
	}

	var vars []Var

	describeStruct(typ, normalizePrefix(prefix), rootScope(typ), false, nil, &vars)

	return vars
}

func normalizePrefix(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}

	return prefix
}

func rootScope(typ reflect.Type) scope {
	return scope{key: "", layout: layout.Of(typ), index: nil}
}

func (at scope) promoted(member layout.Member) scope {
	return scope{key: at.key, layout: at.layout, index: append(slices.Clone(at.index), member.Field.Index...)}
}

func (at scope) nested(key string, typ reflect.Type) scope {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return scope{key: at.key.AppendToken(key), layout: layout.Of(typ), index: nil}
}

// owns reports whether member holds its key in this scope; a field that a
// shallower field shadows is not bound.
func (at scope) owns(member layout.Member) bool {
	field, ok := at.layout.Field(member.Key)

	return ok && slices.Equal(field.Index, append(slices.Clone(at.index), member.Field.Index...))
}

func describeStruct(typ reflect.Type, prefix string, at scope, inheritedSecret bool, active []reflect.Type, vars *[]Var) {
	if slices.Contains(active, typ) {
		return
	}

	active = append(active, typ)

	for _, member := range layout.MembersOf(typ) {
		describeMember(member, prefix, at, inheritedSecret, active, vars)
	}
}

func describeMember(member layout.Member, prefix string, at scope, inheritedSecret bool, active []reflect.Type, vars *[]Var) {
	sf := member.Field
	if !sf.IsExported() || member.Rest {
		return
	}

	secret := inheritedSecret || member.Secret

	fieldType := sf.Type
	for fieldType.Kind() == reflect.Pointer && layout.IsNestedStructType(fieldType) {
		fieldType = fieldType.Elem()
	}

	if member.Promoted {
		describeStruct(fieldType, prefix, at.promoted(member), secret, active, vars)
		return
	}

	if !at.owns(member) {
		return
	}

	if layout.IsNestedStructType(fieldType) {
		describeStruct(fieldType, prefix, at.nested(member.Key, fieldType), secret, active, vars)
		return
	}

	if !envDecodable(sf.Type) {
		return
	}

	key := at.key.AppendToken(member.Key)

	names := candidateNames(sf, prefix, key)
	if len(names) == 0 {
		return
	}

	*vars = append(*vars, Var{Key: key, Name: documentedName(sf, names), Names: names, IsSecret: secret})
}

func documentedName(sf reflect.StructField, names []string) string {
	if tag, _, _ := strings.Cut(sf.Tag.Get("env"), ","); strings.TrimSpace(tag) != "" {
		return names[0]
	}

	return names[len(names)-1]
}

func envDecodable(typ reflect.Type) bool {
	if reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return true
	}

	//nolint:exhaustive // mirrors the kinds unmarshalValue accepts
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice:
		return envDecodable(typ.Elem())
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func bindEnvStruct(
	val reflect.Value,
	prefix string,
	at scope,
	lookup func(string) (string, bool),
	onBind func(key jsontext.Pointer, envVar, rawVal string),
	inheritedSecret bool,
	activeTypes []reflect.Type,
	activePtrs []uintptr,
) error {
	typ := val.Type()
	if slices.Contains(activeTypes, typ) {
		return nil
	}

	activeTypes = append(activeTypes, typ)
	defer func() { activeTypes = activeTypes[:len(activeTypes)-1] }()

	for _, member := range layout.MembersOf(typ) {
		if !member.Field.IsExported() || member.Rest {
			continue
		}

		fieldSecret := inheritedSecret || member.Secret
		field := val.FieldByIndex(member.Field.Index)

		var err error

		switch {
		case member.Promoted:
			err = bindStruct(field, prefix, at.promoted(member), lookup, onBind, fieldSecret, activeTypes, activePtrs)
		case !at.owns(member):
		case layout.IsNestedStructType(field.Type()):
			err = bindStruct(field, prefix, at.nested(member.Key, field.Type()), lookup, onBind, fieldSecret, activeTypes, activePtrs)
		default:
			err = bindLeafField(field, member.Field, prefix, at.key.AppendToken(member.Key), lookup, onBind, fieldSecret)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func bindStruct(
	field reflect.Value,
	prefix string,
	at scope,
	lookup func(string) (string, bool),
	onBind func(key jsontext.Pointer, envVar, rawVal string),
	inheritedSecret bool,
	activeTypes []reflect.Type,
	activePtrs []uintptr,
) error {
	if field.Kind() == reflect.Pointer {
		return bindPointerStructField(field, prefix, at, lookup, onBind, inheritedSecret, activeTypes, activePtrs)
	}

	return bindEnvStruct(field, prefix, at, lookup, onBind, inheritedSecret, activeTypes, activePtrs)
}

func bindPointerStructField(
	field reflect.Value,
	prefix string,
	at scope,
	lookup func(string) (string, bool),
	onBind func(key jsontext.Pointer, envVar, rawVal string),
	inheritedSecret bool,
	activeTypes []reflect.Type,
	activePtrs []uintptr,
) error {
	elemType := field.Type().Elem()
	if slices.Contains(activeTypes, elemType) {
		return nil
	}

	if !field.IsNil() {
		ptr := field.Pointer()
		if slices.Contains(activePtrs, ptr) {
			return nil
		}

		activePtrs = append(activePtrs, ptr)
		defer func() { activePtrs = activePtrs[:len(activePtrs)-1] }()

		return bindEnvStruct(field.Elem(), prefix, at, lookup, onBind, inheritedSecret, activeTypes, activePtrs)
	}

	tmp := reflect.New(elemType)
	boundCount := 0

	countBind := func(k jsontext.Pointer, envVar, rawVal string) {
		boundCount++

		if onBind != nil {
			onBind(k, envVar, rawVal)
		}
	}

	if err := bindEnvStruct(tmp.Elem(), prefix, at, lookup, countBind, inheritedSecret, activeTypes, activePtrs); err != nil {
		return err
	}

	if boundCount > 0 {
		field.Set(tmp)
	}

	return nil
}

func bindLeafField(
	field reflect.Value,
	sf reflect.StructField,
	prefix string,
	key jsontext.Pointer,
	lookup func(string) (string, bool),
	onBind func(key jsontext.Pointer, envVar, rawVal string),
	inheritedSecret bool,
) error {
	envVar, rawVal, found := lookupEnvValue(sf, prefix, key, lookup)
	if !found {
		return nil
	}

	secret := inheritedSecret || layout.IsSecret(sf)

	if err := unmarshalValue(field, rawVal); err != nil {
		// NB: a secret field MUST NOT have its value reproduced here, and the
		// parse library's own error text quotes the input.
		if secret {
			return fmt.Errorf("%w for %s from %s=[REDACTED]", ErrInvalidValue, keypath.Format(key), envVar)
		}

		return fmt.Errorf("%w for %s from %s=%q: %w", ErrInvalidValue, keypath.Format(key), envVar, rawVal, err)
	}

	recordVal := rawVal
	if secret {
		recordVal = "[REDACTED]"
	}

	if onBind != nil {
		onBind(key, envVar, recordVal)
	}

	return nil
}

func lookupEnvValue(
	sf reflect.StructField,
	prefix string,
	key jsontext.Pointer,
	lookup func(string) (string, bool),
) (string, string, bool) {
	for _, name := range candidateNames(sf, prefix, key) {
		if val, ok := lookup(name); ok {
			return name, val, true
		}
	}

	return "", "", false
}

// Derived names require a prefix so generic process variables such as PORT
// and HOST cannot bind accidentally.
func candidateNames(sf reflect.StructField, prefix string, key jsontext.Pointer) []string {
	if isEnvSkipped(sf) {
		return nil
	}

	var names []string

	tag, _, _ := strings.Cut(sf.Tag.Get("env"), ",")
	if tag = strings.TrimSpace(tag); tag != "" {
		if prefix != "" {
			names = append(names, prefix+tag)
		}

		names = append(names, tag)
	}

	if prefix == "" {
		return names
	}

	var parts []string
	for token := range key.Tokens() {
		parts = append(parts, toUpperSnake(token))
	}

	if len(parts) > 1 {
		names = append(names, prefix+strings.Join(parts, "__"))
	}

	single := prefix + strings.Join(parts, "_")
	if !slices.Contains(names, single) {
		names = append(names, single)
	}

	return names
}

func isEnvSkipped(sf reflect.StructField) bool {
	tag := sf.Tag.Get("env")
	if tag == "" {
		return false
	}

	name, _, _ := strings.Cut(tag, ",")

	return strings.TrimSpace(name) == "-"
}

func toUpperSnake(s string) string {
	return strings.ToUpper(layout.ToSnakeCase(s))
}

func unmarshalValue(field reflect.Value, raw string) error {
	if field.CanAddr() {
		if u, ok := reflect.TypeAssert[encoding.TextUnmarshaler](field.Addr()); ok {
			if err := u.UnmarshalText([]byte(raw)); err != nil {
				return fmt.Errorf("text unmarshaler: %w", err)
			}

			return nil
		}
	}

	//nolint:exhaustive // supported primitive, pointer, and slice kinds are bound; other kinds are rejected
	switch field.Kind() {
	case reflect.Pointer:
		return unmarshalPointer(field, raw)
	case reflect.String:
		field.SetString(raw)
		return nil
	case reflect.Bool:
		return unmarshalBool(field, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return unmarshalInt(field, raw)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return unmarshalUint(field, raw)
	case reflect.Float32, reflect.Float64:
		return unmarshalFloat(field, raw)
	case reflect.Slice:
		return unmarshalSlice(field, raw)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedType, field.Type())
	}
}

func unmarshalPointer(field reflect.Value, raw string) error {
	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}

	return unmarshalValue(field.Elem(), raw)
}

func unmarshalBool(field reflect.Value, raw string) error {
	b, err := parseBool(raw)
	if err != nil {
		return err
	}

	field.SetBool(b)

	return nil
}

func unmarshalInt(field reflect.Value, raw string) error {
	if field.Type() == reflect.TypeFor[time.Duration]() {
		d, err := duration.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}

		field.SetInt(int64(d))

		return nil
	}

	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fmt.Errorf("parse int: %w", err)
	}

	// NB: without this check a narrowing field would wrap: int8("300") is 44.
	if field.OverflowInt(v) {
		return fmt.Errorf("%w: %d does not fit %s", ErrInvalidValue, v, field.Type())
	}

	field.SetInt(v)

	return nil
}

func unmarshalUint(field reflect.Value, raw string) error {
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fmt.Errorf("parse uint: %w", err)
	}

	if field.OverflowUint(v) {
		return fmt.Errorf("%w: %d does not fit %s", ErrInvalidValue, v, field.Type())
	}

	field.SetUint(v)

	return nil
}

func unmarshalFloat(field reflect.Value, raw string) error {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), field.Type().Bits())
	if err != nil {
		return fmt.Errorf("parse float: %w", err)
	}

	field.SetFloat(v)

	return nil
}

func unmarshalSlice(field reflect.Value, raw string) error {
	if field.Type().Elem() == reflect.TypeFor[byte]() {
		field.SetBytes([]byte(raw))
		return nil
	}

	rawTrimmed := strings.TrimSpace(raw)
	if rawTrimmed == "" {
		field.Set(reflect.MakeSlice(field.Type(), 0, 0))
		return nil
	}

	items := strings.Split(rawTrimmed, ",")
	slice := reflect.MakeSlice(field.Type(), len(items), len(items))

	for i, item := range items {
		elem := slice.Index(i)
		if err := unmarshalValue(elem, strings.TrimSpace(item)); err != nil {
			return fmt.Errorf("slice element %d (%q): %w", i, item, err)
		}
	}

	field.Set(slice)

	return nil
}

func parseBool(s string) (bool, error) {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	switch trimmed {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrInvalidBool, s)
	}
}
