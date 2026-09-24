package strata

import (
	"encoding"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/strata/internal/atomicfile"
	"github.com/zigai/strata/internal/edit"
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

// SetBytes updates one key in raw configuration bytes and returns the result.
// Comments, keys, and values outside the replaced key survive, including YAML
// anchors and aliases and TOML multi-line values. Formatting changes by format:
//
//   - TOML keeps comments, blank lines, indentation, and key order. Spacing
//     before a trailing comment becomes one space; an appended key follows a
//     blank line.
//   - YAML keeps comments, key order, anchors, aliases, block scalars, merge
//     keys, flow style, quoting, and indentation. It drops blank lines.
//   - JSON keeps indentation and untouched number text, sorts object keys, and
//     adds a trailing newline.
//
// Multi-document YAML and JSON with duplicate keys are rejected rather than
// rewritten, because re-encoding would discard content.
//
// format accepts a case-insensitive extension with or without a leading dot.
// Unsupported formats wrap [ErrUnsupportedFormat].
//
// String values are inferred as numbers or booleans when possible: "9090"
// becomes an integer and "1e5" a number. Only the full words "true" and
// "false" become booleans; a single letter such as "t" stays a string.
func SetBytes(format string, data []byte, dottedKey string, value any) ([]byte, error) {
	return setBytes(format, data, dottedKey, inferCLIValue(value))
}

// Set updates one key in the file at targetPath after checking that T declares
// the key and the value can decode into its field type. Unknown keys include a
// close-match suggestion. Dotted paths may address entries under string-keyed
// maps. Set does not run ValidateWith.
//
// Format-specific edits preserve untouched content as described by [SetBytes].
// The file is staged beside the target and replaced atomically, so an
// interruption leaves the old or new file, never a partial write. Its original
// permission bits, such as 0600 or 0640, are preserved.
func Set[T any](targetPath string, dottedKey string, value any) error {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("%w: %s", errInvalidConfigType, typ)
	}

	if !keyTreeFor(typ).known(dottedKey) {
		var unknown UnknownKey

		unknown.Key = dottedKey
		unknown.Suggestion = keyTreeFor(typ).suggest(dottedKey)

		return &unknownSetKeyError{path: targetPath, unknown: unknown}
	}

	fieldType, err := plan.FieldType(typ, dottedKey)
	if err != nil {
		return fmt.Errorf("%s: %w", targetPath, err)
	}

	typed, err := typedEditValue(fieldType, value)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", targetPath, dottedKey, err)
	}

	return setFile(targetPath, dottedKey, typed)
}

func (e *unknownSetKeyError) Error() string { return e.path + ": " + e.unknown.String() }

func (e *unknownSetKeyError) Unwrap() error { return ErrUnknownKey }

func setBytes(format string, data []byte, dottedKey string, val any) ([]byte, error) {
	ext := normalizeExt(format)

	switch ext {
	case ".yaml", ".yml":
		updated, err := edit.UpdateYAML(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update yaml: %w", err)
		}

		return updated, nil
	case ".toml":
		updated, err := edit.UpdateTOML(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update toml: %w", err)
		}

		return updated, nil
	case ".json":
		updated, err := edit.UpdateJSON(data, dottedKey, val)
		if err != nil {
			return nil, fmt.Errorf("update json: %w", err)
		}

		return updated, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}
}

func setFile(targetPath string, dottedKey string, value any) error {
	resolvedPath, symErr := filepath.EvalSymlinks(targetPath)
	if symErr == nil {
		targetPath = resolvedPath
	}

	f, oErr := os.Open(targetPath)
	if oErr != nil {
		return fmt.Errorf("open target configuration %s: %w", targetPath, oErr)
	}

	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return fmt.Errorf("stat target configuration %s: %w", targetPath, statErr)
	}

	data, rErr := stream.ReadBounded(f, stream.DefaultMaxFileSize)
	if cErr := f.Close(); cErr != nil && rErr == nil {
		return fmt.Errorf("close target configuration %s: %w", targetPath, cErr)
	}

	if rErr != nil {
		return fmt.Errorf("read target configuration %s: %w", targetPath, rErr)
	}

	ext := filepath.Ext(targetPath)

	updated, sErr := setBytes(ext, data, dottedKey, value)
	if sErr != nil {
		return fmt.Errorf("update %s in %s: %w", dottedKey, targetPath, sErr)
	}

	if wErr := atomicfile.WriteFileAtomic(targetPath, updated, info.Mode().Perm()); wErr != nil {
		return fmt.Errorf("write updated configuration to %s: %w", targetPath, wErr)
	}

	return nil
}

func typedEditValue(typ reflect.Type, value any) (any, error) {
	if typ.Kind() == reflect.Interface {
		return inferCLIValue(value), nil
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

	if typ == reflect.TypeFor[time.Duration]() || reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) || scalarKind(typ.Kind()) {
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

func scalarKind(kind reflect.Kind) bool {
	return kind == reflect.Bool || kind >= reflect.Int && kind <= reflect.Uint64 || kind == reflect.Float32 || kind == reflect.Float64
}

func typedEditString(typ reflect.Type, raw string) (any, error) {
	if typ == reflect.TypeFor[time.Duration]() {
		if _, err := time.ParseDuration(raw); err != nil {
			return nil, fmt.Errorf("%q %w", raw, errNotDuration)
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

	parts := strings.Split(raw, ",")
	result := reflect.MakeSlice(typ, len(parts), len(parts))

	for i, part := range parts {
		value, err := typedEditString(typ.Elem(), strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}

		parsed := reflect.ValueOf(value)
		if !parsed.Type().ConvertibleTo(typ.Elem()) {
			return nil, fmt.Errorf("%w: item %d cannot decode as %s", errInvalidEditValue, i+1, typ.Elem())
		}

		result.Index(i).Set(parsed.Convert(typ.Elem()))
	}

	return result.Interface(), nil
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
	_, err := strconv.ParseFloat(raw, typ.Bits())
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q %w %s", raw, errOutOfRange, typ)
		}

		return nil, fmt.Errorf("%q %w", raw, errNotNumber)
	}

	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%q %w", raw, errNotNumber)
	}

	return parsed, nil
}

func inferCLIValue(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}

	return inferCLIString(s)
}

func inferCLIString(s string) any {
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
