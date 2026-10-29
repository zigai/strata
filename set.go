package strata

import (
	"encoding"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zigai/strata/internal/atomicfile"
	"github.com/zigai/strata/internal/edit"
	"github.com/zigai/strata/internal/keypath"
	"github.com/zigai/strata/internal/layout"
	"github.com/zigai/strata/internal/plan"
	"github.com/zigai/strata/internal/stream"
)

var (
	errInvalidConfigType = errors.New("configuration type is not a struct")
	errInvalidEditValue  = errors.New("invalid configuration value")
	errNotInteger        = errors.New("is not an integer")
	errNotUnsigned       = errors.New("is not an unsigned integer")
	errNotBoolean        = errors.New("is not a boolean")
	errNotNumber         = errors.New("is not a number")
	errNotDuration       = errors.New("is not a duration")
	errOutOfRange        = errors.New("is out of range for")
)

type unknownSetKeyError struct {
	path    string
	unknown UnknownKey
}

type fileEdit struct {
	key    jsontext.Pointer
	value  any
	secret bool
}

// SetBytes updates one key in raw configuration bytes and returns the result.
// key is dotted: each dot separates two mapping keys, each spelled exactly.
// Inside a key, \. is a literal dot and \\ a literal backslash, so
// `labels.a\.b` names the key "a.b" under labels, "labels.a/b" names "a/b",
// and "labels." names the empty key. Any other backslash returns
// [ErrInvalidKey], and the empty string returns [ErrEmptyKey].
//
// Comments, keys, and values outside the replaced key survive, including YAML
// anchors and aliases and TOML multi-line values. Formatting changes by format:
//
//   - TOML keeps comments, blank lines, indentation, and key order. Spacing
//     before a trailing comment becomes one space; an appended key follows a
//     blank line.
//   - YAML keeps comments, key order, anchors, aliases, block scalars, merge
//     keys, flow style, quoting, and indentation. It drops blank lines. A block
//     scalar the YAML encoder cannot reproduce exactly, such as one starting
//     with a blank line, is rewritten as a literal block or a quoted string
//     with the same value, and an empty null in a flow collection is written
//     as null.
//   - JSON keeps indentation and untouched number text, sorts object keys, and
//     adds a trailing newline.
//
// Multi-document YAML and JSON with duplicate keys are rejected rather than
// rewritten, because re-encoding would discard content. A key whose path runs
// through a value that is not a mapping, such as a list or an array of TOML
// tables, returns [ErrPathNotMapping]; a null on the path becomes a mapping.
//
// format accepts a case-insensitive extension with or without a leading dot.
// Unsupported formats wrap [ErrUnsupportedFormat].
//
// String values are inferred as numbers or booleans when possible: "9090"
// becomes an integer and "1e5" a number. Only the full words "true" and
// "false" become booleans; a single letter such as "t" stays a string.
func SetBytes(format string, data []byte, key string, value any) ([]byte, error) {
	parsed, err := keypath.Parse(key)
	if err != nil {
		return nil, fmt.Errorf("set configuration: %w", err)
	}

	return setBytes(format, data, parsed, inferScalar(value))
}

// Set updates one key in the file at targetPath after checking that T declares
// the key and the value can decode into its field type. Unknown keys include a
// close-match suggestion. A key may address an entry of a string-keyed map or
// an inline map. Set does not run ValidateWith.
//
// Format-specific edits preserve untouched content as described by [SetBytes].
// A symlink is followed and the file it points to is edited. The file is staged
// beside the target and replaced atomically, so an interruption leaves the old
// or new file, never a partial write. Its original permission bits, such as
// 0600 or 0640, are preserved.
//
// Writers through [Set], [Save], and [Init], in this process or another, take
// an exclusive lock on a ".<name>.lock" file beside the target before reading
// it, so concurrent edits never overwrite each other. The lock file stays in
// place. On platforms without file locking the write fails with an error
// wrapping [errors.ErrUnsupported].
func Set[T any](targetPath string, key string, value any) error {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("%w: %s", errInvalidConfigType, typ)
	}

	if err := layout.Validate(typ); err != nil {
		return fmt.Errorf("set configuration: %w", err)
	}

	keys := keyTreeFor(typ)

	parsed, err := keypath.Parse(key)
	if err != nil {
		return fmt.Errorf("set configuration: %w", err)
	}

	if !keys.known(parsed) {
		return &unknownSetKeyError{path: targetPath, unknown: UnknownKey{
			Origin:     Origin{Key: keypath.Format(parsed), Source: "", Path: "", Line: 0, RawValue: ""},
			Suggestion: keypath.Format(keys.suggest(parsed)),
		}}
	}

	resolvedPath, err := atomicfile.Resolve(targetPath)
	if err != nil {
		return fmt.Errorf("set configuration: %w", err)
	}

	secret := keys.secret(parsed)

	fieldType, err := plan.FieldType(typ, parsed)
	if err != nil {
		return fmt.Errorf("%s: %w", resolvedPath, err)
	}

	typed, err := typedEditValue(fieldType, value)
	if err != nil {
		if secret {
			err = &secretValueError{cause: err}
		}

		return fmt.Errorf("%s: %s: %w", resolvedPath, keypath.Format(parsed), err)
	}

	return editFile(resolvedPath, fileEdit{key: parsed, value: typed, secret: secret})
}

func (e *unknownSetKeyError) Error() string { return e.path + ": " + e.unknown.String() }

func (e *unknownSetKeyError) Unwrap() error { return ErrUnknownKey }

func setBytes(format string, data []byte, key jsontext.Pointer, val any) ([]byte, error) {
	ext := normalizeExt(format)
	tokens := slices.Collect(key.Tokens())

	switch ext {
	case ".yaml", ".yml":
		updated, err := edit.UpdateYAML(data, tokens, val)
		if err != nil {
			return nil, fmt.Errorf("update yaml: %w", err)
		}

		return updated, nil
	case ".toml":
		updated, err := edit.UpdateTOML(data, tokens, val)
		if err != nil {
			return nil, fmt.Errorf("update toml: %w", err)
		}

		return updated, nil
	case ".json":
		updated, err := edit.UpdateJSON(data, tokens, val)
		if err != nil {
			return nil, fmt.Errorf("update json: %w", err)
		}

		return updated, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}
}

func editFile(targetPath string, change fileEdit) (err error) {
	if _, statErr := os.Stat(targetPath); statErr != nil {
		return fmt.Errorf("open target configuration %s: %w", targetPath, statErr)
	}

	lock, err := atomicfile.Acquire(targetPath)
	if err != nil {
		return fmt.Errorf("edit configuration %s: %w", targetPath, err)
	}

	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("edit configuration %s: %w", targetPath, releaseErr))
		}
	}()

	data, mode, err := readTarget(targetPath)
	if err != nil {
		return err
	}

	data, err = setBytes(filepath.Ext(targetPath), data, change.key, change.value)
	if err != nil {
		if change.secret {
			return &secretValueError{cause: err}
		}

		return fmt.Errorf("update %s in %s: %w", change.key, targetPath, err)
	}

	if err := atomicfile.Write(targetPath, data, mode); err != nil {
		return fmt.Errorf("write updated configuration to %s: %w", targetPath, err)
	}

	return nil
}

func readTarget(targetPath string) ([]byte, os.FileMode, error) {
	f, err := os.Open(targetPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open target configuration %s: %w", targetPath, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("stat target configuration %s: %w", targetPath, err)
	}

	data, readErr := stream.ReadBounded(f, stream.DefaultMaxFileSize)
	if closeErr := f.Close(); closeErr != nil && readErr == nil {
		return nil, 0, fmt.Errorf("close target configuration %s: %w", targetPath, closeErr)
	}

	if readErr != nil {
		return nil, 0, fmt.Errorf("read target configuration %s: %w", targetPath, readErr)
	}

	return data, info.Mode().Perm(), nil
}

func typedEditValue(typ reflect.Type, value any) (any, error) {
	if typ.Kind() == reflect.Interface {
		return inferScalar(value), nil
	}

	if raw, ok := value.(string); ok {
		return typedEditString(typ, raw)
	}

	if typ.Kind() == reflect.String {
		return typedEditStringValue(typ, value)
	}

	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return nil, fmt.Errorf("%w: cannot decode %T as %s", errInvalidEditValue, value, typ)
	}

	if typ == reflect.TypeFor[time.Duration]() || reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) || isScalarKind(typ.Kind()) {
		return typedEditString(typ, fmt.Sprint(value))
	}

	if v.Type().AssignableTo(typ) {
		return value, nil
	}

	if v.Type().ConvertibleTo(typ) && (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map) {
		return v.Convert(typ).Interface(), nil
	}

	return nil, fmt.Errorf("%w: cannot decode %T as %s", errInvalidEditValue, value, typ)
}

func typedEditStringValue(typ reflect.Type, value any) (any, error) {
	v := reflect.ValueOf(value)
	if v.IsValid() && v.Type().AssignableTo(typ) {
		return typedEditString(typ, v.String())
	}

	return nil, fmt.Errorf("%w: cannot decode %T as %s", errInvalidEditValue, value, typ)
}

func isScalarKind(kind reflect.Kind) bool {
	return kind == reflect.Bool || kind >= reflect.Int && kind <= reflect.Uint64 || kind == reflect.Float32 || kind == reflect.Float64
}

func typedEditString(typ reflect.Type, raw string) (any, error) {
	return parseTypedEditString(typ, raw, false)
}

func parseTypedEditString(typ reflect.Type, raw string, preserveType bool) (any, error) {
	if typ == reflect.TypeFor[time.Duration]() {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("%q %w", raw, errNotDuration)
		}

		if preserveType {
			return parsed, nil
		}

		return raw, nil
	}

	if reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		v := reflect.New(typ)

		unmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](v)
		if !ok {
			return nil, fmt.Errorf("%w: %s has no text decoder", errInvalidEditValue, typ)
		}

		if err := unmarshaler.UnmarshalText([]byte(raw)); err != nil {
			return nil, fmt.Errorf("decode text: %w", err)
		}

		if preserveType {
			return v.Elem().Interface(), nil
		}

		return raw, nil
	}

	if typ.Kind() == reflect.String {
		return raw, nil
	}

	if typ.Kind() == reflect.Slice {
		return parseTypedSlice(typ, raw)
	}

	return parseTypedScalar(typ, raw)
}

func parseTypedSlice(typ reflect.Type, raw string) (any, error) {
	if raw == "" {
		return reflect.MakeSlice(typ, 0, 0).Interface(), nil
	}

	preserveElements := false

	for _, marshaler := range []reflect.Type{reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[json.Marshaler](), reflect.TypeFor[yaml.Marshaler]()} {
		if typ.Implements(marshaler) || reflect.PointerTo(typ).Implements(marshaler) {
			preserveElements = true
			break
		}
	}

	parts := strings.Split(raw, ",")
	result := reflect.MakeSlice(typ, len(parts), len(parts))
	encoded := make([]any, len(parts))
	typedResult := preserveElements || typ.Elem() != reflect.TypeFor[time.Duration]() && !reflect.PointerTo(typ.Elem()).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())

	for index, part := range parts {
		value, err := parseTypedEditString(typ.Elem(), strings.TrimSpace(part), preserveElements)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", index+1, err)
		}

		encoded[index] = value

		parsed := reflect.ValueOf(value)
		if parsed.Type().ConvertibleTo(typ.Elem()) {
			result.Index(index).Set(parsed.Convert(typ.Elem()))
		} else {
			typedResult = false
		}
	}

	if typedResult {
		return result.Interface(), nil
	}

	return encoded, nil
}

func parseTypedScalar(typ reflect.Type, raw string) (any, error) {
	kind := typ.Kind()
	switch {
	case kind == reflect.Bool:
		return parseEditBool(raw)
	case kind >= reflect.Int && kind <= reflect.Int64:
		return parseEditInt(typ, raw)
	case kind >= reflect.Uint && kind <= reflect.Uint64:
		return parseEditUint(typ, raw)
	case kind == reflect.Float32 || kind == reflect.Float64:
		return parseEditFloat(typ, raw)
	default:
		return nil, fmt.Errorf("%w: cannot decode %q as %s", errInvalidEditValue, raw, typ)
	}
}

func parseEditBool(raw string) (any, error) {
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%q %w", raw, errNotBoolean)
	}

	return parsed, nil
}

func parseEditInt(typ reflect.Type, raw string) (any, error) {
	parsed, err := strconv.ParseInt(raw, 10, typ.Bits())
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q %w %s", raw, errOutOfRange, typ)
		}

		return nil, fmt.Errorf("%q %w", raw, errNotInteger)
	}

	return parsed, nil
}

func parseEditUint(typ reflect.Type, raw string) (any, error) {
	parsed, err := strconv.ParseUint(raw, 10, typ.Bits())
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q %w %s", raw, errOutOfRange, typ)
		}

		return nil, fmt.Errorf("%q %w", raw, errNotUnsigned)
	}

	return parsed, nil
}

func parseEditFloat(typ reflect.Type, raw string) (any, error) {
	parsed, err := strconv.ParseFloat(raw, typ.Bits())
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q %w %s", raw, errOutOfRange, typ)
		}

		return nil, fmt.Errorf("%q %w", raw, errNotNumber)
	}

	if typ.Kind() == reflect.Float32 {
		return float32(parsed), nil
	}

	return parsed, nil
}

func inferScalar(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}

	return inferScalarString(s)
}

func inferScalarString(s string) any {
	trimmed := strings.TrimSpace(s)
	if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return i
	}

	if u, err := strconv.ParseUint(trimmed, 10, 64); err == nil {
		return u
	}

	if strings.EqualFold(trimmed, "true") {
		return true
	}

	if strings.EqualFold(trimmed, "false") {
		return false
	}

	if strings.Contains(trimmed, ".") || strings.ContainsAny(trimmed, "eE") {
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return f
		}
	}

	return s
}
