package plan

import (
	"fmt"
	"reflect"
)

func ResolveField(root reflect.Value, indexPath []int) (reflect.Value, bool) {
	current := root

	for _, index := range indexPath {
		for current.Kind() == reflect.Pointer {
			// An optional container left unset is legitimately absent, not an error.
			if current.IsNil() {
				return reflect.Value{}, false
			}

			current = current.Elem()
		}

		if current.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}

		current = current.Field(index)
	}

	return current, true
}

func EnsureField(root reflect.Value, indexPath []int) (reflect.Value, error) {
	current := root

	for i, index := range indexPath {
		for current.Kind() == reflect.Pointer {
			if current.IsNil() {
				if !current.CanSet() {
					return reflect.Value{}, fmt.Errorf("%w: %s is not addressable", ErrNotStruct, formatIndexPath(indexPath[:i]))
				}

				current.Set(reflect.New(current.Type().Elem()))
			}

			current = current.Elem()
		}

		if current.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("%w: %s is not a struct", ErrNotStruct, formatIndexPath(indexPath[:i]))
		}

		current = current.Field(index)
	}

	return current, nil
}
