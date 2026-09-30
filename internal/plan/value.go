package plan

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
)

var ErrValueOverflow = errors.New("value does not fit the destination field")

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

func SeedStorage(storage reflect.Value, root reflect.Value, target *Leaf) error {
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
		return fmt.Errorf("seed %s: %w", target.FieldName, err)
	}

	return nil
}

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

func MarshalLeaf(src reflect.Value) (string, error) {
	if marshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](src); ok {
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
		}

		return string(text), nil
	}

	target := src
	if !target.CanAddr() {
		tmp := reflect.New(src.Type()).Elem()
		tmp.Set(src)
		target = tmp
	}

	if marshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](target.Addr()); ok {
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
		}

		return string(text), nil
	}

	return fmt.Sprint(src.Interface()), nil
}

func UnmarshalLeaf(dst reflect.Value, value string) error {
	if dst.CanAddr() {
		if unmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](dst.Addr()); ok {
			if err := unmarshaler.UnmarshalText([]byte(value)); err != nil {
				return fmt.Errorf("unmarshal %s: %w", dst.Type(), err)
			}

			return nil
		}
	}

	return fmt.Errorf("%w: %s does not implement encoding.TextUnmarshaler", ErrUnsupportedFieldType, dst.Type())
}

func AssignInt(dst reflect.Value, value int64) error {
	if dst.OverflowInt(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetInt(value)

	return nil
}

func AssignUint(dst reflect.Value, value uint64) error {
	if dst.OverflowUint(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetUint(value)

	return nil
}

func AssignFloat(dst reflect.Value, value float64) error {
	if dst.OverflowFloat(value) {
		return fmt.Errorf("%w: %v does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetFloat(value)

	return nil
}

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
