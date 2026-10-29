package defaulter

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/zigai/strata/internal/keypath"
	"github.com/zigai/strata/internal/layout"
)

var (
	ErrInvalidTarget = errors.New("target must be a non-nil pointer to a struct")

	ErrSetDefaultsPanicked = errors.New("set defaults panicked")
)

type Defaulter interface {
	SetDefaults()
}

type secretVisit struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}

type visitor struct {
	report func(key jsontext.Pointer, rawVal string)
	apply  bool
}

// scope is where a struct value sits: the key it is reached by, and the layout
// whose keys it contributes to, with the index path from that layout's struct.
// A promoted struct shares the scope of the struct that holds it.
type scope struct {
	key    jsontext.Pointer
	layout *layout.Struct
	index  []int
}

func Apply(target any, onDefault func(key jsontext.Pointer, rawVal string)) error {
	if target == nil {
		return ErrInvalidTarget
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return ErrInvalidTarget
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return ErrInvalidTarget
	}

	ensureEmbeddedPointers(elem)

	if d, ok := target.(Defaulter); ok && !isPromotedSetDefaults(target) {
		if err := callSetDefaults(d); err != nil {
			return err
		}
	}

	return recurseDefaults(elem, rootScope(elem.Type()), &visitor{report: onDefault, apply: true}, false, nil, nil)
}

func Report(target any, onDefault func(key jsontext.Pointer, rawVal string)) error {
	if target == nil {
		return ErrInvalidTarget
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() || val.Elem().Kind() != reflect.Struct {
		return ErrInvalidTarget
	}

	return recurseDefaults(val.Elem(), rootScope(val.Elem().Type()), &visitor{report: onDefault, apply: false}, false, nil, nil)
}

func ContainsSecrets(src reflect.Value) bool {
	return containsSecrets(src, make(map[secretVisit]bool))
}

func containsSecrets(src reflect.Value, seen map[secretVisit]bool) bool {
	if layout.IsSecretType(src.Type()) {
		return true
	}

	if src.Kind() == reflect.Pointer || src.Kind() == reflect.Map || src.Kind() == reflect.Slice {
		if src.IsNil() {
			return false
		}

		visit := secretVisit{typ: src.Type(), pointer: uintptr(src.UnsafePointer()), length: 0}
		if src.Kind() == reflect.Slice {
			visit.length = src.Len()
		}

		if seen[visit] {
			return false
		}

		seen[visit] = true
	}

	return containsSecretValues(src, seen)
}

func containsSecretValues(src reflect.Value, seen map[secretVisit]bool) bool {
	switch src.Kind() {
	case reflect.Interface, reflect.Pointer:
		return !src.IsNil() && containsSecrets(src.Elem(), seen)
	case reflect.Struct:
		return containsSecretFields(src, seen)
	case reflect.Slice, reflect.Array:
		for index := range src.Len() {
			if containsSecrets(src.Index(index), seen) {
				return true
			}
		}
	case reflect.Map:
		for iter := src.MapRange(); iter.Next(); {
			if containsSecrets(iter.Value(), seen) {
				return true
			}
		}
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.String, reflect.UnsafePointer:
		return false
	}

	return false
}

func containsSecretFields(src reflect.Value, seen map[secretVisit]bool) bool {
	for field := range src.Type().Fields() {
		if (field.IsExported() || field.Anonymous) && (layout.IsSecret(field) || containsSecrets(src.Field(field.Index[0]), seen)) {
			return true
		}
	}

	return false
}

func ensureEmbeddedPointers(val reflect.Value) {
	ensureEmbeddedPointersVisited(val, nil)
}

func ensureEmbeddedPointersVisited(val reflect.Value, visited map[reflect.Type]bool) {
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	if visited[typ] {
		return
	}

	if visited == nil {
		visited = make(map[reflect.Type]bool)
	}

	visited[typ] = true
	defer delete(visited, typ)

	for i := range val.NumField() {
		sf := typ.Field(i)
		field := val.Field(i)

		if sf.Anonymous && field.CanSet() && field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct {
			elemType := field.Type().Elem()
			if visited[elemType] {
				continue
			}

			if field.IsNil() {
				field.Set(reflect.New(elemType))
			}

			ensureEmbeddedPointersVisited(field.Elem(), visited)
		}
	}
}

func callSetDefaults(d Defaulter) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrSetDefaultsPanicked, r)
		}
	}()

	d.SetDefaults()

	return nil
}

func rootScope(typ reflect.Type) scope {
	return scope{key: "", layout: layout.Of(typ), index: nil}
}

func (at scope) promoted(member layout.Member) scope {
	return scope{key: at.key, layout: at.layout, index: append(slices.Clone(at.index), member.Field.Index...)}
}

func (at scope) nested(key string, typ reflect.Type) scope {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return scope{key: at.key.AppendToken(key), layout: layout.Of(typ), index: nil}
}

// owns reports whether member holds key in this scope; a field that a
// shallower field shadows contributes nothing.
func (at scope) owns(member layout.Member) bool {
	field, ok := at.layout.Field(member.Key)

	return ok && slices.Equal(field.Index, append(slices.Clone(at.index), member.Field.Index...))
}

func (at scope) ownsRest(member layout.Member) bool {
	return at.layout.Rest != nil && slices.Equal(at.layout.Rest.Index, append(slices.Clone(at.index), member.Field.Index...))
}

func recurseMember(field reflect.Value, member layout.Member, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	// An unexported embedded struct is left to the promoted SetDefaults of the
	// struct that holds it.
	if !member.Field.IsExported() {
		return nil
	}

	fieldSecret := inheritedSecret || member.Secret

	switch {
	case member.Promoted:
		return recurseStruct(field, at.promoted(member), v, fieldSecret, activeTypes, activePtrs)
	case member.Rest:
		if at.ownsRest(member) {
			recordRestDefaults(field, at.key, v, fieldSecret)
		}

		return nil
	case !at.owns(member):
		return nil
	case layout.IsNestedStructType(field.Type()):
		return recurseStruct(field, at.nested(member.Key, field.Type()), v, fieldSecret, activeTypes, activePtrs)
	}

	recordDefaultLeaf(field, at.key.AppendToken(member.Key), v, fieldSecret)

	return nil
}

func recurseStruct(field reflect.Value, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	if field.Kind() == reflect.Pointer {
		return handlePointerStructField(field, at, v, inheritedSecret, activeTypes, activePtrs)
	}

	return handleStructField(field, at, v, inheritedSecret, activeTypes, activePtrs)
}

func recurseDefaults(val reflect.Value, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	typ := val.Type()
	if slices.Contains(activeTypes, typ) {
		return nil
	}

	activeTypes = append(activeTypes, typ)
	defer func() { activeTypes = activeTypes[:len(activeTypes)-1] }()

	for _, member := range layout.MembersOf(typ) {
		if err := recurseMember(val.FieldByIndex(member.Field.Index), member, at, v, inheritedSecret, activeTypes, activePtrs); err != nil {
			return err
		}
	}

	return nil
}

func recordRestDefaults(field reflect.Value, key jsontext.Pointer, v *visitor, isSecret bool) {
	if v.report == nil || field.Len() == 0 {
		return
	}

	entries := field.MapKeys()
	slices.SortFunc(entries, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })

	for _, entry := range entries {
		recordDefaultLeaf(field.MapIndex(entry), key.AppendToken(entry.String()), v, isSecret)
	}
}

func scopeError(key jsontext.Pointer, err error) error {
	if key == "" {
		return err
	}

	return fmt.Errorf("%s: %w", keypath.Format(key), err)
}

func recordDefaultLeaf(field reflect.Value, fullKey jsontext.Pointer, v *visitor, isSecret bool) {
	if v.report == nil {
		return
	}

	if isSecret || ContainsSecrets(field) {
		v.report(fullKey, "[REDACTED]")
		return
	}

	v.report(fullKey, rawValueOf(field))
}

func rawValueOf(field reflect.Value) string {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return ""
		}

		return formatLeafValue(field.Elem())
	}

	return formatLeafValue(field)
}

// Types with value methods use fmt so Stringer and Formatter still apply.
func formatLeafValue(field reflect.Value) string {
	if field.Type().NumMethod() != 0 {
		return fmt.Sprintf("%v", field.Interface())
	}

	//nolint:exhaustive // kinds not listed here are rendered by the default branch, which is exact
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(field.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		// NB: Bits, not 64. Float widens to float64, and formatting a float32
		// with bitSize 64 renders 3.3 as 3.299999952316284.
		return strconv.FormatFloat(field.Float(), 'g', -1, field.Type().Bits())
	default:
		return fmt.Sprintf("%v", field.Interface())
	}
}

func handleStructField(field reflect.Value, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	if field.CanAddr() && v.apply {
		ensureEmbeddedPointers(field)

		if d, ok := reflect.TypeAssert[Defaulter](field.Addr()); ok && v.apply && !isPromotedSetDefaults(field.Addr().Interface()) {
			if err := callSetDefaults(d); err != nil {
				return scopeError(at.key, err)
			}
		}
	}

	return recurseDefaults(field, at, v, inheritedSecret, activeTypes, activePtrs)
}

func handlePointerStructField(field reflect.Value, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	elemType := field.Type().Elem()
	if slices.Contains(activeTypes, elemType) {
		return nil
	}

	if field.IsNil() {
		if err := initNilStructPointer(field, at, v, inheritedSecret, activeTypes, activePtrs); err != nil {
			return err
		}

		if field.IsNil() && v.report != nil {
			return recurseDefaults(reflect.New(elemType).Elem(), at, &visitor{report: v.report, apply: false}, inheritedSecret, activeTypes, activePtrs)
		}

		return nil
	}

	ptr := field.Pointer()
	if slices.Contains(activePtrs, ptr) {
		return nil
	}

	activePtrs = append(activePtrs, ptr)
	defer func() { activePtrs = activePtrs[:len(activePtrs)-1] }()

	if v.apply {
		ensureEmbeddedPointers(field.Elem())
	}

	if d, ok := reflect.TypeAssert[Defaulter](field); ok && v.apply && !isPromotedSetDefaults(field.Interface()) {
		if err := callSetDefaults(d); err != nil {
			return scopeError(at.key, err)
		}
	}

	return recurseDefaults(field.Elem(), at, v, inheritedSecret, activeTypes, activePtrs)
}

func initNilStructPointer(field reflect.Value, at scope, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	elemType := field.Type().Elem()
	ptrType := field.Type()

	if !v.apply || slices.Contains(activeTypes, elemType) {
		return nil
	}

	if !ptrType.Implements(reflect.TypeFor[Defaulter]()) &&
		!elemType.Implements(reflect.TypeFor[Defaulter]()) {
		return nil
	}

	newVal := reflect.New(elemType)
	ensureEmbeddedPointers(newVal.Elem())

	if d, ok := reflect.TypeAssert[Defaulter](newVal); ok && !isPromotedSetDefaults(newVal.Interface()) {
		if err := callSetDefaults(d); err != nil {
			return scopeError(at.key, err)
		}
	}

	field.Set(newVal)

	return recurseDefaults(field.Elem(), at, v, inheritedSecret, activeTypes, activePtrs)
}

// A promoted SetDefaults must not run twice: the field walk calls the embedded
// struct itself. Unexported embedded fields are skipped, so their promoted
// method runs on target unless the embedded pointer is nil.
func isPromotedSetDefaults(target any) bool {
	val := reflect.ValueOf(target)
	t := val.Type()

	elem := t
	if elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}

	if !declaresPromotedSetDefaults(t, elem) {
		return false
	}

	if elem.Kind() == reflect.Struct {
		if callable, ok := unexportedSetDefaults(reflect.Indirect(val)); ok {
			return !callable
		}
	}

	return true
}

func declaresPromotedSetDefaults(t, elem reflect.Type) bool {
	if elem.Kind() == reflect.Struct && elem.Name() == "" {
		return true
	}

	m, ok := t.MethodByName("SetDefaults")
	if !ok {
		return false
	}

	fn := runtime.FuncForPC(m.Func.Pointer())
	if fn == nil {
		return false
	}

	file, _ := fn.FileLine(fn.Entry())

	return strings.Contains(file, "<autogenerated>")
}

// The results report whether the method has a non-nil receiver, then whether
// an unexported embedded field declaring SetDefaults exists.
func unexportedSetDefaults(val reflect.Value) (bool, bool) {
	defaulterType := reflect.TypeFor[Defaulter]()

	for f := range val.Type().Fields() {
		if !f.Anonymous || f.IsExported() {
			continue
		}

		if f.Type.Implements(defaulterType) || reflect.PointerTo(f.Type).Implements(defaulterType) {
			field := val.FieldByIndex(f.Index)

			return field.Kind() != reflect.Pointer || !field.IsNil(), true
		}
	}

	return false, false
}
