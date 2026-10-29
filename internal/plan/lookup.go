package plan

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/zigai/strata/internal/keypath"
	"github.com/zigai/strata/internal/layout"
)

var (
	ErrNotStruct            = errors.New("cfg must be a non-nil pointer to a struct")
	ErrUnsupportedFieldType = errors.New("field has no flag mapping")
	errUnknownKey           = errors.New("unknown configuration key")
)

type Leaf struct {
	IndexPath []int
	FieldName string
	ConfigKey jsontext.Pointer
	FlagName  string
	Shorthand string
	Usage     string
	Kind      Kind
	Type      reflect.Type
	IsSecret  bool
}

// LeafFor resolves a dotted key to a field that one flag can hold. The key may
// run through nested and promoted structs, but not through maps or sequences,
// which have no fixed field to bind.
func LeafFor(root reflect.Type, dotted string) (Leaf, error) {
	key, err := keypath.Parse(dotted)
	if err != nil {
		return Leaf{}, err
	}

	if root.Kind() != reflect.Struct {
		return Leaf{}, fmt.Errorf("%w: %s", ErrNotStruct, root)
	}

	var (
		index  []int
		field  reflect.StructField
		secret bool
	)

	current := root
	for token := range key.Tokens() {
		if !layout.IsNestedStructType(current) {
			return Leaf{}, fmt.Errorf(`%w "%s"`, errUnknownKey, keypath.Format(key))
		}

		found, ok := layout.Of(deref(current)).Field(token)
		if !ok {
			return Leaf{}, fmt.Errorf(`%w "%s"`, errUnknownKey, keypath.Format(key))
		}

		index = append(index, found.Index...)
		field = found.Member.Field
		secret = secret || found.Secret
		current = field.Type
	}

	if layout.IsNestedStructType(current) {
		return Leaf{}, fmt.Errorf(`%w "%s"`, errUnknownKey, keypath.Format(key))
	}

	kind, leaf, err := Classify(field.Type)
	if err != nil {
		return Leaf{}, err
	}

	return Leaf{
		IndexPath: index,
		FieldName: field.Name,
		ConfigKey: key,
		FlagName:  FlagName(key),
		Shorthand: "",
		Usage:     "",
		Kind:      kind,
		Type:      leaf,
		IsSecret:  secret,
	}, nil
}

// FlagName spells key as a flag name: its tokens joined by hyphens, with
// underscores also becoming hyphens, so db.max_conns is db-max-conns.
func FlagName(key jsontext.Pointer) string {
	var tokens []string
	for token := range key.Tokens() {
		tokens = append(tokens, strings.ReplaceAll(token, "_", "-"))
	}

	return strings.Join(tokens, "-")
}

// FieldType resolves key to the type of the value it addresses. Under a struct
// a token names a field, or an entry of its inline map; under a string-keyed
// map it names an entry. The value must not be a nested struct.
func FieldType(root reflect.Type, key jsontext.Pointer) (reflect.Type, error) {
	if root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s", ErrNotStruct, root)
	}

	current := root
	for token := range key.Tokens() {
		next, ok := childType(deref(current), token)
		if !ok {
			return nil, fmt.Errorf(`%w "%s"`, errUnknownKey, keypath.Format(key))
		}

		current = next
	}

	if layout.IsNestedStructType(current) {
		return nil, fmt.Errorf(`%w "%s"`, errUnknownKey, keypath.Format(key))
	}

	return deref(current), nil
}

func IsUnsetSecret(root reflect.Value, key string) bool {
	target, err := LeafFor(root.Type(), key)
	if err != nil || !target.IsSecret {
		return false
	}

	field, ok := ResolveField(root, target.IndexPath)

	return !ok || field.IsZero()
}

func childType(typ reflect.Type, token string) (reflect.Type, bool) {
	switch {
	case layout.IsNestedStructType(typ):
		structLayout := layout.Of(typ)
		if field, ok := structLayout.Field(token); ok {
			return field.Member.Field.Type, true
		}

		if structLayout.Rest != nil {
			return structLayout.Rest.Member.Field.Type.Elem(), true
		}

		return nil, false
	case typ.Kind() == reflect.Map && typ.Key().Kind() == reflect.String:
		return typ.Elem(), true
	default:
		return nil, false
	}
}

func deref(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}
