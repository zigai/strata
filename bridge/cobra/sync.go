package stratacobra

import (
	"fmt"
	"reflect"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

// WithFlags returns a [strata.Option] that injects explicitly supplied CLI flags
// into the configuration cascade before validation runs.
//
// Supplied flags are recorded in the load's Metadata as SourceFlag.
// Flags not explicitly supplied by the user leave the loaded configuration untouched.
func WithFlags(cmd *cobra.Command, opts ...FlagOption) strata.Option {
	return strata.WithContribution(func(target any, meta *strata.Metadata) error {
		mergedOpts := make([]FlagOption, 0, len(opts)+1)
		mergedOpts = append(mergedOpts, opts...)
		mergedOpts = append(mergedOpts, withMetadata(meta))

		return syncFlagsToStruct(cmd, target, mergedOpts...)
	})
}

// syncFlagsToStruct copies flag values into the corresponding fields of cfg, but
// only for flags the parser marked as explicitly supplied by the user. A flag
// whose value came from configuration is left alone, and a field whose flag was
// not registered is untouched.
//
// An optional (pointer) field is allocated only when one of its flags was
// explicitly supplied. A nil subtree the configuration never populated stays
// nil.
//
// When withMetadata is supplied, every value written here is recorded with
// SourceFlag and the flag's long name as its path. A field tagged as secret is
// recorded with a redacted raw value.
func syncFlagsToStruct(cmd *cobra.Command, cfg any, opts ...FlagOption) error {
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

func syncTarget(cmd *cobra.Command, root reflect.Value, target *plan.Target, meta *strata.Metadata) error {
	flag := cmd.Flag(target.Name)
	if flag == nil || !flag.Changed {
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

	if err := decodeFlag(cmd.Flags(), target, dst); err != nil {
		return fmt.Errorf("synchronize %s from --%s: %w", target.Display, target.Name, err)
	}

	recordOrigin(meta, target, flag.Value.String())

	return nil
}

func decodeFlag(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // plan.KindUnsupported is never produced by plan.Classify for a tagged field
	switch target.Kind {
	case plan.KindString, plan.KindText:
		return decodeString(fs, target, dst)
	case plan.KindBool:
		return decodeBool(fs, target, dst)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return decodeNumeric(fs, target, dst)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return decodeSlice(fs, target, dst)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeString(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	value, err := fs.GetString(target.Name)
	if err != nil {
		return fmt.Errorf("read --%s: %w", target.Name, err)
	}

	if target.Kind == plan.KindString {
		dst.SetString(value)

		return nil
	}

	return plan.UnmarshalLeaf(dst, value)
}

func decodeBool(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	value, err := fs.GetBool(target.Name)
	if err != nil {
		return fmt.Errorf("read --%s: %w", target.Name, err)
	}

	dst.SetBool(value)

	return nil
}

func decodeNumeric(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // the default branch rejects every non-numeric kind
	switch target.Kind {
	case plan.KindInt, plan.KindInt64, plan.KindDuration:
		return decodeSigned(fs, target, dst)
	case plan.KindUint, plan.KindUint64:
		return decodeUnsigned(fs, target, dst)
	case plan.KindFloat32, plan.KindFloat64:
		return decodeFloat(fs, target, dst)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSigned(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for signed kinds; the default is unreachable
	switch target.Kind {
	case plan.KindInt:
		value, err := fs.GetInt(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignInt(dst, int64(value))
	case plan.KindInt64:
		value, err := fs.GetInt64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignInt(dst, value)
	case plan.KindDuration:
		value, err := fs.GetDuration(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignInt(dst, int64(value))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeUnsigned(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for unsigned kinds; the default is unreachable
	switch target.Kind {
	case plan.KindUint:
		value, err := fs.GetUint(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignUint(dst, uint64(value))
	case plan.KindUint64:
		value, err := fs.GetUint64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignUint(dst, value)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeFloat(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for floating-point kinds; the default is unreachable
	switch target.Kind {
	case plan.KindFloat32:
		value, err := fs.GetFloat32(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignFloat(dst, float64(value))
	case plan.KindFloat64:
		value, err := fs.GetFloat64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignFloat(dst, value)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSlice(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
	//nolint:exhaustive // called only for slice kinds; the default is unreachable
	switch target.Kind {
	case plan.KindStringSlice:
		value, err := fs.GetStringSlice(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignSlice(dst, reflect.ValueOf(value))
	case plan.KindIntSlice:
		value, err := fs.GetIntSlice(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignSlice(dst, reflect.ValueOf(value))
	case plan.KindInt64Slice:
		value, err := fs.GetInt64Slice(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return plan.AssignSlice(dst, reflect.ValueOf(value))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
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
// The option affects syncFlagsToStruct only; registration does not report
// provenance.
func withMetadata(meta *strata.Metadata) FlagOption {
	return func(config *flagConfig) {
		config.metadata = meta
	}
}
