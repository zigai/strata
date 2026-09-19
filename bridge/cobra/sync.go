package stratacobra

import (
	"encoding"
	"fmt"
	"reflect"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

// SyncFlagsToStruct copies flag values into the corresponding fields of cfg, but
// only for flags the parser marked as explicitly supplied by the user. A flag
// whose value came from configuration is left alone, and a field whose flag was
// not registered is untouched.
//
// SyncFlagsToStruct is called after loading configuration and before Apply. That
// order matters: Apply writes through the framework's flag setters, and on
// urfave/cli that marks a flag as supplied. A synchronization performed
// afterwards could no longer tell user input from configuration.
//
// An optional (pointer) field is allocated only when one of its flags was
// explicitly supplied. A nil subtree the configuration never populated stays
// nil.
//
// When WithMetadata is supplied, every value written here is recorded with
// SourceFlag and the flag's long name as its path. A field tagged as secret is
// recorded with a redacted raw value.
func SyncFlagsToStruct(cmd *cobra.Command, cfg any, opts ...FlagOption) error {
	if cmd == nil {
		return nil
	}

	options := resolveFlagOptions(opts)

	root, err := structTarget(cfg)
	if err != nil {
		return err
	}

	targets, err := plan.Build(root.Type(), plan.Registration)
	if err != nil {
		return fmt.Errorf("build flag plan: %w", err)
	}

	for i := range targets {
		if err := syncTarget(cmd, root, &targets[i], options.metadata); err != nil {
			return err
		}
	}

	return nil
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

	return unmarshalLeaf(dst, value)
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

		return assignInt(dst, int64(value))
	case plan.KindInt64:
		value, err := fs.GetInt64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignInt(dst, value)
	case plan.KindDuration:
		value, err := fs.GetDuration(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignInt(dst, int64(value))
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

		return assignUint(dst, uint64(value))
	case plan.KindUint64:
		value, err := fs.GetUint64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignUint(dst, value)
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

		return assignFloat(dst, float64(value))
	case plan.KindFloat64:
		value, err := fs.GetFloat64(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignFloat(dst, value)
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

		return assignSlice(dst, reflect.ValueOf(value))
	case plan.KindIntSlice:
		value, err := fs.GetIntSlice(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignSlice(dst, reflect.ValueOf(value))
	case plan.KindInt64Slice:
		value, err := fs.GetInt64Slice(target.Name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", target.Name, err)
		}

		return assignSlice(dst, reflect.ValueOf(value))
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, target.Display)
	}
}

func unmarshalLeaf(dst reflect.Value, value string) error {
	if dst.CanAddr() {
		if unmarshaller, ok := reflect.TypeAssert[encoding.TextUnmarshaler](dst.Addr()); ok {
			if err := unmarshaller.UnmarshalText([]byte(value)); err != nil {
				return fmt.Errorf("unmarshal %s: %w", dst.Type(), err)
			}

			return nil
		}
	}

	return fmt.Errorf("%w: %s does not implement encoding.TextUnmarshaler", ErrUnsupportedFieldType, dst.Type())
}

func assignInt(dst reflect.Value, value int64) error {
	if dst.OverflowInt(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetInt(value)

	return nil
}

func assignUint(dst reflect.Value, value uint64) error {
	if dst.OverflowUint(value) {
		return fmt.Errorf("%w: %d does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetUint(value)

	return nil
}

func assignFloat(dst reflect.Value, value float64) error {
	if dst.OverflowFloat(value) {
		return fmt.Errorf("%w: %v does not fit %s", ErrValueOverflow, value, dst.Type())
	}

	dst.SetFloat(value)

	return nil
}

func assignSlice(dst, src reflect.Value) error {
	out := reflect.MakeSlice(dst.Type(), src.Len(), src.Len())

	for i := range src.Len() {
		element := src.Index(i)
		targetType := out.Index(i).Type()

		switch {
		case element.Type().AssignableTo(targetType):
			out.Index(i).Set(element)
		case element.Type().ConvertibleTo(targetType):
			out.Index(i).Set(element.Convert(targetType))
		default:
			return fmt.Errorf("%w: %s into %s", ErrValueOverflow, element.Type(), targetType)
		}
	}

	dst.Set(out)

	return nil
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
