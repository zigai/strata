package plan

import (
	"fmt"
	"reflect"
	"strings"
)

const (
	// flagTagName opts a field into CLI flag handling. A field that carries no
	// "flag" tag is not a flag; there is no opt-out sentinel.
	flagTagName = "flag"

	// usageTagName is the struct tag that supplies help text for a generated
	// flag.
	usageTagName = "usage"
)

// tagSpec is the parsed form of a "flag" struct tag.
type tagSpec struct {
	present   bool
	name      string
	shorthand string
}

// parseFlagTag parses the "flag" struct tag. Lookup distinguishes an absent tag
// from an explicitly empty one: absence opts out, and an empty tag opts in with a
// derived name.
//
// A tag that cannot be parsed produces an error wrapping [ErrInvalidTag].
func parseFlagTag(field reflect.StructField) (tagSpec, error) {
	raw, present := field.Tag.Lookup(flagTagName)
	if !present {
		return tagSpec{present: false, name: "", shorthand: ""}, nil
	}

	name, shorthand, hasShorthand := strings.Cut(raw, ",")
	name = strings.TrimSpace(name)
	shorthand = strings.TrimSpace(shorthand)

	if name == "-" {
		return tagSpec{}, fmt.Errorf("%w: remove the tag instead of naming a flag %q", ErrInvalidTag, "-")
	}

	if name != "" && !isValidFlagName(name) {
		return tagSpec{}, fmt.Errorf("%w: flag name %q is not a valid long name", ErrInvalidTag, name)
	}

	if !hasShorthand || shorthand == "" {
		return tagSpec{present: true, name: name, shorthand: ""}, nil
	}

	if strings.Contains(shorthand, ",") {
		return tagSpec{}, fmt.Errorf("%w: shorthand %q must be one byte, not a list", ErrInvalidTag, shorthand)
	}

	if !isValidShorthand(shorthand) {
		return tagSpec{}, fmt.Errorf("%w: shorthand %q must be one ASCII letter or digit", ErrInvalidTag, shorthand)
	}

	return tagSpec{present: true, name: name, shorthand: shorthand}, nil
}

func isValidFlagName(name string) bool {
	for i := range len(name) {
		c := name[i]

		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return name != ""
}

func isValidShorthand(shorthand string) bool {
	if len(shorthand) != 1 {
		return false
	}

	c := shorthand[0]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return false
	}
}
