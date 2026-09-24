package stratacobra

import (
	"fmt"
	"reflect"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

func syncTarget(cmd *cobra.Command, root reflect.Value, bound *boundFlag, meta *strata.Metadata) error {
	target := &bound.target
	flag := cmd.Flag(target.Name)

	if flag != bound.flag || !flag.Changed {
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
	switch target.Kind {
	case plan.KindString, plan.KindText:
		return decodeString(fs, target, dst)
	case plan.KindBool:
		return decodeBool(fs, target, dst)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return decodeNumeric(fs, target, dst)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return decodeSlice(fs, target, dst)
	case plan.KindUnsupported:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
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
	switch target.Kind {
	case plan.KindInt, plan.KindInt64, plan.KindDuration:
		return decodeSigned(fs, target, dst)
	case plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64:
		return decodeOtherNumeric(fs, target, dst)
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSigned(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
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
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeOtherNumeric(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
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
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindInt, plan.KindInt64, plan.KindDuration, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSlice(fs *pflag.FlagSet, target *plan.Target, dst reflect.Value) error {
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
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration, plan.KindText:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
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
