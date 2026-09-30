package plan

import (
	"encoding"
	"fmt"
	"reflect"
	"time"
)

const (
	KindUnsupported Kind = iota

	KindString

	KindBool

	KindInt

	KindInt64

	KindUint

	KindUint64

	KindFloat32

	KindFloat64

	KindDuration

	KindText

	KindStringSlice

	KindIntSlice

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

type Kind int

func Classify(typ reflect.Type) (Kind, reflect.Type, error) {
	leaf := typ
	if leaf.Kind() == reflect.Pointer {
		leaf = leaf.Elem()
	}

	if leaf.Kind() == reflect.Pointer {
		return KindUnsupported, nil, fmt.Errorf("%w: %s is a pointer to a pointer", ErrUnsupportedFieldType, typ)
	}

	if implementsTextCodec(typ) {
		if leaf.PkgPath() == "github.com/zigai/strata" && leaf.Name() == "Duration" {
			return KindDuration, leaf, nil
		}

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

// The marshaler lets a flag carry a default; the unmarshaler decodes parsed
// text into the field.
func implementsTextCodec(typ reflect.Type) bool {
	marshaler := reflect.TypeFor[encoding.TextMarshaler]()
	unmarshaler := reflect.TypeFor[encoding.TextUnmarshaler]()

	if typ.Kind() == reflect.Pointer {
		return typ.Implements(marshaler) && typ.Implements(unmarshaler)
	}

	return (typ.Implements(marshaler) || reflect.PointerTo(typ).Implements(marshaler)) &&
		reflect.PointerTo(typ).Implements(unmarshaler)
}
