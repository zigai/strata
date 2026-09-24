package strata

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/stream"
)

var builtinFormats = codec.NewRegistry()

type Codec = codec.Codec

type Option func(*loadOptions)

type loadOptions struct {
	appName       string
	envPrefix     string
	explicitPath  string
	optionalPath  bool
	stdinReader   io.Reader
	codecReg      *codec.Registry
	defaultsFunc  func(any) error
	maxFileSize   int64
	withoutFiles  bool
	formats       []string
	formatAliases map[string]string
	stdinExt      string
	contributions []Contribution
	strict        bool
	keys          *keyTree
}

type Contribution func(target any, meta *Metadata) error

type DecoderFunc func(data []byte, target any) error

type decoderCodec struct {
	decode DecoderFunc
}

type typedDecoderCodec[T any] struct {
	fn func([]byte, *T) error
}

// WithAppName sets the directory name searched under each configuration root,
// such as <XDG_CONFIG_HOME>/myapp/config.toml.
//
// Tier discovery is skipped entirely when this is unset. An application that
// relies only on an explicit path or the environment does not need it.
func WithAppName(name string) Option {
	return func(o *loadOptions) {
		o.appName = name
	}
}

// WithEnvPrefix sets the environment variable prefix, such as "MYAPP_".
//
// When unset, only fields whose env tag names a variable are bound. Derived
// names such as PORT are never read without a prefix, so an unrelated variable
// in the process environment cannot change the configuration.
func WithEnvPrefix(prefix string) Option {
	return func(o *loadOptions) {
		o.envPrefix = prefix
	}
}

// WithPath loads one file above the system and user files.
//
// Its extension must be enabled with [WithFormats]. Missing files are errors;
// use [WithOptionalPath] to allow one.
//
// The path "-" reads process stdin using the first enabled format. Process
// stdin is cached after the first read.
//
// A reader from [WithStdin] is consumed directly and is not cached.
func WithPath(path string) Option {
	return func(o *loadOptions) {
		o.explicitPath = path
		o.optionalPath = false
	}
}

// WithOptionalPath is [WithPath] for a file that may not exist. A missing file
// contributes nothing; a file that exists but cannot be read or parsed is still
// an error. Its extension must be enabled with [WithFormats]; "-" uses the
// first enabled format for stdin.
func WithOptionalPath(path string) Option {
	return func(o *loadOptions) {
		o.explicitPath = path
		o.optionalPath = true
	}
}

// WithoutFiles disables tier discovery, leaving only the environment and
// defaults.
//
// A path set through [WithPath] still applies. Naming one file is a more
// specific instruction than turning discovery off.
func WithoutFiles() Option {
	return func(o *loadOptions) {
		o.withoutFiles = true
	}
}

// WithStrict fails the load when a configuration file sets a key the target
// type does not declare.
//
// Each such key is reported as a [*ConfigError] wrapping [ErrUnknownKey], with
// the file it came from and, when one is close, the key that was probably
// meant. Without WithStrict the same keys are listed by
// [Metadata.UnknownKeys] and otherwise ignored.
func WithStrict() Option {
	return func(o *loadOptions) {
		o.strict = true
	}
}

// WithContribution registers a configuration provider that executes after
// defaults, configuration files, and environment variables have merged, but
// immediately before validation.
//
// Multiple contributions execute in registration order.
func WithContribution(c Contribution) Option {
	return func(o *loadOptions) {
		if c != nil {
			o.contributions = append(o.contributions, c)
		}
	}
}

// WithStdin supplies the reader used by WithPath("-").
//
// The process standard input is used when this is unset.
func WithStdin(r io.Reader) Option {
	return func(o *loadOptions) {
		o.stdinReader = r
	}
}

// WithMaxFileSize bounds the bytes one configuration file or stream may
// contribute.
//
// Input beyond the limit fails with [ErrFileTooLarge]. It is never truncated. A
// value of zero or less selects the default of 1 MiB.
func WithMaxFileSize(maxBytes int64) Option {
	return func(o *loadOptions) {
		o.maxFileSize = maxBytes
	}
}

// WithDefaults replaces the target with an explicit defaults value before any
// configuration file is read.
//
// It is applied after [Defaulter.SetDefaults], so it wins wherever both set a
// field, and every configuration file, the environment, and CLI flags rank
// above it. The value replaces the whole struct, including values a caller
// seeded before [LoadInto].
//
// The type argument MUST match the type passed to [Load] or [LoadInto]. A
// mismatch fails the load with [ErrDefaultsTypeMismatch].
func WithDefaults[T any](defaults T) Option {
	return func(o *loadOptions) {
		o.defaultsFunc = func(target any) error {
			ptr, ok := target.(*T)
			if !ok {
				return fmt.Errorf("%w: WithDefaults[%T] used to load %T", ErrDefaultsTypeMismatch, defaults, target)
			}

			*ptr = defaults

			return nil
		}
	}
}

// WithCodec registers a codec for a file extension on this load only.
//
// An extension the registry already serves is replaced for this load. The
// codec is used for reading only when its extension is listed in [WithFormats].
//
// c MUST NOT be nil; [codec.Registry.Register] panics if it is.
func WithCodec(ext string, c Codec) Option {
	return func(o *loadOptions) {
		if o.codecReg == nil {
			o.codecReg = codec.NewRegistry()
		}

		o.codecReg.Register(ext, c)
	}
}

// WithFormats enables configuration file discovery and decoding for the
// specified formats or extensions.
//
// Formats can be specified by format name (e.g. "toml", "yaml", "json") or by
// file extension (e.g. ".toml", ".yaml", ".yml", ".json"). Passing "yaml"
// enables both .yaml and .yml extensions in that order, while passing ".yaml" or
// ".yml" enables only that specific extension.
//
// The order of arguments determines auto-discovery priority across all file
// tiers and selects the first format for stdin and new user files. Formats not
// specified are neither discovered nor decoded. At least one format is required
// whenever files are read.
//
// If a format has no registered codec, the load fails with an error wrapping
// [ErrUnsupportedFormat]. An empty selection when files are read returns
// [ErrNoFormats].
func WithFormats(formats ...string) Option {
	return func(o *loadOptions) {
		o.formats = append([]string(nil), formats...)
	}
}

// WithFormatAlias registers a file extension as an alias for an existing format.
//
// Discovery will search for files matching aliasExt and decode them using the
// target format's codec. When recording provenance, strata analyzes the file
// using the target format's syntax. The alias is read only when aliasExt is
// listed in [WithFormats].
func WithFormatAlias(aliasExt, targetFormat string) Option {
	return func(o *loadOptions) {
		normAlias := normalizeExt(aliasExt)
		normTarget := normalizeExt(targetFormat)

		if normAlias == "" || normTarget == "" {
			return
		}

		if o.formatAliases == nil {
			o.formatAliases = make(map[string]string)
		}

		o.formatAliases[normAlias] = normTarget
	}
}

// WithDecoder registers a decoder function for a file extension on this load.
//
// It is used for reading only when the extension is listed in [WithFormats].
// fn MUST NOT be nil; WithDecoder panics if it is.
func WithDecoder(ext string, fn DecoderFunc) Option {
	if fn == nil {
		panic("strata: WithDecoder: nil DecoderFunc")
	}

	return func(o *loadOptions) {
		if o.codecReg == nil {
			o.codecReg = codec.NewRegistry()
		}

		o.codecReg.Register(ext, decoderCodec{decode: fn})
	}
}

// WithDecoderFunc registers a type-safe decoder function for a file extension on
// this load.
//
// It is used for reading only when the extension is listed in [WithFormats].
// fn MUST NOT be nil; WithDecoderFunc panics if it is.
func WithDecoderFunc[T any](ext string, fn func(data []byte, target *T) error) Option {
	if fn == nil {
		panic("strata: WithDecoderFunc: nil decoder function")
	}

	return func(o *loadOptions) {
		if o.codecReg == nil {
			o.codecReg = codec.NewRegistry()
		}

		o.codecReg.Register(ext, typedDecoderCodec[T]{fn: fn})
	}
}

func (d decoderCodec) Decode(data []byte, target any) error {
	if isNilTarget(target) {
		return codec.ErrNilTarget
	}

	if err := d.decode(data, target); err != nil {
		if errors.Is(err, codec.ErrMalformed) {
			return err
		}

		return fmt.Errorf("%w: %w", codec.ErrMalformed, err)
	}

	return nil
}

func (d decoderCodec) Encode(any) ([]byte, error) {
	return nil, fmt.Errorf("%w: encoder not provided for decoder-only codec", ErrUnsupportedFormat)
}

func (d typedDecoderCodec[T]) Decode(data []byte, target any) error {
	if target == nil {
		return codec.ErrNilTarget
	}

	ptr, ok := target.(*T)
	if !ok {
		return fmt.Errorf("%w: expected *%T, got %T", ErrCodecTargetMismatch, ptr, target)
	}

	if ptr == nil {
		return codec.ErrNilTarget
	}

	if err := d.fn(data, ptr); err != nil {
		if errors.Is(err, codec.ErrMalformed) {
			return err
		}

		return fmt.Errorf("%w: %w", codec.ErrMalformed, err)
	}

	return nil
}

func (d typedDecoderCodec[T]) Encode(any) ([]byte, error) {
	return nil, fmt.Errorf("%w: encoder not provided for decoder-only codec", ErrUnsupportedFormat)
}

func isNilTarget(target any) bool {
	if target == nil {
		return true
	}

	val := reflect.ValueOf(target)

	return val.Kind() == reflect.Pointer && val.IsNil()
}

func normalizeExt(ext string) string {
	trimmed := strings.ToLower(strings.TrimSpace(ext))
	if trimmed == "" {
		return ""
	}

	if !strings.HasPrefix(trimmed, ".") {
		return "." + trimmed
	}

	return trimmed
}

func formatName(ext string) string {
	if ext == ".yml" {
		return "yaml"
	}

	return strings.TrimPrefix(ext, ".")
}

func defaultLoadOptions() *loadOptions {
	return &loadOptions{
		appName:       "",
		envPrefix:     "",
		explicitPath:  "",
		optionalPath:  false,
		stdinReader:   os.Stdin,
		codecReg:      codec.NewRegistry(),
		defaultsFunc:  nil,
		maxFileSize:   stream.DefaultMaxFileSize,
		withoutFiles:  false,
		formats:       nil,
		formatAliases: nil,
		stdinExt:      "",
		contributions: nil,
		strict:        false,
		keys:          nil,
	}
}
