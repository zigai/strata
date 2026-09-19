package stratacobra

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata/internal/plan"
)

// Apply writes configuration values from cfg into the flags of cmd. It performs
// the configuration-to-flags direction of the merge and leaves every flag the
// parser marked as supplied untouched.
//
// Apply shares its field traversal with RegisterFlags and SyncFlagsToStruct, and
// a field tagged `flag:"port,p"` is matched through the same tag parser
// everywhere. A field that carries no "flag" tag keeps the historical lenient
// matching against flags derived from the field name, and hand-registered flags
// continue to receive configuration values.
//
// A cfg that is nil, is not a pointer, or is not a struct leaves the command
// untouched and reports no error. RegisterFlags and SyncFlagsToStruct are the
// strict counterparts and report ErrNotStruct for such a cfg.
func Apply(cmd *cobra.Command, cfg any) error {
	if cmd == nil {
		return nil
	}

	root, ok := optionalStructTarget(cfg)
	if !ok {
		return nil
	}

	targets, err := plan.Build(root.Type(), plan.Apply)
	if err != nil {
		return fmt.Errorf("build field plan: %w", err)
	}

	for i := range targets {
		if err := applyTarget(cmd, root, &targets[i]); err != nil {
			return err
		}
	}

	return nil
}

func optionalStructTarget(cfg any) (reflect.Value, bool) {
	if cfg == nil {
		return reflect.Value{}, false
	}

	val := reflect.ValueOf(cfg)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return reflect.Value{}, false
		}

		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}

	return val, true
}

func applyTarget(cmd *cobra.Command, root reflect.Value, target *plan.Target) error {
	flag := findFlag(cmd, target)
	if flag == nil || flag.Changed {
		return nil
	}

	source, ok := plan.ResolveField(root, target.IndexPath)
	if !ok {
		return nil
	}

	if source.Kind() == reflect.Pointer {
		if source.IsNil() {
			return nil
		}

		source = source.Elem()
	}

	if err := writeFlagValue(flag, target.Kind, source); err != nil {
		return fmt.Errorf("apply %s to --%s: %w", target.Display, flag.Name, err)
	}

	return nil
}

func findFlag(cmd *cobra.Command, target *plan.Target) *pflag.Flag {
	// A tagged name is matched exactly. A fallback to the reduced candidate
	// names would let a nested configuration path such as "database.port" match
	// an unrelated top-level --port. A one-character name may still match a
	// shorthand, which is unambiguous.
	if target.Tagged {
		if flag := cmd.Flag(target.Name); flag != nil {
			return flag
		}

		if len(target.Name) == 1 {
			return cmd.Flags().ShorthandLookup(target.Name)
		}

		return nil
	}

	for _, candidate := range candidateNames(target.Name) {
		if flag := cmd.Flag(candidate); flag != nil {
			return flag
		}

		if len(candidate) == 1 {
			if flag := cmd.Flags().ShorthandLookup(candidate); flag != nil {
				return flag
			}
		}
	}

	return nil
}

func candidateNames(name string) []string {
	parts := strings.Split(name, ".")
	kebab := strings.Join(parts, "-")
	snake := strings.Join(parts, "_")
	leaf := parts[len(parts)-1]

	return []string{name, kebab, snake, leaf}
}

func writeFlagValue(flag *pflag.Flag, kind plan.Kind, source reflect.Value) error {
	if isSliceKind(kind) {
		return replaceSliceValue(flag, source)
	}

	text, err := encodeScalar(source, kind)
	if err != nil {
		return err
	}

	if err := flag.Value.Set(text); err != nil {
		return fmt.Errorf("set --%s: %w", flag.Name, err)
	}

	return nil
}

// isSliceKind reports whether a kind carries a slice value, which the write path
// replaces as a whole rather than rendering as one scalar.
func isSliceKind(kind plan.Kind) bool {
	return kind == plan.KindStringSlice || kind == plan.KindIntSlice || kind == plan.KindInt64Slice
}

// replaceSliceValue writes source into flag through pflag's typed replacement
// interface. The element list is passed through directly, and elements containing
// commas, quotes, backslashes, or newlines survive intact. A string round trip
// would not preserve them.
//
// A flag whose value has no replacement method produces an error wrapping
// ErrUnsupportedFieldType.
func replaceSliceValue(flag *pflag.Flag, source reflect.Value) error {
	replacer, ok := flag.Value.(interface{ Replace(val []string) error })
	if !ok {
		return fmt.Errorf("%w: --%s does not support slice replacement", ErrUnsupportedFieldType, flag.Name)
	}

	items := make([]string, source.Len())
	for i := range source.Len() {
		items[i] = fmt.Sprint(source.Index(i).Interface())
	}

	if err := replacer.Replace(items); err != nil {
		return fmt.Errorf("replace --%s: %w", flag.Name, err)
	}

	return nil
}

// encodeScalar renders a configuration value as the string form the flag's
// parser accepts. A text codec is rendered by its own marshaller, which keeps the
// round trip exact; every other kind uses the natural Go representation.
func encodeScalar(source reflect.Value, kind plan.Kind) (string, error) {
	//nolint:exhaustive // every non-text, non-string kind uses its natural Go representation
	switch kind {
	case plan.KindText:
		return marshalLeaf(source)
	case plan.KindString:
		return source.String(), nil
	default:
		return fmt.Sprint(source.Interface()), nil
	}
}
