package plan

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var (
	// ErrDuplicateFlag is returned when two fields map to the same flag name or
	// the same shorthand.
	//
	// [ValidateUniqueTargets] reports it for a plan whose targets disagree.
	ErrDuplicateFlag = errors.New("two fields map to the same flag")

	// ErrValueOverflow is returned when a configuration value does not fit the
	// destination field's width.
	//
	// The assignment helpers raise it when a value cannot be stored.
	ErrValueOverflow = errors.New("value does not fit the destination field")
)

// StructTarget reports the struct value cfg points at. A cfg that is nil, is not
// a pointer, or does not point to a struct produces an error wrapping
// [ErrNotStruct].
func StructTarget(cfg any) (reflect.Value, error) {
	if cfg == nil {
		return reflect.Value{}, fmt.Errorf("%w: cfg is nil", ErrNotStruct)
	}

	val := reflect.ValueOf(cfg)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return reflect.Value{}, fmt.Errorf("%w: got %T", ErrNotStruct, cfg)
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("%w: %s is not a struct", ErrNotStruct, elem.Type())
	}

	return elem, nil
}

// OptionalStructTarget reports the struct value behind cfg without treating an
// unusable cfg as an error. It reports false for a cfg that is nil, is not a
// pointer, or does not point to a struct.
func OptionalStructTarget(cfg any) (reflect.Value, bool) {
	if cfg == nil {
		return reflect.Value{}, false
	}

	val := reflect.ValueOf(cfg)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return reflect.Value{}, false
		}

		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}

	return val, true
}

// SeedStorage copies the configuration value a target addresses into its
// detached flag storage, converting it to the storage representation of the
// target's kind.
//
// A container the target's index path cannot reach seeds nothing and reports no
// error.
func SeedStorage(storage reflect.Value, root reflect.Value, target *Target) error {
	source, ok := ResolveField(root, target.IndexPath)
	if !ok {
		return nil
	}

	if source.Kind() == reflect.Pointer {
		if source.IsNil() {
			return nil
		}

		source = source.Elem()
	}

	if err := EncodeLeaf(storage.Elem(), source, target.Kind); err != nil {
		return fmt.Errorf("seed %s: %w", target.Display, err)
	}

	return nil
}

// EncodeLeaf writes src into dst, which holds the detached storage of a leaf of
// the given kind, converting the value to the storage representation.
//
// A value with no storage mapping, and a slice element that is neither
// assignable nor convertible to the destination element type, produce an error
// wrapping [ErrUnsupportedFieldType].
func EncodeLeaf(dst, src reflect.Value, kind Kind) error {
	//nolint:exhaustive // KindUnsupported never reaches storage seeding
	switch kind {
	case KindText:
		text, err := MarshalLeaf(src)
		if err != nil {
			return err
		}

		dst.SetString(text)

		return nil
	case KindString:
		dst.SetString(src.String())

		return nil
	case KindBool:
		dst.SetBool(src.Bool())

		return nil
	case KindInt, KindInt64, KindDuration:
		dst.SetInt(src.Int())

		return nil
	case KindUint, KindUint64:
		dst.SetUint(src.Uint())

		return nil
	case KindFloat32, KindFloat64:
		dst.SetFloat(src.Float())

		return nil
	case KindStringSlice, KindIntSlice, KindInt64Slice:
		return CloneSlice(dst, src)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, src.Type())
	}
}

// CloneSlice replaces dst with a copy of src, converting every element that is
// not directly assignable to the destination element type.
//
// An element that is neither assignable nor convertible produces an error
// wrapping [ErrUnsupportedFieldType].
func CloneSlice(dst, src reflect.Value) error {
	out := reflect.MakeSlice(dst.Type(), src.Len(), src.Len())

	for i := range src.Len() {
		element := src.Index(i)
		targetType := out.Index(i).Type()

		switch {
		case element.Type().AssignableTo(targetType):
			out.Index(i).Set(element)
		case element.Type().ConvertibleTo(targetType):
			out.Index(i).Set(element.Convert(targetType))
		default:
			return fmt.Errorf("%w: %s into %s", ErrUnsupportedFieldType, element.Type(), targetType)
		}
	}

	dst.Set(out)

	return nil
}

// MarshalLeaf renders src as text. A value that implements
// [encoding.TextMarshaler], directly or through its address, is rendered by its
// own marshaller; every other value uses [fmt.Sprint].
func MarshalLeaf(src reflect.Value) (string, error) {
	if marshaller, ok := reflect.TypeAssert[encoding.TextMarshaler](src); ok {
		text, err := marshaller.MarshalText()
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
		}

		return string(text), nil
	}

	if src.CanAddr() {
		if marshaller, ok := reflect.TypeAssert[encoding.TextMarshaler](src.Addr()); ok {
			text, err := marshaller.MarshalText()
			if err != nil {
				return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
			}

			return string(text), nil
		}
	}

	return fmt.Sprint(src.Interface()), nil
}

// EncodeScalar renders a configuration value as the string form the flag's
// parser accepts. A text codec is rendered by its own marshaller, which keeps the
// round trip exact; every other kind uses the natural Go representation.
func EncodeScalar(source reflect.Value, kind Kind) (string, error) {
	//nolint:exhaustive // every non-text, non-string kind uses its natural Go representation
	switch kind {
	case KindText:
		return MarshalLeaf(source)
	case KindString:
		return source.String(), nil
	default:
		return fmt.Sprint(source.Interface()), nil
	}
}

// UnmarshalLeaf decodes value into dst through [encoding.TextUnmarshaler], which
// dst must implement directly or through its address.
//
// A dst with no unmarshaller produces an error wrapping
// [ErrUnsupportedFieldType].
func UnmarshalLeaf(dst reflect.Value, value string) error {
	if dst.CanAddr() {
		if unmarshaller, ok := reflect.TypeAssert[encoding.TextUnmarshaler](dst.Addr()); ok {
			if err := unmarshaller.UnmarshalText([]byte(value)); err != nil {
				return fmt.Errorf("unmarshal %s: %w", dst.Type(), err)
			}

			return nil
		}
	}

	return fmt.Errorf("%w: %s does not implement encoding.TextUnmarshaler", ErrUnsupportedFieldType, dst.Type())
}

// AssignInt stores value in dst, and reports [ErrValueOverflow] when the value
// does not fit the destination field's width.
func AssignInt(dst reflect.Value, value int64) error {
	if dst.OverflowInt(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetInt(value)

	return nil
}

// AssignUint stores value in dst, and reports [ErrValueOverflow] when the value
// does not fit the destination field's width.
func AssignUint(dst reflect.Value, value uint64) error {
	if dst.OverflowUint(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetUint(value)

	return nil
}

// AssignFloat stores value in dst, and reports [ErrValueOverflow] when the value
// does not fit the destination field's width.
func AssignFloat(dst reflect.Value, value float64) error {
	if dst.OverflowFloat(value) {
		return fmt.Errorf("%w: %v does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetFloat(value)

	return nil
}

// AssignSlice replaces dst with the elements of src converted to the
// destination element type. An element that does not fit produces an error
// wrapping [ErrValueOverflow].
func AssignSlice(dst, src reflect.Value) error {
	out := reflect.MakeSlice(dst.Type(), src.Len(), src.Len())

	for i := range src.Len() {
		element := src.Index(i)
		targetType := out.Index(i).Type()

		switch {
		case element.Type().AssignableTo(targetType):
			out.Index(i).Set(element)
		case element.Type().ConvertibleTo(targetType):
			out.Index(i).Set(element.Convert(targetType))
		default:
			return fmt.Errorf("%w: %s into %s", ErrValueOverflow, element.Type(), targetType)
		}
	}

	dst.Set(out)

	return nil
}

// ValidateUniqueTargets reports the first flag name or shorthand that two
// targets claim.
//
// A duplicate is reported as [ErrDuplicateFlag], naming the earlier and later
// claimants by their display paths.
func ValidateUniqueTargets(targets []Target) error {
	names := make(map[string]string, len(targets))
	shorthands := make(map[string]string, len(targets))

	for i := range targets {
		target := &targets[i]

		if previous, duplicate := names[target.Name]; duplicate {
			return fmt.Errorf("%w: %q is claimed by %s and %s", ErrDuplicateFlag, target.Name, previous, target.Display)
		}

		names[target.Name] = target.Display

		if target.Shorthand == "" {
			continue
		}

		if previous, duplicate := shorthands[target.Shorthand]; duplicate {
			return fmt.Errorf("%w: shorthand %q is claimed by %s and %s", ErrDuplicateFlag, target.Shorthand, previous, target.Display)
		}

		shorthands[target.Shorthand] = target.Display
	}

	return nil
}

// CandidateFlagNames reports the flag names an untagged field is matched
// against, in order: the name itself, its kebab-case and snake_case spellings,
// and its last dot-separated element.
func CandidateFlagNames(name string) []string {
	parts := strings.Split(name, ".")
	kebab := strings.Join(parts, "-")
	snake := strings.Join(parts, "_")
	leaf := parts[len(parts)-1]

	return []string{name, kebab, snake, leaf}
}

// ForEachTarget builds the flag plan for root under policy and calls fn once per
// target in declaration order, stopping at the first error.
//
// A [Build] failure is wrapped as "build flag plan". The error fn returns is
// reported unwrapped.
func ForEachTarget(root reflect.Value, policy Policy, fn func(root reflect.Value, target *Target) error) error {
	targets, err := Build(root.Type(), policy)
	if err != nil {
		return fmt.Errorf("build flag plan: %w", err)
	}

	for i := range targets {
		if err := fn(root, &targets[i]); err != nil {
			return err
		}
	}

	return nil
}
