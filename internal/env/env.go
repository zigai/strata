package env

import (
	"encoding"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/strata/internal/defaulter"
)

var (
	ErrUnsupportedType = errors.New("unsupported field type for environment binding")

	ErrInvalidValue = errors.New("invalid environment variable value")

	ErrInvalidBool = errors.New("cannot parse boolean value")
)

type Options struct {
	Prefix        string
	Lookup        func(string) (string, bool)
	OnBind        func(key, envVar, rawVal string)
	ParseDuration func(string) (time.Duration, error)
}

type Var struct {
	Key      string
	Name     string
	Names    []string
	IsSecret bool
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

	return bindEnvStruct(elem, normalizePrefix(opts.Prefix), "", lookup, opts.OnBind, opts.ParseDuration, false, nil, nil)
}

func Describe(typ reflect.Type, prefix string) []Var {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return nil
	}

	var vars []Var

	describeStruct(typ, normalizePrefix(prefix), "", false, nil, &vars)

	return vars
}

func normalizePrefix(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}

	return prefix
}

func describeStruct(typ reflect.Type, prefix, parentPath string, inheritedSecret bool, active []reflect.Type, vars *[]Var) {
	if slices.Contains(active, typ) {
		return
	}

	active = append(active, typ)

	for sf := range typ.Fields() {
		describeField(sf, prefix, parentPath, inheritedSecret, active, vars)
	}
}

func describeField(sf reflect.StructField, prefix, parentPath string, inheritedSecret bool, active []reflect.Type, vars *[]Var) {
	if !sf.IsExported() {
		return
	}

	key := defaulter.FieldKey(sf)
	if key == "-" {
		return
	}

	secret := inheritedSecret || defaulter.IsSecret(sf)

	dottedKey := key
	if sf.Anonymous {
		dottedKey = parentPath
	} else if parentPath != "" {
		dottedKey = parentPath + "." + key
	}

	fieldType := sf.Type
	if fieldType.Kind() == reflect.Pointer && defaulter.IsNestedStructType(fieldType.Elem()) {
		fieldType = fieldType.Elem()
	}

	if fieldType.Kind() == reflect.Struct && defaulter.IsNestedStructType(fieldType) {
		describeStruct(fieldType, prefix, dottedKey, secret, active, vars)
		return
	}

	if !envDecodable(sf.Type) {
		return
	}

	names := candidateNames(sf, prefix, dottedKey)
	if len(names) == 0 {
		return
	}

	*vars = append(*vars, Var{Key: dottedKey, Name: documentedName(sf, names), Names: names, IsSecret: secret})
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
	parentPath string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDuration func(string) (time.Duration, error),
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

	for i := range val.NumField() {
		field := val.Field(i)
		sf := typ.Field(i)

		if !sf.IsExported() {
			continue
		}

		key := defaulter.FieldKey(sf)
		if key == "-" {
			continue
		}

		fieldSecret := inheritedSecret || defaulter.IsSecret(sf)

		dottedKey := key
		if sf.Anonymous {
			dottedKey = parentPath
		} else if parentPath != "" {
			dottedKey = parentPath + "." + key
		}

		if err := bindField(field, sf, prefix, dottedKey, lookup, onBind, parseDuration, fieldSecret, activeTypes, activePtrs); err != nil {
			return err
		}
	}

	return nil
}

func bindField(
	field reflect.Value,
	sf reflect.StructField,
	prefix string,
	dottedKey string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDuration func(string) (time.Duration, error),
	inheritedSecret bool,
	activeTypes []reflect.Type,
	activePtrs []uintptr,
) error {
	if defaulter.IsNestedStruct(field) {
		return bindEnvStruct(field, prefix, dottedKey, lookup, onBind, parseDuration, inheritedSecret, activeTypes, activePtrs)
	}

	if field.Kind() == reflect.Pointer && defaulter.IsNestedStructType(field.Type().Elem()) {
		return bindPointerStructField(field, prefix, dottedKey, lookup, onBind, parseDuration, inheritedSecret, activeTypes, activePtrs)
	}

	return bindLeafField(field, sf, prefix, dottedKey, lookup, onBind, parseDuration, inheritedSecret)
}

func bindPointerStructField(
	field reflect.Value,
	prefix string,
	dottedKey string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDuration func(string) (time.Duration, error),
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

		return bindEnvStruct(field.Elem(), prefix, dottedKey, lookup, onBind, parseDuration, inheritedSecret, activeTypes, activePtrs)
	}

	tmp := reflect.New(elemType)
	boundCount := 0

	countBind := func(k, envVar, rawVal string) {
		boundCount++

		if onBind != nil {
			onBind(k, envVar, rawVal)
		}
	}

	if err := bindEnvStruct(tmp.Elem(), prefix, dottedKey, lookup, countBind, parseDuration, inheritedSecret, activeTypes, activePtrs); err != nil {
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
	dottedKey string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDuration func(string) (time.Duration, error),
	inheritedSecret bool,
) error {
	envVar, rawVal, found := lookupEnvValue(sf, prefix, dottedKey, lookup)
	if !found {
		return nil
	}

	secret := inheritedSecret || defaulter.IsSecret(sf)

	if err := unmarshalValue(field, rawVal, parseDuration); err != nil {
		// NB: a secret field MUST NOT have its value reproduced here, and the
		// parse library's own error text quotes the input.
		if secret {
			return fmt.Errorf("%w for %s from %s=[REDACTED]", ErrInvalidValue, dottedKey, envVar)
		}

		return fmt.Errorf("%w for %s from %s=%q: %w", ErrInvalidValue, dottedKey, envVar, rawVal, err)
	}

	recordVal := rawVal
	if secret {
		recordVal = "[REDACTED]"
	}

	if onBind != nil {
		onBind(dottedKey, envVar, recordVal)
	}

	return nil
}

func lookupEnvValue(
	sf reflect.StructField,
	prefix string,
	dottedKey string,
	lookup func(string) (string, bool),
) (string, string, bool) {
	for _, name := range candidateNames(sf, prefix, dottedKey) {
		if val, ok := lookup(name); ok {
			return name, val, true
		}
	}

	return "", "", false
}

// Derived names require a prefix so generic process variables such as PORT
// and HOST cannot bind accidentally.
func candidateNames(sf reflect.StructField, prefix, dottedKey string) []string {
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

	parts := strings.Split(dottedKey, ".")
	for i, part := range parts {
		parts[i] = toUpperSnake(part)
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
	return strings.ToUpper(defaulter.ToSnakeCase(s))
}

func unmarshalValue(field reflect.Value, raw string, parseDuration func(string) (time.Duration, error)) error {
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
		return unmarshalPointer(field, raw, parseDuration)
	case reflect.String:
		field.SetString(raw)
		return nil
	case reflect.Bool:
		return unmarshalBool(field, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return unmarshalInt(field, raw, parseDuration)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return unmarshalUint(field, raw)
	case reflect.Float32, reflect.Float64:
		return unmarshalFloat(field, raw)
	case reflect.Slice:
		return unmarshalSlice(field, raw, parseDuration)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedType, field.Type())
	}
}

func unmarshalPointer(field reflect.Value, raw string, parseDuration func(string) (time.Duration, error)) error {
	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}

	return unmarshalValue(field.Elem(), raw, parseDuration)
}

func unmarshalBool(field reflect.Value, raw string) error {
	b, err := parseBool(raw)
	if err != nil {
		return err
	}

	field.SetBool(b)

	return nil
}

func unmarshalInt(field reflect.Value, raw string, parseDuration func(string) (time.Duration, error)) error {
	if field.Type() == reflect.TypeFor[time.Duration]() {
		if parseDuration != nil {
			d, err := parseDuration(raw)
			if err != nil {
				return err
			}

			field.SetInt(int64(d))

			return nil
		}

		d, err := time.ParseDuration(raw)
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
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("parse float: %w", err)
	}

	// NB: without this check a float32 field would saturate: 1e40 becomes +Inf.
	if field.OverflowFloat(v) {
		return fmt.Errorf("%w: %v does not fit %s", ErrInvalidValue, v, field.Type())
	}

	field.SetFloat(v)

	return nil
}

func unmarshalSlice(field reflect.Value, raw string, parseDuration func(string) (time.Duration, error)) error {
	if field.Type() == reflect.TypeFor[[]byte]() {
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
		if err := unmarshalValue(elem, strings.TrimSpace(item), parseDuration); err != nil {
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
