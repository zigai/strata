package layout

import (
	"encoding"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrInvalid reports a struct whose configuration keys cannot be laid out: an
// inline declaration on a type that cannot hold keys, more than one inline
// map, or two fields that claim the same key at the same depth.
var ErrInvalid = errors.New("invalid configuration layout")

var (
	structs sync.Map // reflect.Type -> *Struct

	selfEncoders = [...]reflect.Type{
		reflect.TypeFor[encoding.TextMarshaler](),
		reflect.TypeFor[json.Marshaler](),
		reflect.TypeFor[json.MarshalerTo](),
		reflect.TypeFor[yaml.Marshaler](),
	}
)

// Member is a field of one struct type as configuration reads it.
//
// A promoted member is an embedded struct without a name, or an inline struct;
// its keys join the keys of the struct that holds it. A rest member is an
// inline map, which receives every key no field claims.
type Member struct {
	Field    reflect.StructField
	Key      string
	Promoted bool
	Rest     bool
	Secret   bool
}

// Field is one configuration key of a struct type. Index runs from the struct
// through any promoted members to the declaring field, and Owner is the struct
// type that declares it. Secret holds when the field or a promoted member on
// the way is secret.
type Field struct {
	Key    string
	Index  []int
	Owner  reflect.Type
	Member Member
	Secret bool
}

// Struct is the configuration layout of a struct type: the members it
// declares, the keys it exposes in declaration order, and its inline map.
type Struct struct {
	Members []Member
	Fields  []Field
	Rest    *Field
	keys    map[string]int
	err     error
}

type candidate struct {
	field Field
	depth int
}

// Of returns the layout of a struct type. A layout that cannot be built keeps
// the keys it could place and reports why through [Struct.Err].
func Of(typ reflect.Type) *Struct {
	if cached, ok := structs.Load(typ); ok {
		layout, _ := cached.(*Struct)
		return layout
	}

	layout := build(typ)
	structs.Store(typ, layout)

	return layout
}

// Field returns the field that owns key.
func (s *Struct) Field(key string) (Field, bool) {
	index, ok := s.keys[key]
	if !ok {
		var missing Field

		return missing, false
	}

	return s.Fields[index], true
}

// Err reports why the layout is invalid, wrapping [ErrInvalid].
func (s *Struct) Err() error { return s.err }

// Validate checks the layout of typ and of every struct type reachable through
// its fields, pointers, slices, arrays, and maps.
func Validate(typ reflect.Type) error {
	return validate(typ, make(map[reflect.Type]bool))
}

// MembersOf lists the members a struct type declares, in declaration order.
// Unexported fields that are not embedded structs and fields excluded with a
// "-" key are left out.
func MembersOf(typ reflect.Type) []Member {
	return Of(typ).Members
}

// IsInline reports whether a field's tags declare it inline: a strata or yaml
// tag with the inline option, or a json tag with the embed option.
func IsInline(field reflect.StructField) bool {
	return hasOption(field.Tag.Get("strata"), "inline") ||
		hasOption(field.Tag.Get("yaml"), "inline") ||
		hasOption(field.Tag.Get("json"), "embed")
}

// EncodesItself reports whether typ writes itself through a text, JSON, or
// YAML marshaler. Such a struct is one value: embedding it does not promote
// its fields.
func EncodesItself(typ reflect.Type) bool {
	for _, marshaler := range selfEncoders {
		if typ.Implements(marshaler) || reflect.PointerTo(typ).Implements(marshaler) {
			return true
		}
	}

	return false
}

func validate(typ reflect.Type, seen map[reflect.Type]bool) error {
	if seen[typ] {
		return nil
	}

	seen[typ] = true

	if typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		return validate(typ.Elem(), seen)
	}

	if !IsNestedStructType(typ) {
		return nil
	}

	layout := Of(typ)
	if layout.err != nil {
		return layout.err
	}

	for _, field := range layout.Fields {
		if err := validate(field.Member.Field.Type, seen); err != nil {
			return err
		}
	}

	if layout.Rest != nil {
		return validate(layout.Rest.Member.Field.Type, seen)
	}

	return nil
}

func build(typ reflect.Type) *Struct {
	layout := &Struct{Members: members(typ), Fields: nil, Rest: nil, keys: make(map[string]int), err: nil}

	var (
		found []candidate
		rests []candidate
		errs  []error
	)

	collect(typ, nil, 0, false, []reflect.Type{typ}, &found, &rests, &errs)

	shallowest := make(map[string]int)
	claims := make(map[string][]candidate)

	for _, item := range found {
		depth, seen := shallowest[item.field.Key]
		if !seen || item.depth < depth {
			shallowest[item.field.Key] = item.depth
			claims[item.field.Key] = nil
		}

		if item.depth == shallowest[item.field.Key] {
			claims[item.field.Key] = append(claims[item.field.Key], item)
		}
	}

	slices.SortStableFunc(found, func(a, b candidate) int { return slices.Compare(a.field.Index, b.field.Index) })

	for _, item := range found {
		owners := claims[item.field.Key]
		if !slices.Equal(owners[0].field.Index, item.field.Index) {
			continue
		}

		if len(owners) > 1 {
			errs = append(errs, fmt.Errorf("%w: %s: fields %s claim the key %q", ErrInvalid, typ, fieldNames(owners), item.field.Key))
			continue
		}

		layout.keys[item.field.Key] = len(layout.Fields)
		layout.Fields = append(layout.Fields, item.field)
	}

	switch len(rests) {
	case 0:
	case 1:
		layout.Rest = &rests[0].field
	default:
		errs = append(errs, fmt.Errorf("%w: %s: more than one inline map: %s", ErrInvalid, typ, fieldNames(rests)))
	}

	layout.err = errors.Join(errs...)

	return layout
}

// collect lists the keyed fields of typ in declaration order. A promoted
// member's fields follow at the next depth, so a field declared closer to the
// struct owns its key, as in Go.
func collect(typ reflect.Type, prefix []int, depth int, secret bool, active []reflect.Type, found, rests *[]candidate, errs *[]error) {
	var promoted []Member

	for _, member := range members(typ) {
		if err := memberError(typ, member); err != nil {
			*errs = append(*errs, err)
			continue
		}

		index := append(slices.Clone(prefix), member.Field.Index...)
		field := Field{Key: member.Key, Index: index, Owner: typ, Member: member, Secret: secret || member.Secret}

		switch {
		case member.Promoted:
			promoted = append(promoted, member)
		case member.Rest:
			*rests = append(*rests, candidate{field: field, depth: depth})
		default:
			*found = append(*found, candidate{field: field, depth: depth})
		}
	}

	for _, member := range promoted {
		base := deref(member.Field.Type)
		if slices.Contains(active, base) {
			continue
		}

		index := append(slices.Clone(prefix), member.Field.Index...)
		collect(base, index, depth+1, secret || member.Secret, append(active, base), found, rests, errs)
	}
}

func memberError(owner reflect.Type, member Member) error {
	switch {
	case member.Promoted && EncodesItself(deref(member.Field.Type)):
		return fmt.Errorf("%w: %s.%s: an inline struct must not marshal itself", ErrInvalid, owner, member.Field.Name)
	case member.Rest:
		return checkRest(owner, member.Field)
	case !member.Promoted && IsInline(member.Field):
		return fmt.Errorf("%w: %s.%s: an inline field must be a struct or a map, not %s", ErrInvalid, owner, member.Field.Name, member.Field.Type)
	}

	return nil
}

func members(typ reflect.Type) []Member {
	var list []Member

	for field := range typ.Fields() {
		if !field.IsExported() && !field.Anonymous {
			continue
		}

		inline := IsInline(field)
		promoted := promotes(field)

		if !promoted && !field.IsExported() {
			continue
		}

		key := FieldKey(field)
		if key == "-" && !inline {
			continue
		}

		if promoted {
			key = ""
		}

		list = append(list, Member{
			Field:    field,
			Key:      key,
			Promoted: promoted,
			Rest:     inline && !promoted && field.Type.Kind() == reflect.Map,
			Secret:   IsSecret(field),
		})
	}

	return list
}

func promotes(field reflect.StructField) bool {
	if !IsNestedStructType(field.Type) {
		return false
	}

	return IsInline(field) || field.Anonymous && !named(field) && !EncodesItself(deref(field.Type))
}

func checkRest(owner reflect.Type, field reflect.StructField) error {
	if field.Type.Key().Kind() != reflect.String {
		return fmt.Errorf("%w: %s.%s: an inline map needs string keys, not %s", ErrInvalid, owner, field.Name, field.Type.Key())
	}

	if !field.IsExported() {
		return fmt.Errorf("%w: %s.%s: an inline map must be exported", ErrInvalid, owner, field.Name)
	}

	return nil
}

func named(field reflect.StructField) bool {
	for _, tagName := range candidateTags {
		name, _, _ := strings.Cut(field.Tag.Get(tagName), ",")
		if strings.TrimSpace(name) != "" {
			return true
		}
	}

	return false
}

func hasOption(tag, option string) bool {
	_, options, found := strings.Cut(tag, ",")
	if !found {
		return false
	}

	for part := range strings.SplitSeq(options, ",") {
		if strings.TrimSpace(part) == option {
			return true
		}
	}

	return false
}

func fieldNames(items []candidate) string {
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.field.Owner.String() + "." + item.field.Member.Field.Name
	}

	return strings.Join(names, ", ")
}

func deref(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}
