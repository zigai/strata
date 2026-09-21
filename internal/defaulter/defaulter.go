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
	// ErrTargetNotPointer is returned by Apply when target is not a non-nil
	// pointer to a struct.
	ErrTargetNotPointer = errors.New("target must be a non-nil pointer to a struct")

	// ErrSetDefaultsPanicked is returned when a [Defaulter]'s SetDefaults panics.
	// The panicking value is included in the message.
	//
	// When the panic comes from a nested struct, the returned error is prefixed
	// with the dotted key of that struct.
	ErrSetDefaultsPanicked = errors.New("set defaults panicked")

	candidateTags   = [...]string{"strata", "toml", "yaml", "json"}
	secretTags      = [...]string{"strata", "env"}
	secretDirective = "secret"
)

// Defaulter is an optional interface that a struct can implement to declare its
// defaults in Go code, with compile-time type checking.
//
// [Apply] calls SetDefaults on a target that implements the interface and on
// nested fields whose type declares defaults, before any leaf value is recorded.
// A panic from SetDefaults is reported as [ErrSetDefaultsPanicked].
type Defaulter interface {
	SetDefaults()
}

// Apply applies the defaults declared by target's type and reports every leaf
// field that ends up non-zero to onDefault. Unexported fields and fields that a
// "-" tag excludes are not walked.
//
// target MUST be a non-nil pointer to a struct. [ErrTargetNotPointer] is
// returned otherwise.
//
// onDefault receives the dotted key of the leaf and its rendered value. A leaf
// whose field is tagged secret is reported as a redacted placeholder. A nil
// onDefault disables reporting; defaults are still applied.
//
// A panic from a nested SetDefaults aborts the walk. The error returned for it
// wraps [ErrSetDefaultsPanicked] and is prefixed with the dotted key of the
// struct that panicked.
func Apply(target any, onDefault func(key, rawVal string)) error {
	if target == nil {
		return ErrTargetNotPointer
	}

	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return ErrTargetNotPointer
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return ErrTargetNotPointer
	}

	ensureEmbeddedPointers(elem)

	if d, ok := target.(Defaulter); ok && !isPromotedSetDefaults(target) {
		if err := callSetDefaults(d); err != nil {
			return err
		}
	}

	return recurseDefaults(elem, "", onDefault, false, nil, nil)
}

// IsNestedStruct reports whether v is a nested composite struct that the walk
// recurses into rather than treating as a leaf.
//
// Struct types that carry their own text decoding are leaves: a value that
// implements [encoding.TextUnmarshaler], an addressable value whose pointer
// does, and [time.Time].
func IsNestedStruct(v reflect.Value) bool {
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return false
	}

	return IsNestedStructType(v.Type())
}

// IsNestedStructType reports whether typ is a nested composite struct rather
// than a leaf type (such as [time.Time] or a type implementing
// [encoding.TextUnmarshaler]).
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

// FieldKey resolves the configuration key of a struct field.
//
// The first tag among strata, toml, yaml, and json that names the field wins,
// with surrounding whitespace trimmed and the option list after the first comma
// discarded. When no tag names the field, the snake_case form of the Go field
// name is used.
//
// A name of "-" is returned as-is; the walkers that call FieldKey skip such
// fields. Note that a tag with an empty name, as in `strata:",secret"`, does not
// name the field, and the remaining tags are consulted before the fallback.
//
// FieldKey performs no nesting. The returned string is one path segment, and
// callers that need a nested key join segments with ".".
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

// ToSnakeCase converts a CamelCase or PascalCase identifier to snake_case.
//
// An identifier with no uppercase letters is returned unchanged. All-ASCII
// identifiers take a byte-wise path, and any identifier containing a non-ASCII
// byte is walked as runes.
//
// A run of uppercase letters is treated as an acronym: "HTTPServer" becomes
// "http_server".
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

// ShouldInsertUnderscore reports whether an underscore is inserted before index
// i of identifier s.
//
// It is called for each uppercase byte of s, with n equal to len(s), and returns
// false for i == 0. Otherwise the result is true when s[i-1] is a lowercase
// letter, or when s[i-1] is uppercase and a lowercase byte follows at i+1. The
// second case separates an acronym from the word after it: "HTTPServer" becomes
// "http_server".
func ShouldInsertUnderscore(s string, i, n int) bool {
	if i <= 0 {
		return false
	}

	prev := s[i-1]
	if isLower(prev) || isDigit(prev) {
		return true
	}

	return isUpper(prev) && i+1 < n && isLower(s[i+1])
}

// IsSecret reports whether a struct field is tagged as secret.
//
// A field is secret when its strata or env tag carries an option equal to
// "secret", either alone or after a comma, as in `strata:"token,secret"`.
// Whitespace around an option is ignored. A tag name that merely contains the
// word, as in `strata:"secret_key"`, does not mark the field.
//
// Callers that record or print a field value MUST redact it when this reports
// true. Both CLI bridges consult IsSecret when they derive flags, and the
// environment tier consults it before reporting a bound value.
func IsSecret(f reflect.StructField) bool {
	for _, tagName := range secretTags {
		tag := f.Tag.Get(tagName)

		// NB: almost every field carries no secret option, so a substring
		// reject avoids splitting the tag for every leaf of every load. The
		// substring can only over-accept, and the option comparison below
		// rejects those cases.
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
			if ShouldInsertUnderscore(s, i, n) {
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

// callSetDefaults invokes d.SetDefaults and converts a panic into an error.
//
// The error returned for a panic wraps [ErrSetDefaultsPanicked]. A panic left to
// escape would terminate the caller's process, and recovering without an error
// would report a configuration whose defaults were never applied,
// indistinguishable from one that declares none.
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

func recurseField(field reflect.Value, sf reflect.StructField, prefix string, onDefault func(key, rawVal string), inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
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
		return handleStructField(field, fullKey, onDefault, fieldSecret, activeTypes, activePtrs)
	}

	if field.Kind() == reflect.Pointer && IsNestedStructType(field.Type().Elem()) {
		return handlePointerStructField(field, fullKey, onDefault, fieldSecret, activeTypes, activePtrs)
	}

	recordDefaultLeaf(field, sf, fullKey, onDefault, fieldSecret)

	return nil
}

func recurseDefaults(val reflect.Value, prefix string, onDefault func(key, rawVal string), inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	typ := val.Type()
	if slices.Contains(activeTypes, typ) {
		return nil
	}

	activeTypes = append(activeTypes, typ)
	defer func() { activeTypes = activeTypes[:len(activeTypes)-1] }()

	for i := range val.NumField() {
		if err := recurseField(val.Field(i), typ.Field(i), prefix, onDefault, inheritedSecret, activeTypes, activePtrs); err != nil {
			return err
		}
	}

	return nil
}

func recordDefaultLeaf(field reflect.Value, sf reflect.StructField, fullKey string, onDefault func(key, rawVal string), isSecret bool) {
	if onDefault == nil || field.IsZero() {
		return
	}

	if isSecret || IsSecret(sf) {
		onDefault(fullKey, "[REDACTED]")
		return
	}

	onDefault(fullKey, rawValueOf(field))
}

// rawValueOf renders a leaf field for provenance.
//
// A pointer reports the value it points at, not an address. A nil pointer cannot
// reach here: the caller skips zero fields before rendering.
func rawValueOf(field reflect.Value) string {
	if field.Kind() == reflect.Pointer {
		return formatLeafValue(field.Elem())
	}

	return formatLeafValue(field)
}

// formatLeafValue renders a leaf value exactly as fmt's %v verb would, without
// boxing it into an interface on the paths that do not need the fmt printer.
//
// A type with no methods cannot intercept %v. Its rendering is fully determined
// by its kind and strconv is exact. A type with any method in its value method
// set goes through %v instead, because Stringer, error, and Formatter must keep
// working: a [time.Duration] default records "10s", not "10000000000".
//
// The method set that matters is the value type's. A type whose String method is
// on the pointer receiver has no String in its value method set, so %v does not
// call it either and the strconv path stays exact.
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

func handleStructField(field reflect.Value, fullKey string, onDefault func(key, rawVal string), inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	if field.CanAddr() {
		ensureEmbeddedPointers(field)

		if d, ok := reflect.TypeAssert[Defaulter](field.Addr()); ok && !isPromotedSetDefaults(field.Addr().Interface()) {
			if err := callSetDefaults(d); err != nil {
				return fmt.Errorf("%s: %w", fullKey, err)
			}
		}
	}

	return recurseDefaults(field, fullKey, onDefault, inheritedSecret, activeTypes, activePtrs)
}

func handlePointerStructField(field reflect.Value, fullKey string, onDefault func(key, rawVal string), inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	elemType := field.Type().Elem()
	if slices.Contains(activeTypes, elemType) {
		return nil
	}

	if field.IsNil() {
		return initNilStructPointer(field, fullKey, onDefault, inheritedSecret, activeTypes, activePtrs)
	}

	ptr := field.Pointer()
	if slices.Contains(activePtrs, ptr) {
		return nil
	}

	activePtrs = append(activePtrs, ptr)
	defer func() { activePtrs = activePtrs[:len(activePtrs)-1] }()

	ensureEmbeddedPointers(field.Elem())

	if d, ok := reflect.TypeAssert[Defaulter](field); ok && !isPromotedSetDefaults(field.Interface()) {
		if err := callSetDefaults(d); err != nil {
			return fmt.Errorf("%s: %w", fullKey, err)
		}
	}

	return recurseDefaults(field.Elem(), fullKey, onDefault, inheritedSecret, activeTypes, activePtrs)
}

// initNilStructPointer allocates a nil struct pointer field when its type
// declares defaults, so the newly created value contributes its own defaults.
//
// The field is left nil when neither the pointer type nor its element type
// implements [Defaulter].
func initNilStructPointer(field reflect.Value, fullKey string, onDefault func(key, rawVal string), inheritedSecret bool, activeTypes []reflect.Type, activePtrs []uintptr) error {
	elemType := field.Type().Elem()
	ptrType := field.Type()

	if slices.Contains(activeTypes, elemType) {
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

	return recurseDefaults(field.Elem(), fullKey, onDefault, inheritedSecret, activeTypes, activePtrs)
}

func isPromotedSetDefaults(target any) bool {
	val := reflect.ValueOf(target)
	t := val.Type()

	elem := t
	if elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}

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
