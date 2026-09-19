package plan

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/zigai/strata/internal/defaulter"
)

const (
	// maxTraversalDepth bounds the depth of one struct traversal. A type that
	// repeats on a single traversal path is detected separately, and a path that
	// reaches this bound is reported as ErrRecursiveType.
	maxTraversalDepth = 16

	// snakePad reserves room for the underscores a CamelCase identifier gains
	// when it is converted to snake case.
	snakePad = 4
)

var (
	// ErrNotStruct is returned when a root type is not a struct, and when an
	// index path does not lead through struct fields.
	ErrNotStruct = errors.New("cfg must be a non-nil pointer to a struct")

	// ErrInvalidTag is returned when a "flag" struct tag cannot be parsed or
	// contradicts the field it is attached to.
	ErrInvalidTag = errors.New("invalid flag tag")

	// ErrUnsupportedFieldType is returned when a tagged field has no flag
	// mapping, and when a value cannot pass through the mapping the field does
	// have.
	//
	// A slice whose element type is not assignable to the destination element type
	// is reported here while detached storage is seeded. The synchronization path
	// reports the same condition as a value that does not fit the destination
	// field.
	ErrUnsupportedFieldType = errors.New("tagged field has no flag mapping")

	// ErrRecursiveType is returned when a struct type repeats on a single
	// traversal path.
	ErrRecursiveType = errors.New("recursive type in flag schema")
)

var (
	// Registration is the policy that reports tagged leaves under tagged
	// containers only. RegisterFlags and SyncFlagsToStruct share it, and the two
	// operations always agree on the field set they govern.
	Registration = Policy{
		RecurseUntaggedStructs: false,
		IncludeUntaggedLeaves:  false,
	}

	// Apply is the permissive policy used by the bridge Apply functions. It
	// preserves the historical matching of untagged fields against hand-registered
	// flags derived from field names.
	Apply = Policy{
		RecurseUntaggedStructs: true,
		IncludeUntaggedLeaves:  true,
	}
)

// Policy selects which fields a traversal reports.
type Policy struct {
	// RecurseUntaggedStructs traverses named structs that carry no "flag" tag.
	// Registration leaves this false. Apply enables it, and pre-existing
	// structures continue to match flags derived from field names.
	RecurseUntaggedStructs bool

	// IncludeUntaggedLeaves reports leaf fields that carry no "flag" tag.
	// Registration leaves this false.
	IncludeUntaggedLeaves bool
}

// Target describes one leaf field that maps to, or can be matched against, a CLI
// flag.
type Target struct {
	// IndexPath locates the leaf field from the root struct value. Pointer
	// containers along the path are dereferenced when the value is resolved.
	IndexPath []int

	// Display is the field path as reported in error messages, such as
	// "Settings.Database.Port".
	Display string

	// ConfigKey is the dotted configuration key, for example "database.port". It
	// is derived through internal/defaulter, so it matches the key every other
	// tier records.
	ConfigKey string

	// Name and Shorthand describe the generated flag.
	Name      string
	Shorthand string

	// Usage is the help text taken from the "usage" tag.
	Usage string

	// Kind and LeafType describe the flag's value type. LeafType is never a
	// pointer; a pointer field's element type is recorded instead.
	Kind     Kind
	LeafType reflect.Type

	// Secret reports whether the field carries the secret directive, as read
	// through internal/defaulter.
	Secret bool

	// Tagged reports whether the field carried an explicit "flag" tag. Only
	// tagged fields are registered; both tagged and untagged fields are matched
	// by Apply for backwards compatibility.
	Tagged bool
}

// walker accumulates Target values while traversing a struct type.
type walker struct {
	policy  Policy
	active  []reflect.Type
	targets []Target
}

// Build reports one Target per leaf field reachable from root under policy.
//
// root MUST be a struct type. A field becomes a target when it carries a "flag"
// tag, or when policy reports untagged fields, and the targets are returned in
// field declaration order.
//
// A non-struct root is reported as ErrNotStruct, an unparsable tag as
// ErrInvalidTag, a tagged field with no flag mapping as ErrUnsupportedFieldType,
// and a type that repeats on one traversal path as ErrRecursiveType. The result
// is nil whenever an error is returned. An untagged field whose type has no flag
// mapping is skipped, and only a tagged field with no mapping is rejected.
func Build(root reflect.Type, policy Policy) ([]Target, error) {
	if root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s is not a struct", ErrNotStruct, root)
	}

	w := &walker{
		policy:  policy,
		active:  make([]reflect.Type, 0, maxTraversalDepth),
		targets: make([]Target, 0, root.NumField()),
	}

	if err := w.walkType(root, nil, "", "", ""); err != nil {
		return nil, err
	}

	return w.targets, nil
}

func (w *walker) walkType(typ reflect.Type, indexPath []int, flagPrefix, configPrefix, displayPrefix string) error {
	if slices.Contains(w.active, typ) {
		return fmt.Errorf("%w: %s repeats %s on one traversal path", ErrRecursiveType, displayPrefix, typ)
	}

	if len(w.active) >= maxTraversalDepth {
		return fmt.Errorf("%w: %s exceeds %d levels", ErrRecursiveType, displayPrefix, maxTraversalDepth)
	}

	w.active = append(w.active, typ)
	defer func() { w.active = w.active[:len(w.active)-1] }()

	for i := range typ.NumField() {
		field := typ.Field(i)

		// An unexported named field is invisible to callers and cannot be set
		// through reflection. An embedded unexported type differs: Go promotes
		// its exported fields, and reflection permits writing to them.
		if !field.IsExported() && !field.Anonymous {
			continue
		}

		if err := w.walkField(field, appendIndex(indexPath, i), flagPrefix, configPrefix, displayPrefix); err != nil {
			return err
		}
	}

	return nil
}

func (w *walker) walkField(field reflect.StructField, indexPath []int, flagPrefix, configPrefix, displayPrefix string) error {
	display := joinDisplay(displayPrefix, field.Name)

	spec, err := parseFlagTag(field)
	if err != nil {
		return fmt.Errorf("%s: %w", display, err)
	}

	if isContainerType(field.Type) {
		return w.walkContainer(field, spec, indexPath, flagPrefix, configPrefix, display)
	}

	return w.walkLeaf(field, spec, indexPath, flagPrefix, configPrefix, display)
}

func (w *walker) walkContainer(
	field reflect.StructField,
	spec tagSpec,
	indexPath []int,
	flagPrefix, configPrefix, display string,
) error {
	embedded := field.Anonymous
	if err := validateContainerSpec(embedded, spec, display); err != nil {
		return err
	}

	switch {
	case embedded:
		// An embedded struct is inlined, and Go's own field promotion applies.
	case spec.present:
		name := spec.name
		if name == "" {
			name = kebabCase(field.Name)
		}

		flagPrefix = joinFlagName(flagPrefix, name)
	case w.policy.RecurseUntaggedStructs:
		flagPrefix = joinFlagName(flagPrefix, kebabCase(field.Name))
	default:
		return nil
	}

	child := field.Type
	if child.Kind() == reflect.Pointer {
		child = child.Elem()
	}

	nextConfigPrefix := configPrefix
	if !embedded {
		nextConfigPrefix = joinConfigPath(configPrefix, defaulter.FieldKey(field))
	}

	return w.walkType(child, indexPath, flagPrefix, nextConfigPrefix, display)
}

func validateContainerSpec(embedded bool, spec tagSpec, display string) error {
	if embedded && spec.present && spec.name != "" {
		return fmt.Errorf("%w: %s: an embedded struct is inlined and cannot be named", ErrInvalidTag, display)
	}

	if spec.present && spec.shorthand != "" {
		return fmt.Errorf("%w: %s: a struct container cannot take a shorthand", ErrInvalidTag, display)
	}

	return nil
}

func (w *walker) walkLeaf(
	field reflect.StructField,
	spec tagSpec,
	indexPath []int,
	flagPrefix, configPrefix, display string,
) error {
	if !spec.present && !w.policy.IncludeUntaggedLeaves {
		return nil
	}

	kind, leafType, err := Classify(field.Type)
	if err != nil {
		if spec.present {
			return fmt.Errorf("%s: %w", display, err)
		}

		return nil
	}

	name := spec.name
	if name == "" {
		name = kebabCase(field.Name)
	}

	w.targets = append(w.targets, Target{
		IndexPath: indexPath,
		Display:   display,
		ConfigKey: joinConfigPath(configPrefix, defaulter.FieldKey(field)),
		Name:      joinFlagName(flagPrefix, name),
		Shorthand: spec.shorthand,
		Usage:     strings.TrimSpace(field.Tag.Get(usageTagName)),
		Kind:      kind,
		LeafType:  leafType,
		Secret:    defaulter.IsSecret(field),
		Tagged:    spec.present,
	})

	return nil
}
