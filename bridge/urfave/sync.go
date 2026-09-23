package strataurfave

import (
	"fmt"
	"reflect"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

// WithFlags returns a [strata.Option] that injects explicitly supplied CLI flags
// into the configuration cascade before validation runs.
//
// Supplied flags are recorded in the load's Metadata as SourceFlag.
// Flags not explicitly supplied by the user leave the loaded configuration untouched.
func WithFlags(cmd *cli.Command, opts ...FlagOption) strata.Option {
	return strata.WithContribution(func(target any, meta *strata.Metadata) error {
		mergedOpts := make([]FlagOption, 0, len(opts)+1)
		mergedOpts = append(mergedOpts, opts...)
		mergedOpts = append(mergedOpts, withMetadata(meta))

		return syncFlagsToStruct(cmd, target, mergedOpts...)
	})
}

// syncFlagsToStruct copies flag values into the corresponding fields of cfg, but
// only for flags the user supplied explicitly on the command line. A flag value
// that came from configuration is left alone, and a field whose flag was not
// registered is untouched.
//
// Command.IsSet is the marker read here, so only values the parser saw on the
// command line are copied.
//
// An optional (pointer) field is allocated only when one of its flags was
// explicitly supplied. A nil subtree the configuration never populated stays
// nil.
//
// When withMetadata is supplied, every value written here is recorded with
// SourceFlag and the flag's long name as its path. A field tagged as secret is
// recorded with a redacted raw value.
func syncFlagsToStruct(cmd *cli.Command, cfg any, opts ...FlagOption) error {
	if cmd == nil {
		return nil
	}

	options := resolveFlagOptions(opts)

	root, err := plan.StructTarget(cfg)
	if err != nil {
		return err
	}

	return plan.ForEachTarget(root, plan.Registration, func(root reflect.Value, target *plan.Target) error {
		return syncTarget(cmd, root, target, options.metadata)
	})
}

func syncTarget(cmd *cli.Command, root reflect.Value, target *plan.Target, meta *strata.Metadata) error {
	if !cmd.IsSet(target.Name) {
		return nil
	}

	dst, err := plan.EnsureField(root, target.IndexPath)
	if err != nil {
		return fmt.Errorf("resolve field %s: %w", target.Display, err)
	}

	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}

		dst = dst.Elem()
	}

	if err := decodeFlag(cmd, target, dst); err != nil {
		return fmt.Errorf("synchronize %s from --%s: %w", target.Display, target.Name, err)
	}

	recordOrigin(meta, target, rawFlagValue(cmd, target.Name))

	return nil
}

func decodeFlag(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // plan.KindUnsupported is never produced by plan.Classify for a tagged field
	switch target.Kind {
	case plan.KindString, plan.KindText:
		return decodeString(cmd, target, dst)
	case plan.KindBool:
		return decodeBool(cmd, target, dst)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return decodeNumeric(cmd, target, dst)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return decodeSlice(cmd, target, dst)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeString(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	value := cmd.String(target.Name)

	if target.Kind == plan.KindString {
		dst.SetString(value)

		return nil
	}

	return plan.UnmarshalLeaf(dst, value)
}

func decodeBool(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	dst.SetBool(cmd.Bool(target.Name))

	return nil
}

func decodeNumeric(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // the default branch rejects every non-numeric kind
	switch target.Kind {
	case plan.KindInt, plan.KindInt64, plan.KindDuration:
		return decodeSigned(cmd, target, dst)
	case plan.KindUint, plan.KindUint64:
		return decodeUnsigned(cmd, target, dst)
	case plan.KindFloat32, plan.KindFloat64:
		return decodeFloat(cmd, target, dst)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSigned(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for signed kinds; the default is unreachable
	switch target.Kind {
	case plan.KindInt:
		return plan.AssignInt(dst, int64(cmd.Int(target.Name)))
	case plan.KindInt64:
		return plan.AssignInt(dst, cmd.Int64(target.Name))
	case plan.KindDuration:
		return plan.AssignInt(dst, int64(cmd.Duration(target.Name)))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeUnsigned(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for unsigned kinds; the default is unreachable
	switch target.Kind {
	case plan.KindUint:
		return plan.AssignUint(dst, uint64(cmd.Uint(target.Name)))
	case plan.KindUint64:
		return plan.AssignUint(dst, cmd.Uint64(target.Name))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeFloat(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for floating-point kinds; the default is unreachable
	switch target.Kind {
	case plan.KindFloat32:
		return plan.AssignFloat(dst, float64(cmd.Float32(target.Name)))
	case plan.KindFloat64:
		return plan.AssignFloat(dst, cmd.Float64(target.Name))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSlice(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for slice kinds; the default is unreachable
	switch target.Kind {
	case plan.KindStringSlice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.StringSlice(target.Name)))
	case plan.KindIntSlice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.IntSlice(target.Name)))
	case plan.KindInt64Slice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.Int64Slice(target.Name)))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func rawFlagValue(cmd *cli.Command, name string) string {
	return fmt.Sprint(cmd.Value(name))
}

func recordOrigin(meta *strata.Metadata, target *plan.Target, raw string) {
	if meta == nil {
		return
	}

	if target.Secret {
		raw = "[REDACTED]"
	}

	meta.Record(strata.Origin{
		Key:      target.ConfigKey,
		Source:   strata.SourceFlag,
		Path:     "--" + target.Name,
		Line:     0,
		RawValue: raw,
	})
}

// withMetadata makes syncFlagsToStruct record the origin of every flag value
// that becomes the winning configuration for its key. A field tagged as secret is
// recorded with a redacted raw value.
//
// The option affects syncFlagsToStruct only; generation and registration do not
// report provenance.
func withMetadata(meta *strata.Metadata) FlagOption {
	return func(config *flagConfig) {
		config.metadata = meta
	}
}
