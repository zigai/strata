package stratacobra

import (
	"fmt"
	"reflect"
	"time"

	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

type pendingFlag struct {
	target  plan.Target
	storage reflect.Value
}

type durationFlagValue struct{ value *time.Duration }

func bindFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	switch p.target.Kind {
	case plan.KindString, plan.KindText:
		return bindStringFlag(destination, p)
	case plan.KindBool:
		return bindBoolFlag(destination, p)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return bindNumericFlag(destination, p)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return bindSliceFlag(destination, p)
	case plan.KindUnsupported:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}
}

func bindStringFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	pointer, ok := reflect.TypeAssert[*string](p.storage)
	if !ok {
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	destination.StringVarP(pointer, p.target.Name, p.target.Shorthand, *pointer, p.target.Usage)

	return nil
}

func bindBoolFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	pointer, ok := reflect.TypeAssert[*bool](p.storage)
	if !ok {
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	destination.BoolVarP(pointer, p.target.Name, p.target.Shorthand, *pointer, p.target.Usage)

	return nil
}

func bindNumericFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	switch value := p.storage.Interface().(type) {
	case *int:
		destination.IntVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *int64:
		destination.Int64VarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *uint:
		destination.UintVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *uint64:
		destination.Uint64VarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *float32:
		destination.Float32VarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *float64:
		destination.Float64VarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *time.Duration:
		destination.VarP(&durationFlagValue{value: value}, p.target.Name, p.target.Shorthand, p.target.Usage)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	return nil
}

func (v *durationFlagValue) Set(raw string) error {
	duration, err := strata.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration flag: %w", err)
	}

	*v.value = duration

	return nil
}

func (v *durationFlagValue) String() string { return v.value.String() }

func (v *durationFlagValue) Type() string { return "duration" }

func bindSliceFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	switch value := p.storage.Interface().(type) {
	case *[]string:
		destination.StringSliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *[]int:
		destination.IntSliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *[]int64:
		destination.Int64SliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	default:
		return fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	return nil
}
