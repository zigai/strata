// Package strataurfave registers CLI flags from a configuration struct on
// urfave/cli commands and merges the parsed flags back into that struct.
//
// # Lifecycle
//
// Generated flags own detached storage, and no generated flag points at a
// caller's field. The canonical order is
//
//	RegisterFlags(cmd, &cfg)          // before parsing: seeds detached storage
//	// Load: defaults -> system -> user -> project -> environment
//	SyncFlagsToStruct(cmd, &cfg)      // after parsing: CLI values -> cfg
//	Apply(cmd, cfg)                   // cfg -> flags the user did not supply
//
// Sync before Apply is required. Apply writes configuration values into flags
// through urfave/cli's Command.Set, which marks the flag as supplied, and
// SyncFlagsToStruct reads Command.IsSet. Sync MUST therefore run while that
// marker still reflects what the parser saw.
//
// # Merge precedence
//
//	CLI flag > environment > project file > user file > system file > defaults
//
// SyncFlagsToStruct writes only the flags the user supplied, Apply writes only
// the flags the user did not, and those conditions are disjoint. Both orders
// therefore agree, provided Sync reads the marker before Apply changes it.
//
// # Detached storage
//
// RegisterFlags seeds each flag's storage from the configuration value and keeps
// that storage to itself. A loader that reassigns the caller's configuration
// value between parsing and synchronization cannot destroy a parsed CLI value.
package strataurfave

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

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
//
// A scalar is written through urfave/cli's Command.Set, which marks the flag as
// supplied. The caller MUST run SyncFlagsToStruct before Apply; see the package
// documentation.
func Apply(cmd *cli.Command, cfg any) error {
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

func applyTarget(cmd *cli.Command, root reflect.Value, target *plan.Target) error {
	flag := findFlag(cmd, target)
	if flag == nil || flag.IsSet() {
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

	if err := writeFlagValue(cmd, flag, target, source); err != nil {
		return fmt.Errorf("apply %s to --%s: %w", target.Display, primaryName(flag), err)
	}

	return nil
}

// findFlag matches a plan target against the flags of the command. A tagged
// target is matched by exact name. A fallback to the reduced candidate names
// would let a nested configuration path such as "database.port" match an
// unrelated top-level --port. An untagged target keeps the historical candidate
// list, and flags derived from field names continue to match.
func findFlag(cmd *cli.Command, target *plan.Target) cli.Flag {
	if target.Tagged {
		return lookupFlag(cmd, target.Name)
	}

	for _, candidate := range candidateNames(target.Name) {
		if flag := lookupFlag(cmd, candidate); flag != nil {
			return flag
		}
	}

	return nil
}

// lookupFlag resolves a name or alias on the command or one of its ancestors,
// following urfave/cli's own lookup order.
func lookupFlag(cmd *cli.Command, name string) cli.Flag {
	for _, ancestor := range cmd.Lineage() {
		for _, flag := range ancestor.Flags {
			if slices.Contains(flag.Names(), name) {
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

// primaryName reports the name urfave/cli lists first, which is the name a write
// should address: every alias resolves to the same flag.
func primaryName(flag cli.Flag) string {
	names := flag.Names()
	if len(names) == 0 {
		return ""
	}

	return names[0]
}

func writeFlagValue(cmd *cli.Command, flag cli.Flag, target *plan.Target, source reflect.Value) error {
	if isSliceKind(target.Kind) {
		return replaceSliceValue(cmd, flag, source)
	}

	text, err := encodeScalar(source, target.Kind)
	if err != nil {
		return err
	}

	name := primaryName(flag)

	if err := cmd.Set(name, text); err != nil {
		return fmt.Errorf("set --%s: %w", name, err)
	}

	return nil
}

// isSliceKind reports whether a kind carries a slice value, which the write path
// replaces as a whole rather than rendering as one scalar.
func isSliceKind(kind plan.Kind) bool {
	return kind == plan.KindStringSlice || kind == plan.KindIntSlice || kind == plan.KindInt64Slice
}

// replaceSliceValue replaces the slice of a flag. A flag generated by this bridge
// owns the destination it registered, and the slice is assigned directly:
// elements containing commas, quotes, backslashes, or newlines survive intact. An
// element that cannot be stored in the destination slice produces an error
// wrapping ErrValueOverflow.
//
// A flag the bridge did not generate exposes no destination, which leaves
// urfave/cli's string interface as the only remaining path; the elements are then
// joined with commas, and urfave/cli clears a slice on its first write. That path
// replaces the registered default and does not append to it.
func replaceSliceValue(cmd *cli.Command, flag cli.Flag, source reflect.Value) error {
	switch typed := flag.(type) {
	case *cli.StringSliceFlag:
		if typed.Destination != nil {
			return assignSliceDestination(typed.Destination, source)
		}
	case *cli.IntSliceFlag:
		if typed.Destination != nil {
			return assignSliceDestination(typed.Destination, source)
		}
	case *cli.Int64SliceFlag:
		if typed.Destination != nil {
			return assignSliceDestination(typed.Destination, source)
		}
	}

	return setSliceText(cmd, flag, source)
}

// assignSliceDestination replaces the slice a generated flag points at. The flag
// holds exactly the configuration's elements and never an append of two sources,
// which mirrors pflag's own Replace.
func assignSliceDestination[T any](destination *[]T, source reflect.Value) error {
	out := make([]T, source.Len())

	for i := range source.Len() {
		element, ok := reflect.TypeAssert[T](source.Index(i))
		if !ok {
			return fmt.Errorf("%w: %s into %s", ErrValueOverflow, source.Index(i).Type(), reflect.TypeFor[[]T]())
		}

		out[i] = element
	}

	*destination = out

	return nil
}

func setSliceText(cmd *cli.Command, flag cli.Flag, source reflect.Value) error {
	items := make([]string, source.Len())
	for i := range source.Len() {
		items[i] = fmt.Sprint(source.Index(i).Interface())
	}

	name := primaryName(flag)

	if err := cmd.Set(name, strings.Join(items, ",")); err != nil {
		return fmt.Errorf("set --%s: %w", name, err)
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
