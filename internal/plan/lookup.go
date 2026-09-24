package plan

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/zigai/strata/internal/defaulter"
)

var (
	ErrNotStruct            = errors.New("cfg must be a non-nil pointer to a struct")
	ErrUnsupportedFieldType = errors.New("field has no flag mapping")
	errUnknownKey           = errors.New("unknown configuration key")
)

type Target struct {
	IndexPath []int
	Display   string
	ConfigKey string
	Name      string
	Shorthand string
	Usage     string
	Kind      Kind
	LeafType  reflect.Type
	Secret    bool
}

func Lookup(root reflect.Type, key string) (Target, error) {
	index, field, secret, err := resolvedField(root, key)
	if err != nil {
		return Target{}, err
	}

	kind, leaf, err := Classify(field.Type)
	if err != nil {
		return Target{}, err
	}

	return Target{
		IndexPath: index,
		Display:   field.Name,
		ConfigKey: key,
		Name:      strings.ReplaceAll(strings.ReplaceAll(key, ".", "-"), "_", "-"),
		Shorthand: "",
		Usage:     "",
		Kind:      kind,
		LeafType:  leaf,
		Secret:    secret,
	}, nil
}

func FieldType(root reflect.Type, key string) (reflect.Type, error) {
	if root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s", ErrNotStruct, root)
	}

	typ, ok := fieldTypePath(root, strings.Split(key, "."), nil)
	if !ok {
		return nil, fmt.Errorf("%w %q", errUnknownKey, key)
	}

	return typ, nil
}

func IsUnsetSecret(root reflect.Value, key string) bool {
	target, err := Lookup(root.Type(), key)
	if err != nil || !target.Secret {
		return false
	}

	field, ok := ResolveField(root, target.IndexPath)

	return !ok || field.IsZero()
}

func fieldTypePath(typ reflect.Type, parts []string, active []reflect.Type) (reflect.Type, bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if len(parts) == 0 {
		return typ, !defaulter.IsNestedStructType(typ)
	}

	if typ.Kind() == reflect.Map && typ.Key().Kind() == reflect.String {
		return fieldTypePath(typ.Elem(), parts[1:], active)
	}

	if typ.Kind() != reflect.Struct || slices.Contains(active, typ) {
		return nil, false
	}

	return fieldTypeInStruct(typ, parts, append(active, typ))
}

func fieldTypeInStruct(typ reflect.Type, parts []string, active []reflect.Type) (reflect.Type, bool) {
	for field := range typ.Fields() {
		if !field.IsExported() && !field.Anonymous {
			continue
		}

		key := defaulter.FieldKey(field)
		if key == "-" {
			continue
		}

		if field.Anonymous && defaulter.IsNestedStructType(field.Type) {
			if found, ok := fieldTypePath(field.Type, parts, active); ok {
				return found, true
			}
		}

		if key == parts[0] {
			return fieldTypePath(field.Type, parts[1:], active)
		}
	}

	return nil, false
}

func resolvedField(root reflect.Type, key string) ([]int, *reflect.StructField, bool, error) {
	if root.Kind() != reflect.Struct {
		return nil, nil, false, fmt.Errorf("%w: %s", ErrNotStruct, root)
	}

	index, field, secret := lookupField(root, strings.Split(key, "."), nil, false, nil)
	if field == nil {
		return nil, nil, false, fmt.Errorf("%w %q", errUnknownKey, key)
	}

	return index, field, secret, nil
}

func lookupField(typ reflect.Type, parts []string, index []int, secret bool, active []reflect.Type) ([]int, *reflect.StructField, bool) {
	if len(parts) == 0 || slices.Contains(active, typ) {
		return nil, nil, false
	}

	active = append(active, typ)
	for i := range typ.NumField() {
		field := typ.Field(i)

		path, leaf, fieldSecret := lookupCandidate(field, parts, append(append([]int(nil), index...), i), secret, active)
		if leaf != nil {
			return path, leaf, fieldSecret
		}
	}

	return nil, nil, false
}

func lookupCandidate(field reflect.StructField, parts []string, path []int, secret bool, active []reflect.Type) ([]int, *reflect.StructField, bool) {
	if !field.IsExported() && !field.Anonymous {
		return nil, nil, false
	}

	key := defaulter.FieldKey(field)
	if key == "-" {
		return nil, nil, false
	}

	base := field.Type
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}

	fieldSecret := secret || defaulter.IsSecret(field)
	if field.Anonymous && defaulter.IsNestedStructType(base) {
		return lookupField(base, parts, path, fieldSecret, active)
	}

	if key != parts[0] {
		return nil, nil, false
	}

	if len(parts) == 1 {
		if defaulter.IsNestedStructType(base) {
			return nil, nil, false
		}

		return path, &field, fieldSecret
	}

	if defaulter.IsNestedStructType(base) {
		return lookupField(base, parts[1:], path, fieldSecret, active)
	}

	return nil, nil, false
}
