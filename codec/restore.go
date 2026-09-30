package codec

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Untouched values keep their identity and unexported state. Decoded pointers
// and maps are updated in place; decoded sequences and map entries start
// from their zero values.
func decodeYAMLMirror(doc *yaml.Node, dst reflect.Value, mirror *keyedMirror) error {
	staged := reflect.New(mirror.typ)
	staged.Elem().Set(convertKeyed(dst, mirror))

	if err := doc.Decode(staged.Interface()); err != nil {
		return fmt.Errorf("%w: yaml unmarshal: %w", ErrMalformed, err)
	}

	// NB: a second conversion, not a copy of staged, so the decode above cannot
	// have written into it.
	restoreStruct(staged.Elem(), convertKeyed(dst, mirror), dst, mirror)

	return nil
}

// base is the source value before decoding and orig its original mirror;
// either is invalid when absent. When src still equals orig, base is unchanged.
func restoreKeyed(src, orig reflect.Value, typ reflect.Type, base reflect.Value) reflect.Value {
	mirror := mirrorOfMode(typ, mirrorMode{OmitSecrets: false, StringifyDurations: false})
	if mirror.typ == nil {
		return src
	}

	if base.IsValid() && orig.IsValid() && reflect.DeepEqual(src.Interface(), orig.Interface()) {
		return base
	}

	//nolint:exhaustive // buildMirror produces mirrors for these kinds only
	switch typ.Kind() {
	case reflect.Pointer:
		return restorePointer(src, orig, typ, base)
	case reflect.Slice:
		if src.IsNil() {
			return reflect.Zero(typ)
		}

		out := reflect.MakeSlice(typ, src.Len(), src.Len())
		restoreElements(src, out)

		return out
	case reflect.Array:
		out := reflect.New(typ).Elem()
		restoreElements(src, out)

		return out
	case reflect.Map:
		return restoreMap(src, orig, typ, base)
	case reflect.Struct:
		out := reflect.New(typ).Elem()
		if base.IsValid() {
			out.Set(base)
		}

		restoreStruct(src, orig, out, mirror)

		return out
	}

	return src
}

func restorePointer(src, orig reflect.Value, typ reflect.Type, base reflect.Value) reflect.Value {
	if src.IsNil() {
		return reflect.Zero(typ)
	}

	if !base.IsValid() || base.IsNil() {
		ptr := reflect.New(typ.Elem())
		ptr.Elem().Set(restoreKeyed(src.Elem(), reflect.Value{}, typ.Elem(), reflect.Value{}))

		return ptr
	}

	var origElem reflect.Value
	if orig.IsValid() && !orig.IsNil() {
		origElem = orig.Elem()
	}

	base.Elem().Set(restoreKeyed(src.Elem(), origElem, typ.Elem(), base.Elem()))

	return base
}

func restoreMap(src, orig reflect.Value, typ reflect.Type, base reflect.Value) reflect.Value {
	if src.IsNil() {
		return reflect.Zero(typ)
	}

	out, inPlace := base, base.IsValid() && !base.IsNil()
	if !inPlace {
		out = reflect.MakeMapWithSize(typ, src.Len())
	}

	for iter := src.MapRange(); iter.Next(); {
		var elemOrig, elemBase reflect.Value
		if inPlace && orig.IsValid() && !orig.IsNil() {
			elemOrig, elemBase = orig.MapIndex(iter.Key()), base.MapIndex(iter.Key())
		}

		out.SetMapIndex(iter.Key(), restoreEntry(iter.Value(), elemOrig, typ.Elem(), elemBase))
	}

	return out
}

func restoreEntry(src, orig reflect.Value, typ reflect.Type, base reflect.Value) reflect.Value {
	if base.IsValid() && orig.IsValid() && reflect.DeepEqual(src.Interface(), orig.Interface()) {
		return base
	}

	return restoreKeyed(src, reflect.Value{}, typ, reflect.Value{})
}

func restoreElements(src, dst reflect.Value) {
	for i := range src.Len() {
		dst.Index(i).Set(restoreKeyed(src.Index(i), reflect.Value{}, dst.Type().Elem(), reflect.Value{}))
	}
}

func restoreStruct(src, orig, dst reflect.Value, mirror *keyedMirror) {
	for i, path := range mirror.paths {
		value := src.Field(i)

		var origField reflect.Value
		if orig.IsValid() {
			origField = orig.Field(i)
		}

		field, err := dst.FieldByIndexErr(path)
		if err != nil {
			if value.IsZero() || !allocateEmbedded(dst, path) {
				continue
			}

			field = dst.FieldByIndex(path)
		}

		if field.CanSet() {
			field.Set(restoreKeyed(value, origField, field.Type(), field))
		}
	}
}

func allocateEmbedded(dst reflect.Value, path []int) bool {
	cur := dst

	for _, index := range path[:len(path)-1] {
		cur = cur.Field(index)
		if cur.Kind() != reflect.Pointer {
			continue
		}

		if cur.IsNil() {
			if !cur.CanSet() {
				return false
			}

			cur.Set(reflect.New(cur.Type().Elem()))
		}

		cur = cur.Elem()
	}

	return true
}
