package stratacobra

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"
	"github.com/zigai/strata/internal/plan"
)

var (
	// ErrNotStruct is returned when cfg is nil, is not a pointer, or does not
	// point to a struct.
	//
	// The error value is declared by internal/plan, which reports the same
	// condition for the field paths it walks.
	ErrNotStruct = plan.ErrNotStruct

	// ErrInvalidTag is returned when a "flag" struct tag cannot be parsed or
	// contradicts the field it is attached to.
	//
	// The error value is declared by internal/plan, which parses the tag.
	ErrInvalidTag = plan.ErrInvalidTag

	// ErrDuplicateFlag is returned when two fields map to the same flag name or
	// the same shorthand.
	ErrDuplicateFlag = errors.New("two fields map to the same flag")

	// ErrFlagConflict is returned when a generated flag name or shorthand is
	// already registered on the destination command.
	//
	// No flags are registered when this error is returned.
	ErrFlagConflict = errors.New("generated flag conflicts with an existing flag")

	// ErrUnsupportedFieldType is returned when a tagged field has no flag
	// mapping, and when a value cannot pass through the mapping the field does
	// have.
	//
	// The error value is declared by internal/plan, which reports a tagged field
	// with no mapping while it builds the field schema. A slice whose element type
	// is not assignable to the destination element type is reported here while
	// detached storage is seeded, and the synchronization path reports the same
	// condition as ErrValueOverflow.
	ErrUnsupportedFieldType = plan.ErrUnsupportedFieldType

	// ErrRecursiveType is returned when a struct type repeats on a single
	// traversal path.
	//
	// The error value is declared by internal/plan, which walks the field schema.
	ErrRecursiveType = plan.ErrRecursiveType

	// ErrValueOverflow is returned when a configuration value does not fit the
	// destination field's width.
	//
	// A slice whose element type is not assignable to the destination element type
	// is reported here when a CLI value is written back into the field. The
	// seeding path reports the same condition as ErrUnsupportedFieldType.
	ErrValueOverflow = errors.New("value does not fit the destination field")
)

// flagConfig carries the options accepted by RegisterFlags and
// SyncFlagsToStruct.
type flagConfig struct {
	persistent bool
	metadata   *strata.Metadata
}

// FlagOption configures a flag operation.
type FlagOption func(*flagConfig)

// pendingFlag couples a discovered field target with its detached storage. The
// storage is owned by the bridge and never aliases a caller field. A parsed CLI
// value therefore survives a loader that reassigns cfg between parsing and
// synchronization.
type pendingFlag struct {
	target  plan.Target
	storage reflect.Value
}

// WithPersistent registers generated flags on cmd.PersistentFlags() in place of
// cmd.Flags().
//
// The option affects RegisterFlags only; SyncFlagsToStruct and Apply match
// against both flag sets regardless.
func WithPersistent() FlagOption {
	return func(config *flagConfig) {
		config.persistent = true
	}
}

// WithMetadata makes SyncFlagsToStruct record the origin of every flag value
// that becomes the winning configuration for its key. A field tagged as secret is
// recorded with a redacted raw value.
//
// The option affects SyncFlagsToStruct only; registration does not report
// provenance.
func WithMetadata(meta *strata.Metadata) FlagOption {
	return func(config *flagConfig) {
		config.metadata = meta
	}
}

// RegisterFlags discovers CLI flags from the exported fields of cfg and
// registers them on cmd with detached storage.
//
// Discovery, seeding, and validation failures are reported as ErrNotStruct,
// ErrInvalidTag, ErrDuplicateFlag, ErrFlagConflict, ErrUnsupportedFieldType, or
// ErrRecursiveType. When any error is returned, no flags have been added.
//
// # Field discovery
//
// A field becomes a flag only when it carries a "flag" struct tag. Untagged
// fields are invisible, including named struct subtrees. An embedded struct is
// inlined, matching Go's own field promotion.
//
//	flag:""        derived kebab-case name
//	flag:"name"    long name only
//	flag:"name,p"  long name with shorthand
//	flag:",p"      derived name with shorthand
//
// A "usage" tag supplies help text. A field tagged `strata:"...,secret"` or
// `env:"...,secret"` never publishes its default through framework help output.
//
// # Defaults
//
// Detached storage is seeded from the current state of cfg. A caller that wants
// configuration defaults to appear in --help should apply them before calling
// RegisterFlags, for example by calling SetDefaults on cfg first.
//
// A field tagged as secret is the exception: its storage starts at the zero
// value, and the configured secret never reaches pflag's default rendering. The
// real value is written into storage by Apply, which the documented lifecycle
// runs before any application code reads a flag.
//
// # Manual flags
//
// Registration is additive. A flag registered by hand on cmd is never inspected
// for behavior and never modified. The only rejection is a direct collision on a
// name or a shorthand, reported as ErrFlagConflict.
func RegisterFlags(cmd *cobra.Command, cfg any, opts ...FlagOption) error {
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

	destination := cmd.Flags()
	if options.persistent {
		destination = cmd.PersistentFlags()
	}

	pending, err := prepareFlags(targets, root)
	if err != nil {
		return err
	}

	if err := validateFlags(cmd, destination, pending); err != nil {
		return err
	}

	for i := range pending {
		if err := bindFlag(destination, &pending[i]); err != nil {
			return err
		}
	}

	return nil
}

func resolveFlagOptions(opts []FlagOption) *flagConfig {
	config := &flagConfig{persistent: false, metadata: nil}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(config)
	}

	return config
}

func structTarget(cfg any) (reflect.Value, error) {
	if cfg == nil {
		return reflect.Value{}, fmt.Errorf("%w: cfg is nil", ErrNotStruct)
	}

	val := reflect.ValueOf(cfg)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return reflect.Value{}, fmt.Errorf("%w: got %T", ErrNotStruct, cfg)
	}

	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("%w: %s is not a struct", ErrNotStruct, elem.Type())
	}

	return elem, nil
}

func prepareFlags(targets []plan.Target, root reflect.Value) ([]pendingFlag, error) {
	pending := make([]pendingFlag, 0, len(targets))

	for i := range targets {
		target := &targets[i]
		storage := reflect.New(plan.StorageTypeFor(target.Kind, target.LeafType))

		if !target.Secret {
			if err := seedStorage(storage, root, target); err != nil {
				return nil, err
			}
		}

		pending = append(pending, pendingFlag{target: targets[i], storage: storage})
	}

	return pending, nil
}

func seedStorage(storage reflect.Value, root reflect.Value, target *plan.Target) error {
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

	if err := encodeLeaf(storage.Elem(), source, target.Kind); err != nil {
		return fmt.Errorf("seed %s: %w", target.Display, err)
	}

	return nil
}

func encodeLeaf(dst, src reflect.Value, kind plan.Kind) error {
	//nolint:exhaustive // plan.KindUnsupported never reaches storage seeding
	switch kind {
	case plan.KindText:
		text, err := marshalLeaf(src)
		if err != nil {
			return err
		}

		dst.SetString(text)

		return nil
	case plan.KindString:
		dst.SetString(src.String())

		return nil
	case plan.KindBool:
		dst.SetBool(src.Bool())

		return nil
	case plan.KindInt, plan.KindInt64, plan.KindDuration:
		dst.SetInt(src.Int())

		return nil
	case plan.KindUint, plan.KindUint64:
		dst.SetUint(src.Uint())

		return nil
	case plan.KindFloat32, plan.KindFloat64:
		dst.SetFloat(src.Float())

		return nil
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return cloneSlice(dst, src)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, src.Type())
	}
}

func cloneSlice(dst, src reflect.Value) error {
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
			return fmt.Errorf("%w: %s into %s", ErrUnsupportedFieldType, element.Type(), targetType)
		}
	}

	dst.Set(out)

	return nil
}

func marshalLeaf(src reflect.Value) (string, error) {
	if marshaller, ok := reflect.TypeAssert[encoding.TextMarshaler](src); ok {
		text, err := marshaller.MarshalText()
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
		}

		return string(text), nil
	}

	if src.CanAddr() {
		if marshaller, ok := reflect.TypeAssert[encoding.TextMarshaler](src.Addr()); ok {
			text, err := marshaller.MarshalText()
			if err != nil {
				return "", fmt.Errorf("marshal %s: %w", src.Type(), err)
			}

			return string(text), nil
		}
	}

	return fmt.Sprint(src.Interface()), nil
}

func validateFlags(cmd *cobra.Command, destination *pflag.FlagSet, pending []pendingFlag) error {
	if err := validateUnique(pending); err != nil {
		return err
	}

	return validateAvailable(cmd, destination, pending)
}

func validateUnique(pending []pendingFlag) error {
	names := make(map[string]string, len(pending))
	shorthands := make(map[string]string, len(pending))

	for i := range pending {
		target := &pending[i].target

		if previous, duplicate := names[target.Name]; duplicate {
			return fmt.Errorf("%w: %q is claimed by %s and %s", ErrDuplicateFlag, target.Name, previous, target.Display)
		}

		names[target.Name] = target.Display

		if target.Shorthand == "" {
			continue
		}

		if previous, duplicate := shorthands[target.Shorthand]; duplicate {
			return fmt.Errorf("%w: shorthand %q is claimed by %s and %s", ErrDuplicateFlag, target.Shorthand, previous, target.Display)
		}

		shorthands[target.Shorthand] = target.Display
	}

	return nil
}

func validateAvailable(cmd *cobra.Command, destination *pflag.FlagSet, pending []pendingFlag) error {
	for i := range pending {
		target := &pending[i].target

		if existing := lookupAnyFlag(cmd, destination, target.Name); existing != nil {
			return fmt.Errorf("%w: --%s is already registered (%s)", ErrFlagConflict, target.Name, target.Display)
		}

		if target.Shorthand == "" {
			continue
		}

		if existing := lookupAnyShorthand(cmd, destination, target.Shorthand); existing != nil {
			return fmt.Errorf("%w: -%s is already registered to --%s (%s)",
				ErrFlagConflict, target.Shorthand, existing.Name, target.Display)
		}
	}

	return nil
}

func lookupAnyFlag(cmd *cobra.Command, destination *pflag.FlagSet, name string) *pflag.Flag {
	if destination != nil {
		if f := destination.Lookup(name); f != nil {
			return f
		}
	}

	if cmd != nil {
		if f := cmd.Flags().Lookup(name); f != nil {
			return f
		}

		if f := cmd.PersistentFlags().Lookup(name); f != nil {
			return f
		}

		if f := cmd.InheritedFlags().Lookup(name); f != nil {
			return f
		}
	}

	return nil
}

func lookupAnyShorthand(cmd *cobra.Command, destination *pflag.FlagSet, shorthand string) *pflag.Flag {
	if destination != nil {
		if f := destination.ShorthandLookup(shorthand); f != nil {
			return f
		}
	}

	if cmd != nil {
		if f := cmd.Flags().ShorthandLookup(shorthand); f != nil {
			return f
		}

		if f := cmd.PersistentFlags().ShorthandLookup(shorthand); f != nil {
			return f
		}

		if f := cmd.InheritedFlags().ShorthandLookup(shorthand); f != nil {
			return f
		}
	}

	return nil
}

func bindFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	//nolint:exhaustive // plan.KindUnsupported is never produced by plan.Classify for a tagged field
	switch p.target.Kind {
	case plan.KindString, plan.KindText:
		return bindStringFlag(destination, p)
	case plan.KindBool:
		return bindBoolFlag(destination, p)
	case plan.KindInt, plan.KindInt64, plan.KindUint, plan.KindUint64, plan.KindFloat32, plan.KindFloat64, plan.KindDuration:
		return bindNumericFlag(destination, p)
	case plan.KindStringSlice, plan.KindIntSlice, plan.KindInt64Slice:
		return bindSliceFlag(destination, p)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}
}

func bindStringFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	pointer, ok := reflect.TypeAssert[*string](p.storage)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}

	destination.StringVarP(pointer, p.target.Name, p.target.Shorthand, *pointer, p.target.Usage)

	return nil
}

func bindBoolFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	pointer, ok := reflect.TypeAssert[*bool](p.storage)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
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
		destination.DurationVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}

	return nil
}

func bindSliceFlag(destination *pflag.FlagSet, p *pendingFlag) error {
	switch value := p.storage.Interface().(type) {
	case *[]string:
		destination.StringSliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *[]int:
		destination.IntSliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	case *[]int64:
		destination.Int64SliceVarP(value, p.target.Name, p.target.Shorthand, *value, p.target.Usage)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}

	return nil
}
