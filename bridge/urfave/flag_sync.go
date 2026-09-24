package strataurfave

import (
	"fmt"
	"reflect"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

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
	switch target.Kind {
	case plan.KindString, plan.KindText:
		return decodeString(cmd, target, dst)
	case plan.KindBool:
		return decodeBool(cmd, target, dst)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return decodeNumeric(cmd, target, dst)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return decodeSlice(cmd, target, dst)
	case plan.KindUnsupported:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
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
	switch target.Kind {
	case plan.KindInt, plan.KindInt64, plan.KindDuration:
		return decodeSigned(cmd, target, dst)
	case plan.KindUint, plan.KindUint64:
		return decodeUnsigned(cmd, target, dst)
	case plan.KindFloat32, plan.KindFloat64:
		return decodeFloat(cmd, target, dst)
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSigned(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	switch target.Kind {
	case plan.KindInt:
		return plan.AssignInt(dst, int64(cmd.Int(target.Name)))
	case plan.KindInt64:
		return plan.AssignInt(dst, cmd.Int64(target.Name))
	case plan.KindDuration:
		return plan.AssignInt(dst, int64(cmd.Duration(target.Name)))
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeUnsigned(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	switch target.Kind {
	case plan.KindUint:
		return plan.AssignUint(dst, uint64(cmd.Uint(target.Name)))
	case plan.KindUint64:
		return plan.AssignUint(dst, cmd.Uint64(target.Name))
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindInt, plan.KindInt64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeFloat(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	switch target.Kind {
	case plan.KindFloat32:
		return plan.AssignFloat(dst, float64(cmd.Float32(target.Name)))
	case plan.KindFloat64:
		return plan.AssignFloat(dst, cmd.Float64(target.Name))
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindDuration, plan.KindText, plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	}
}

func decodeSlice(cmd *cli.Command, target *plan.Target, dst reflect.Value) error {
	switch target.Kind {
	case plan.KindStringSlice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.StringSlice(target.Name)))
	case plan.KindIntSlice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.IntSlice(target.Name)))
	case plan.KindInt64Slice:
		return plan.AssignSlice(dst, reflect.ValueOf(cmd.Int64Slice(target.Name)))
	case plan.KindUnsupported, plan.KindString, plan.KindBool, plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration, plan.KindText:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, target.Display)
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
