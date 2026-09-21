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
	// ErrUnsupportedType is returned when a leaf field's kind has no environment
	// decoder, such as a map, an array, or a channel.
	ErrUnsupportedType = errors.New("unsupported field type for environment binding")

	// ErrInvalidEnvValue is returned when the text taken from an environment
	// variable does not decode into its field, and when it decodes to a value
	// that does not fit the field's type.
	//
	// The error names the dotted key and the environment variable. For a field
	// marked secret the value is redacted instead of reproduced.
	ErrInvalidEnvValue = errors.New("invalid environment variable value")

	// ErrInvalidBool is returned when text for a bool field is not an accepted
	// spelling.
	//
	// Accepted for true are 1, t, true, yes, y, and on; accepted for false are 0,
	// f, false, no, n, and off. Case is ignored and surrounding whitespace is
	// trimmed.
	ErrInvalidBool = errors.New("cannot parse boolean value")
)

// Options configures environment variable binding.
//
// Prefix is prepended to every environment variable name, after being trimmed
// and uppercased. An underscore is appended when the result does not already end
// in one.
//
// Lookup resolves an environment variable name. A nil Lookup uses [os.LookupEnv].
//
// OnBind is invoked for every value taken from the environment, with the dotted
// configuration key, the environment variable name, and the raw value. It is not
// called for a key that Lookup does not resolve, and the value of a field marked
// secret is replaced before the call.
//
// ParseDuration parses the text of a [time.Duration] field. A nil ParseDuration
// uses [time.ParseDuration].
type Options struct {
	Prefix        string
	Lookup        func(string) (string, bool)
	OnBind        func(key, envVar, rawVal string)
	ParseDuration func(string) (time.Duration, error)
}

// Apply binds environment variables into target and reports every value it
// takes through [Options.OnBind].
//
// target MUST be a non-nil pointer to a struct. [defaulter.ErrTargetNotPointer]
// is returned otherwise.
//
// A field is matched by its env tag when the tag names one, with the prefixed
// name tried before the bare one and the tag text used as written. Otherwise the
// name is the prefix followed by the upper-snake form of the dotted key, and the
// segments of a nested key are tried joined by "__" before they are tried joined
// by "_".
//
// A field that no variable resolves to is left unchanged. A nil struct pointer
// is allocated only when at least one variable binds within it.
//
// A value that does not decode into its field fails the bind with an error
// wrapping [ErrInvalidEnvValue]. A leaf field whose kind has no decoder fails
// with [ErrUnsupportedType].
//
// A value that does not fit the field's type is rejected rather than wrapped or
// saturated.
//
// A field marked secret by [defaulter.IsSecret] never has its value reproduced.
// [Options.OnBind] receives a redacted value for it, and the error reported for
// a failed bind names only the key and the variable.
func Apply(target any, opts Options) error {
	if target == nil {
		return defaulter.ErrTargetNotPointer
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return defaulter.ErrTargetNotPointer
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return defaulter.ErrTargetNotPointer
	}

	lookup := opts.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}

	prefix := strings.ToUpper(strings.TrimSpace(opts.Prefix))
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}

	return bindEnvStruct(elem, prefix, "", lookup, opts.OnBind, opts.ParseDuration, false, nil, nil)
}

func bindEnvStruct(
	val reflect.Value,
	prefix string,
	parentPath string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDur func(string) (time.Duration, error),
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

		if err := bindField(field, sf, prefix, dottedKey, lookup, onBind, parseDur, fieldSecret, activeTypes, activePtrs); err != nil {
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
	parseDur func(string) (time.Duration, error),
	inheritedSecret bool,
	activeTypes []reflect.Type,
	activePtrs []uintptr,
) error {
	if defaulter.IsNestedStruct(field) {
		return bindEnvStruct(field, prefix, dottedKey, lookup, onBind, parseDur, inheritedSecret, activeTypes, activePtrs)
	}

	if field.Kind() == reflect.Pointer && defaulter.IsNestedStructType(field.Type().Elem()) {
		return bindPointerStructField(field, prefix, dottedKey, lookup, onBind, parseDur, inheritedSecret, activeTypes, activePtrs)
	}

	return bindLeafField(field, sf, prefix, dottedKey, lookup, onBind, parseDur, inheritedSecret)
}

func bindPointerStructField(
	field reflect.Value,
	prefix string,
	dottedKey string,
	lookup func(string) (string, bool),
	onBind func(key, envVar, rawVal string),
	parseDur func(string) (time.Duration, error),
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

		return bindEnvStruct(field.Elem(), prefix, dottedKey, lookup, onBind, parseDur, inheritedSecret, activeTypes, activePtrs)
	}

	tmp := reflect.New(elemType)
	boundCount := 0

	countBind := func(k, envVar, rawVal string) {
		boundCount++

		if onBind != nil {
			onBind(k, envVar, rawVal)
		}
	}

	if err := bindEnvStruct(tmp.Elem(), prefix, dottedKey, lookup, countBind, parseDur, inheritedSecret, activeTypes, activePtrs); err != nil {
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
	parseDur func(string) (time.Duration, error),
	inheritedSecret bool,
) error {
	envVar, rawVal, found := lookupEnvValue(sf, prefix, dottedKey, lookup)
	if !found {
		return nil
	}

	secret := inheritedSecret || defaulter.IsSecret(sf)

	if err := unmarshalValue(field, rawVal, parseDur); err != nil {
		// NB: a secret field MUST NOT have its value reproduced here, and the
		// parse library's own error text quotes the input.
		if secret {
			return fmt.Errorf("%w for %s from %s=[REDACTED]", ErrInvalidEnvValue, dottedKey, envVar)
		}

		return fmt.Errorf("%w for %s from %s=%q: %w", ErrInvalidEnvValue, dottedKey, envVar, rawVal, err)
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
	if isEnvSkipped(sf) {
		return "", "", false
	}

	if envVar, val, ok := lookupEnvTag(sf, prefix, lookup); ok {
		return envVar, val, true
	}

	if !strings.Contains(dottedKey, ".") {
		name := prefix + toUpperSnake(dottedKey)
		if val, ok := lookup(name); ok {
			return name, val, true
		}

		return "", "", false
	}

	parts := strings.Split(dottedKey, ".")
	upperParts := make([]string, 0, len(parts))

	for _, p := range parts {
		upperParts = append(upperParts, toUpperSnake(p))
	}

	doubleName := prefix + strings.Join(upperParts, "__")
	if val, ok := lookup(doubleName); ok {
		return doubleName, val, true
	}

	singleName := prefix + strings.Join(upperParts, "_")
	if val, ok := lookup(singleName); ok {
		return singleName, val, true
	}

	return "", "", false
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

func lookupEnvTag(sf reflect.StructField, prefix string, lookup func(string) (string, bool)) (string, string, bool) {
	tag := sf.Tag.Get("env")
	if tag == "" {
		return "", "", false
	}

	parts := strings.Split(tag, ",")

	tagName := strings.TrimSpace(parts[0])
	if tagName == "" || tagName == "-" {
		return "", "", false
	}

	if prefix != "" {
		prefixed := prefix + tagName
		if val, ok := lookup(prefixed); ok {
			return prefixed, val, true
		}
	}

	if val, ok := lookup(tagName); ok {
		return tagName, val, true
	}

	return "", "", false
}

func unmarshalValue(field reflect.Value, raw string, parseDur func(string) (time.Duration, error)) error {
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
		return unmarshalPointer(field, raw, parseDur)
	case reflect.String:
		field.SetString(raw)
		return nil
	case reflect.Bool:
		return unmarshalBool(field, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return unmarshalInt(field, raw, parseDur)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return unmarshalUint(field, raw)
	case reflect.Float32, reflect.Float64:
		return unmarshalFloat(field, raw)
	case reflect.Slice:
		return unmarshalSlice(field, raw, parseDur)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedType, field.Type())
	}
}

func unmarshalPointer(field reflect.Value, raw string, parseDur func(string) (time.Duration, error)) error {
	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}

	return unmarshalValue(field.Elem(), raw, parseDur)
}

func unmarshalBool(field reflect.Value, raw string) error {
	b, err := parseBool(raw)
	if err != nil {
		return err
	}

	field.SetBool(b)

	return nil
}

func unmarshalInt(field reflect.Value, raw string, parseDur func(string) (time.Duration, error)) error {
	if field.Type() == reflect.TypeFor[time.Duration]() {
		if parseDur != nil {
			d, err := parseDur(raw)
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
		return fmt.Errorf("%w: %d does not fit %s", ErrInvalidEnvValue, v, field.Type())
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
		return fmt.Errorf("%w: %d does not fit %s", ErrInvalidEnvValue, v, field.Type())
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
		return fmt.Errorf("%w: %v does not fit %s", ErrInvalidEnvValue, v, field.Type())
	}

	field.SetFloat(v)

	return nil
}

func unmarshalSlice(field reflect.Value, raw string, parseDur func(string) (time.Duration, error)) error {
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
		if err := unmarshalValue(elem, strings.TrimSpace(item), parseDur); err != nil {
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
