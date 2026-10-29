package layout

import (
	"encoding"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	maxASCII       byte = 0x80
	snakeBufferPad int  = 4
)

var (
	candidateTags   = [...]string{"strata", "toml", "yaml", "json"}
	secretTags      = [...]string{"strata", "env"}
	secretDirective = "secret"
)

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
	if IsSecretType(f.Type) {
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

func IsSecretType(typ reflect.Type) bool {
	var visited []reflect.Type
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		if slices.Contains(visited, typ) {
			break
		}

		visited = append(visited, typ)
		typ = typ.Elem()
	}

	return typ.PkgPath() == "github.com/zigai/strata" && typ.Name() == "Secret"
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
