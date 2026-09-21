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

	root, ok := plan.OptionalStructTarget(cfg)
	if !ok {
		return nil
	}

	return plan.ForEachTarget(root, plan.Apply, func(root reflect.Value, target *plan.Target) error {
		return applyTarget(cmd, root, target)
	})
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

	for _, candidate := range plan.CandidateFlagNames(target.Name) {
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
	if target.Kind.IsSlice() {
		return replaceSliceValue(cmd, flag, source)
	}

	text, err := plan.EncodeScalar(source, target.Kind)
	if err != nil {
		return err
	}

	name := primaryName(flag)

	if err := cmd.Set(name, text); err != nil {
		return fmt.Errorf("set --%s: %w", name, err)
	}

	return nil
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
func checkIntOverflow(elemVal, converted reflect.Value, targetType reflect.Type) error {
	conv := converted.Int()

	if elemVal.CanInt() {
		raw := elemVal.Int()
		if raw != conv {
			return fmt.Errorf("%w: %v into %s", ErrValueOverflow, raw, targetType)
		}
	} else if elemVal.CanUint() {
		raw := elemVal.Uint()
		if conv < 0 || uint64(conv) != raw {
			return fmt.Errorf("%w: %v into %s", ErrValueOverflow, raw, targetType)
		}
	}

	return nil
}

func checkUintOverflow(elemVal, converted reflect.Value, targetType reflect.Type) error {
	conv := converted.Uint()

	if elemVal.CanUint() {
		raw := elemVal.Uint()
		if raw != conv {
			return fmt.Errorf("%w: %v into %s", ErrValueOverflow, raw, targetType)
		}
	} else if elemVal.CanInt() {
		raw := elemVal.Int()
		if raw < 0 || uint64(raw) != conv {
			return fmt.Errorf("%w: %v into %s", ErrValueOverflow, raw, targetType)
		}
	}

	return nil
}

func checkOverflow(elemVal, converted reflect.Value, targetType reflect.Type) error {
	if converted.CanInt() {
		return checkIntOverflow(elemVal, converted, targetType)
	}

	if converted.CanUint() {
		return checkUintOverflow(elemVal, converted, targetType)
	}

	return nil
}

func assignSliceDestination[T any](destination *[]T, source reflect.Value) error {
	out := make([]T, source.Len())
	targetType := reflect.TypeFor[T]()

	for i := range source.Len() {
		elemVal := source.Index(i)
		if element, ok := reflect.TypeAssert[T](elemVal); ok {
			out[i] = element
			continue
		}

		if !elemVal.Type().ConvertibleTo(targetType) {
			return fmt.Errorf("%w: %s into %s", ErrValueOverflow, elemVal.Type(), reflect.TypeFor[[]T]())
		}

		converted := elemVal.Convert(targetType)
		if err := checkOverflow(elemVal, converted, targetType); err != nil {
			return err
		}

		val, ok := reflect.TypeAssert[T](converted)
		if !ok {
			return fmt.Errorf("%w: %s into %s", ErrValueOverflow, elemVal.Type(), reflect.TypeFor[[]T]())
		}

		out[i] = val
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
