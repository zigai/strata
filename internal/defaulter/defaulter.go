package defaulter

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	maxASCII       byte = 0x80
	snakeBufferPad int  = 4
)

var (
	ErrInvalidTarget = errors.New("target must be a non-nil pointer to a struct")

	ErrSetDefaultsPanicked = errors.New("set defaults panicked")

	candidateTags   = [...]string{"strata", "toml", "yaml", "json"}
	secretTags      = [...]string{"strata", "env"}
	secretDirective = "secret"
)

type Defaulter interface {
	SetDefaults()
}

type visitor struct {
	report func(key, rawVal string)
	apply  bool
}

func Apply(target any, onDefault func(key, rawVal string)) error {
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

	return recurseDefaults(elem, "", &visitor{report: onDefault, apply: true}, false, nil, nil)
}

func Report(target any, onDefault func(key, rawVal string)) error {
	if target == nil {
		return ErrInvalidTarget
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() || val.Elem().Kind() != reflect.Struct {
		return ErrInvalidTarget
	}

	return recurseDefaults(val.Elem(), "", &visitor{report: onDefault, apply: false}, false, nil, nil)
}

func IsNestedStruct(v reflect.Value) bool {
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return false
	}

	return IsNestedStructType(v.Type())
}

func IsNestedStructType(typ reflect.Type) bool {
	if typ == nil {
		return false
	}

	for typ.Kind() == reflect.Pointer {
		if typ.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
			return false
		}

		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return false
	}

	// Struct types that carry their own text decoding are leaves, as is time.Time.
	if typ.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return false
	}

	if reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return false
	}

	if typ == reflect.TypeFor[time.Time]() {
		return false
	}

	return true
}

func FieldKey(f reflect.StructField) string {
	for _, tagName := range candidateTags {
		tag := f.Tag.Get(tagName)
		if tag != "" {
			name, _, _ := strings.Cut(tag, ",")

			name = strings.TrimSpace(name)
			if name != "" {
				return name
			}
		}
	}

	return ToSnakeCase(f.Name)
}

func ToSnakeCase(s string) string {
	if s == "" {
		return ""
	}

	hasUpper, isASCII := scanIdentifier(s)
	if isASCII && !hasUpper {
		return s
	}

	if isASCII {
		return toSnakeCaseASCII(s)
	}

	return toSnakeCaseUnicode(s)
}

func IsSecret(f reflect.StructField) bool {
	typ := f.Type
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.PkgPath() == "github.com/zigai/strata" && typ.Name() == "Secret" {
		return true
	}

	for _, tagName := range secretTags {
		tag := f.Tag.Get(tagName)

		if !strings.Contains(tag, secretDirective) {
			continue
		}

		for opt := range strings.SplitSeq(tag, ",") {
			if strings.TrimSpace(opt) == secretDirective {
				return true
			}
		}
	}

	return false
}

func shouldInsertUnderscore(s string, i, n int) bool {
	if i <= 0 {
		return false
	}

	prev := s[i-1]
	if isLower(prev) || isDigit(prev) {
		return true
	}

	return isUpper(prev) && i+1 < n && isLower(s[i+1])
}

func isLower(c byte) bool {
	return c >= 'a' && c <= 'z'
}

func isUpper(c byte) bool {
	return c >= 'A' && c <= 'Z'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func scanIdentifier(s string) (bool, bool) {
	hasUpper := false

	for i := range len(s) {
		c := s[i]
		if c >= maxASCII {
			return false, false
		}

		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		}
	}

	return hasUpper, true
}

func toSnakeCaseASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s) + snakeBufferPad)
	n := len(s)

	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if shouldInsertUnderscore(s, i, n) {
				b.WriteByte('_')
			}

			b.WriteByte(c + ('a' - 'A'))
		} else {
			b.WriteByte(c)
		}
	}

	return b.String()
}

func toSnakeCaseUnicode(s string) string {
	var b strings.Builder

	runes := []rune(s)
	n := len(runes)

	for i := range runes {
		r := runes[i]
		if unicode.IsUpper(r) {
			switch {
			case i > 0 && unicode.IsLower(runes[i-1]):
				b.WriteRune('_')
			case i > 0 && unicode.IsDigit(runes[i-1]):
				b.WriteRune('_')
			case i > 0 && i+1 < n && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]):
				b.WriteRune('_')
			}

			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}

	return b.String()
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

func fullFieldKey(prefix, key string, anonymous bool) string {
	if anonymous {
		return prefix
	}

	if prefix != "" {
		return prefix + "." + key
	}

	return key
}

func recurseField(field reflect.Value, sf reflect.StructField, prefix string, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	if !sf.IsExported() {
		return nil
	}

	key := FieldKey(sf)
	if key == "-" {
		return nil
	}

	fieldSecret := inheritedSecret || IsSecret(sf)
	fullKey := fullFieldKey(prefix, key, sf.Anonymous)

	if IsNestedStruct(field) {
		return handleStructField(field, fullKey, v, fieldSecret, activeTypes, activePtrs)
	}

	if field.Kind() == reflect.Pointer && IsNestedStructType(field.Type().Elem()) {
		return handlePointerStructField(field, fullKey, v, fieldSecret, activeTypes, activePtrs)
	}

	recordDefaultLeaf(field, sf, fullKey, v, fieldSecret)

	return nil
}

func recurseDefaults(val reflect.Value, prefix string, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	typ := val.Type()
	if slices.Contains(activeTypes, typ) {
		return nil
	}

	activeTypes = append(activeTypes, typ)
	defer func() { activeTypes = activeTypes[:len(activeTypes)-1] }()

	for i := range val.NumField() {
		if err := recurseField(val.Field(i), typ.Field(i), prefix, v, inheritedSecret, activeTypes, activePtrs); err != nil {
			return err
		}
	}

	return nil
}

func recordDefaultLeaf(field reflect.Value, sf reflect.StructField, fullKey string, v *visitor, isSecret bool) {
	if v.report == nil {
		return
	}

	if isSecret || IsSecret(sf) {
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

func handleStructField(field reflect.Value, fullKey string, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	if field.CanAddr() && v.apply {
		ensureEmbeddedPointers(field)

		if d, ok := reflect.TypeAssert[Defaulter](field.Addr()); ok && v.apply && !isPromotedSetDefaults(field.Addr().Interface()) {
			if err := callSetDefaults(d); err != nil {
				return fmt.Errorf("%s: %w", fullKey, err)
			}
		}
	}

	return recurseDefaults(field, fullKey, v, inheritedSecret, activeTypes, activePtrs)
}

func handlePointerStructField(field reflect.Value, fullKey string, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	elemType := field.Type().Elem()
	if slices.Contains(activeTypes, elemType) {
		return nil
	}

	if field.IsNil() {
		if err := initNilStructPointer(field, fullKey, v, inheritedSecret, activeTypes, activePtrs); err != nil {
			return err
		}

		if field.IsNil() && v.report != nil {
			return recurseDefaults(reflect.New(elemType).Elem(), fullKey, &visitor{report: v.report, apply: false}, inheritedSecret, activeTypes, activePtrs)
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
			return fmt.Errorf("%s: %w", fullKey, err)
		}
	}

	return recurseDefaults(field.Elem(), fullKey, v, inheritedSecret, activeTypes, activePtrs)
}

func initNilStructPointer(field reflect.Value, fullKey string, v *visitor, inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
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
			return fmt.Errorf("%s: %w", fullKey, err)
		}
	}

	field.Set(newVal)

	return recurseDefaults(field.Elem(), fullKey, v, inheritedSecret, activeTypes, activePtrs)
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
