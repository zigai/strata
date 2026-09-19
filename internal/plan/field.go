package plan

import (
	"fmt"
	"reflect"
)

// ResolveField walks indexPath from root and dereferences pointers along the way.
// It reports false when a nil pointer blocks the path.
//
// A blocked path is not an error: an optional container the configuration never
// populated is legitimately absent.
func ResolveField(root reflect.Value, indexPath []int) (reflect.Value, bool) {
	current := root

	for _, index := range indexPath {
		for current.Kind() == reflect.Pointer {
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

// EnsureField resolves indexPath from root and allocates nil pointer containers
// along the way. It is the write path for an optional subtree the configuration
// left unset and a CLI value supplies.
//
// A nil container that is not addressable, or a path element that is not a
// struct, produces an error wrapping [ErrNotStruct].
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
