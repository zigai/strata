package strataurfave

import (
	"fmt"
	"reflect"
	"time"

	"github.com/urfave/cli/v3"

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
	//
	// No flags are generated when this error is returned.
	//
	// The error value is declared by internal/plan, which reports the condition.
	ErrDuplicateFlag = plan.ErrDuplicateFlag

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
	// is reported here when a CLI value is written back into the field, and when
	// Apply writes such a slice into a generated flag. The seeding path reports the
	// same condition as ErrUnsupportedFieldType.
	//
	// The error value is declared by internal/plan, which reports the condition.
	ErrValueOverflow = plan.ErrValueOverflow
)

// flagConfig carries the options accepted by GenerateFlags, RegisterFlags, and
// SyncFlagsToStruct.
type flagConfig struct {
	metadata *strata.Metadata
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

// textValue adapts the detached storage of a text codec field to cli.Value.
// urfave/cli represents a text codec with a generic flag, and a generic flag
// delegates parsing to a cli.Value, not to a typed constructor.
type textValue struct {
	text *string
}

// WithMetadata makes SyncFlagsToStruct record the origin of every flag value
// that becomes the winning configuration for its key. A field tagged as secret is
// recorded with a redacted raw value.
//
// The option affects SyncFlagsToStruct only; generation and registration do not
// report provenance.
func WithMetadata(meta *strata.Metadata) FlagOption {
	return func(config *flagConfig) {
		config.metadata = meta
	}
}

// GenerateFlags discovers CLI flags from the exported fields of cfg and returns
// them with detached storage, without touching a command.
//
// Discovery, seeding, and validation failures are reported as ErrNotStruct,
// ErrInvalidTag, ErrDuplicateFlag, ErrUnsupportedFieldType, or ErrRecursiveType.
// When an error is returned, no flags are produced.
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
// GenerateFlags, for example by calling SetDefaults on cfg first.
//
// # Validation
//
// Every generated name and shorthand is checked for uniqueness before any flag is
// built. Options are accepted for symmetry with RegisterFlags and
// SyncFlagsToStruct; none of them affect generation.
func GenerateFlags(cfg any, opts ...FlagOption) ([]cli.Flag, error) {
	root, err := plan.StructTarget(cfg)
	if err != nil {
		return nil, err
	}

	targets, err := plan.Build(root.Type(), plan.Registration)
	if err != nil {
		return nil, fmt.Errorf("build flag plan: %w", err)
	}

	pending, err := prepareFlags(targets, root)
	if err != nil {
		return nil, err
	}

	if err := plan.ValidateUniqueTargets(targets); err != nil {
		return nil, err
	}

	flags := make([]cli.Flag, 0, len(pending))

	for i := range pending {
		flag, err := bindFlag(&pending[i])
		if err != nil {
			return nil, err
		}

		flags = append(flags, flag)
	}

	return flags, nil
}

// RegisterFlags discovers CLI flags from the exported fields of cfg and appends
// them to cmd.Flags with detached storage.
//
// Registration is additive. A flag already present on cmd is never inspected,
// modified, or removed. urfave/cli resolves a name registered twice through its
// own lookup order, so the caller should not hand-register a name that a tagged
// field also claims.
//
// A duplicate inside the generated set is reported as ErrDuplicateFlag. When any
// error is returned, no flags have been added.
//
// See GenerateFlags for the field-discovery rules and the remaining errors.
func RegisterFlags(cmd *cli.Command, cfg any, opts ...FlagOption) error {
	if cmd == nil {
		return nil
	}

	flags, err := GenerateFlags(cfg, opts...)
	if err != nil {
		return err
	}

	cmd.Flags = append(cmd.Flags, flags...)

	return nil
}

// Set stores text as the adapted value.
func (v *textValue) Set(text string) error {
	*v.text = text

	return nil
}

// String reports the adapted text.
func (v *textValue) String() string {
	return *v.text
}

// Get reports the adapted text in the form urfave/cli's typed accessors read.
func (v *textValue) Get() any {
	return *v.text
}

func resolveFlagOptions(opts []FlagOption) *flagConfig {
	config := &flagConfig{metadata: nil}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(config)
	}

	return config
}

func prepareFlags(targets []plan.Target, root reflect.Value) ([]pendingFlag, error) {
	pending := make([]pendingFlag, 0, len(targets))

	for i := range targets {
		target := &targets[i]
		storage := reflect.New(plan.StorageTypeFor(target.Kind, target.LeafType))

		// A secret field is seeded like any other field, and the flag suppresses
		// the value in help output through HideDefault. The value is not
		// discarded.
		if err := plan.SeedStorage(storage, root, target); err != nil {
			return nil, err
		}

		pending = append(pending, pendingFlag{target: targets[i], storage: storage})
	}

	return pending, nil
}

func bindFlag(p *pendingFlag) (cli.Flag, error) {
	//nolint:exhaustive // plan.KindUnsupported is never produced by plan.Classify for a tagged field
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
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}
}

// bindTextFlag registers a text codec field as a generic flag. The adapter holds
// the raw text, and decoding it into the field is the synchronization's job.
func bindTextFlag(p *pendingFlag) (cli.Flag, error) {
	storage, ok := reflect.TypeAssert[*string](p.storage)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
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
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
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
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
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

// bindNumericFlag registers a numeric field. A narrow integer was widened into
// the storage type plan.StorageTypeFor reports; the write-back path narrows it
// again with a range check.
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
		return &cli.DurationFlag{
			Name:        p.target.Name,
			Aliases:     aliases,
			Usage:       usage,
			Value:       *storage,
			Destination: storage,
			HideDefault: hidden,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}
}

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
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFieldType, p.target.Display)
	}
}

// flagAliases turns a tag shorthand into urfave/cli's alias list. urfave/cli has
// no separate shorthand concept: an alias matches exactly as a name does.
func flagAliases(target *plan.Target) []string {
	if target.Shorthand == "" {
		return nil
	}

	return []string{target.Shorthand}
}
