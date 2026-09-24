package strataurfave

import (
	"fmt"
	"reflect"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

type pendingFlag struct {
	target  plan.Target
	storage reflect.Value
}

type textValue struct {
	text *string
}

type extendedDurationValue struct{ duration *time.Duration }

func (v *textValue) Set(text string) error { *v.text = text; return nil }
func (v *textValue) String() string        { return *v.text }
func (v *textValue) Get() any              { return *v.text }

func bindFlag(p *pendingFlag) (cli.Flag, error) {
	switch p.target.Kind {
	case plan.KindText:
		return bindTextFlag(p)
	case plan.KindString:
		return bindStringFlag(p)
	case plan.KindBool:
		return bindBoolFlag(p)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return bindNumericFlag(p)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return bindSliceFlag(p)
	case plan.KindUnsupported:
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	default:
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}
}

func bindTextFlag(p *pendingFlag) (cli.Flag, error) {
	storage, ok := reflect.TypeAssert[*string](p.storage)
	if !ok {
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	value := &textValue{text: storage}
	destination := new(cli.Value)
	*destination = value

	return &cli.GenericFlag{
		Name:        p.target.Name,
		Aliases:     flagAliases(&p.target),
		Usage:       p.target.Usage,
		Value:       value,
		Destination: destination,
		HideDefault: p.target.Secret,
	}, nil
}

func bindStringFlag(p *pendingFlag) (cli.Flag, error) {
	storage, ok := reflect.TypeAssert[*string](p.storage)
	if !ok {
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	return &cli.StringFlag{
		Name:        p.target.Name,
		Aliases:     flagAliases(&p.target),
		Usage:       p.target.Usage,
		Value:       *storage,
		Destination: storage,
		HideDefault: p.target.Secret,
	}, nil
}

func bindBoolFlag(p *pendingFlag) (cli.Flag, error) {
	storage, ok := reflect.TypeAssert[*bool](p.storage)
	if !ok {
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}

	return &cli.BoolFlag{
		Name:        p.target.Name,
		Aliases:     flagAliases(&p.target),
		Usage:       p.target.Usage,
		Value:       *storage,
		Destination: storage,
		HideDefault: p.target.Secret,
	}, nil
}

func bindNumericFlag(p *pendingFlag) (cli.Flag, error) {
	usage := p.target.Usage
	aliases := flagAliases(&p.target)
	hidden := p.target.Secret

	switch storage := p.storage.Interface().(type) {
	case *int:
		return &cli.IntFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *int64:
		return &cli.Int64Flag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *uint:
		return &cli.UintFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *uint64:
		return &cli.Uint64Flag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *float32:
		return &cli.Float32Flag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *float64:
		return &cli.Float64Flag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *time.Duration:
		value := &extendedDurationValue{duration: storage}

		var destination cli.Value = value

		return &cli.GenericFlag{Name: p.target.Name, Aliases: aliases, Usage: usage, Value: value, Destination: &destination, HideDefault: hidden}, nil
	default:
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}
}

func (v *extendedDurationValue) Set(raw string) error {
	duration, err := strata.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration flag: %w", err)
	}

	*v.duration = duration

	return nil
}

func (v *extendedDurationValue) String() string { return v.duration.String() }

func (v *extendedDurationValue) Get() any { return *v.duration }

func bindSliceFlag(p *pendingFlag) (cli.Flag, error) {
	usage := p.target.Usage
	aliases := flagAliases(&p.target)
	hidden := p.target.Secret

	switch storage := p.storage.Interface().(type) {
	case *[]string:
		return &cli.StringSliceFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *[]int:
		return &cli.IntSliceFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	case *[]int64:
		return &cli.Int64SliceFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %s", plan.ErrUnsupportedFieldType, p.target.Display)
	}
}

func flagAliases(target *plan.Target) []string {
	if target.Shorthand == "" {
		return nil
	}

	return []string{target.Shorthand}
}
