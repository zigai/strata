package plan

import (
	"encoding"
	"fmt"
	"reflect"
	"time"
)

const (
	// KindUnsupported reports a type that has no CLI flag mapping.
	KindUnsupported Kind = iota

	// KindString is a string leaf.
	KindString

	// KindBool is a boolean leaf.
	KindBool

	// KindInt is an int leaf.
	KindInt

	// KindInt64 is a signed integer leaf that is not an int.
	KindInt64

	// KindUint is a uint leaf.
	KindUint

	// KindUint64 is an unsigned integer leaf that is not a uint.
	KindUint64

	// KindFloat32 is a float32 leaf.
	KindFloat32

	// KindFloat64 is a float64 leaf.
	KindFloat64

	// KindDuration is a [time.Duration] leaf, which carries a duration parser in
	// place of the integer parser.
	KindDuration

	// KindText is a leaf that satisfies the text codec contract, and is carried
	// as text.
	KindText

	// KindStringSlice is a []string leaf.
	KindStringSlice

	// KindIntSlice is a []int leaf.
	KindIntSlice

	// KindInt64Slice is a []int64 leaf.
	KindInt64Slice
)

var (
	//nolint:exhaustive // reflect.Kind has many values with no flag representation; the lookup reports them
	primitiveKinds = map[reflect.Kind]Kind{
		reflect.String:  KindString,
		reflect.Bool:    KindBool,
		reflect.Int:     KindInt,
		reflect.Int8:    KindInt64,
		reflect.Int16:   KindInt64,
		reflect.Int32:   KindInt64,
		reflect.Int64:   KindInt64,
		reflect.Uint:    KindUint,
		reflect.Uint8:   KindUint64,
		reflect.Uint16:  KindUint64,
		reflect.Uint32:  KindUint64,
		reflect.Uint64:  KindUint64,
		reflect.Float32: KindFloat32,
		reflect.Float64: KindFloat64,
	}

	//nolint:exhaustive // most slice element kinds have no flag representation; the lookup reports them
	sliceElementKinds = map[reflect.Kind]Kind{
		reflect.String: KindStringSlice,
		reflect.Int:    KindIntSlice,
		reflect.Int64:  KindInt64Slice,
	}

	// storageTypes maps a flag kind to the canonical type its detached storage
	// holds. A narrow integer widens to the framework's own width and a text
	// codec widens to string; the write-back path converts with a range check.
	//nolint:exhaustive // KindUnsupported never reaches storage; StorageTypeFor falls back to the leaf type
	storageTypes = map[Kind]reflect.Type{
		KindText:        reflect.TypeFor[string](),
		KindString:      reflect.TypeFor[string](),
		KindBool:        reflect.TypeFor[bool](),
		KindInt:         reflect.TypeFor[int](),
		KindInt64:       reflect.TypeFor[int64](),
		KindUint:        reflect.TypeFor[uint](),
		KindUint64:      reflect.TypeFor[uint64](),
		KindFloat32:     reflect.TypeFor[float32](),
		KindFloat64:     reflect.TypeFor[float64](),
		KindDuration:    reflect.TypeFor[time.Duration](),
		KindStringSlice: reflect.TypeFor[[]string](),
		KindIntSlice:    reflect.TypeFor[[]int](),
		KindInt64Slice:  reflect.TypeFor[[]int64](),
	}
)

// Kind classifies a leaf type into the flag family that represents it.
type Kind int

// Classify maps a Go type to a flag kind and reports the type's leaf form. A
// pointer is unwrapped, so the returned leaf type is never itself a pointer.
//
// A type with no flag mapping produces an error wrapping
// [ErrUnsupportedFieldType].
func Classify(typ reflect.Type) (Kind, reflect.Type, error) {
	leaf := typ
	if leaf.Kind() == reflect.Pointer {
		leaf = leaf.Elem()
	}

	if leaf.Kind() == reflect.Pointer {
		return KindUnsupported, nil, fmt.Errorf("%w: %s is a pointer to a pointer", ErrUnsupportedFieldType, typ)
	}

	if implementsTextCodec(typ) {
		return KindText, leaf, nil
	}

	if leaf == reflect.TypeFor[time.Duration]() {
		return KindDuration, leaf, nil
	}

	if leaf.Kind() == reflect.Slice {
		return classifySlice(leaf)
	}

	if kind, ok := primitiveKinds[leaf.Kind()]; ok {
		return kind, leaf, nil
	}

	return KindUnsupported, nil, fmt.Errorf("%w: type %s has no flag mapping", ErrUnsupportedFieldType, typ)
}

// StorageTypeFor reports the canonical type that detached storage holds for a
// flag kind. A kind with no entry in storageTypes falls back to the leaf type.
func StorageTypeFor(kind Kind, leaf reflect.Type) reflect.Type {
	if storage, ok := storageTypes[kind]; ok {
		return storage
	}

	return leaf
}

func classifySlice(leaf reflect.Type) (Kind, reflect.Type, error) {
	if kind, ok := sliceElementKinds[leaf.Elem().Kind()]; ok {
		return kind, leaf, nil
	}

	return KindUnsupported, nil, fmt.Errorf("%w: slice element type %s has no flag mapping", ErrUnsupportedFieldType, leaf.Elem())
}

// implementsTextCodec reports whether typ satisfies the text codec contract: the
// value must be marshalable and its address unmarshalable.
//
// The marshaller lets the flag carry a default; the unmarshaller decodes a parsed
// string into the field.
func implementsTextCodec(typ reflect.Type) bool {
	marshaller := reflect.TypeFor[encoding.TextMarshaler]()
	unmarshaller := reflect.TypeFor[encoding.TextUnmarshaler]()

	if typ.Kind() == reflect.Pointer {
		return typ.Implements(marshaller) && typ.Implements(unmarshaller)
	}

	return (typ.Implements(marshaller) || reflect.PointerTo(typ).Implements(marshaller)) &&
		reflect.PointerTo(typ).Implements(unmarshaller)
}

func isContainerType(typ reflect.Type) bool {
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}

	if base.Kind() != reflect.Struct {
		return false
	}

	return !implementsTextCodec(typ)
}
