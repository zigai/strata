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

// builtinFormats is the registry of formats this package writes on its own: the
// TOML, YAML, and JSON codecs [codec.NewRegistry] serves. It is never mutated,
// so a shared instance is safe.
var builtinFormats = codec.NewRegistry()

// Codec defines the decoding and encoding operations for one configuration
// format. It is an alias for [codec.Codec].
type Codec = codec.Codec

// Option configures the behavior of [Load] and [LoadInto]. An Option is
// applied once per call and is not retained.
type Option func(*loadOptions)

type loadOptions struct {
	appName       string
	envPrefix     string
	explicitPath  string
	cwd           string
	stdinReader   io.Reader
	codecReg      *codec.Registry
	defaultsFunc  func(any) error
	maxFileSize   int64
	withoutFiles  bool
	formats       []string
	formatsSet    bool
	formatAliases map[string]string
	excludedExts  map[string]bool
}

// DecoderFunc decodes data into a target struct.
//
// It matches the standard library unmarshaler signature used by packages
// such as encoding/json, yaml, and toml.
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
// When unset, no environment variable is bound.
func WithEnvPrefix(prefix string) Option {
	return func(o *loadOptions) {
		o.envPrefix = prefix
	}
}

// WithExplicitPath names one file to load and skips tier discovery.
//
// The path "-" reads from standard input instead. The process standard input is
// read once and buffered for the lifetime of the process, so a later load sees
// the same bytes rather than an exhausted stream.
//
// NB: A reader supplied through [WithStdin] is not cached. It is consumed
// directly, so a second load over an exhausted reader produces defaults without
// reporting an error.
func WithExplicitPath(path string) Option {
	return func(o *loadOptions) {
		o.explicitPath = path
	}
}

// WithoutFiles disables tier discovery, leaving only the environment and
// defaults.
//
// A path set through [WithExplicitPath] still applies. Naming one file is a more
// specific instruction than turning discovery off.
func WithoutFiles() Option {
	return func(o *loadOptions) {
		o.withoutFiles = true
	}
}

// WithStdin supplies the reader used by WithExplicitPath("-").
//
// The process standard input is used when this is unset.
func WithStdin(r io.Reader) Option {
	return func(o *loadOptions) {
		o.stdinReader = r
	}
}

// WithCWD sets the directory searched for project-tier configuration.
//
// The process working directory is used when this is unset.
func WithCWD(cwd string) Option {
	return func(o *loadOptions) {
		o.cwd = cwd
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

// WithDefaults copies an explicit defaults value into the target before any
// layer is read.
//
// The type argument MUST match the type passed to [Load] or [LoadInto]. A
// mismatch is silently ignored rather than reported, so pairing
// WithDefaults[Config] with Load[configView] is a no-op that appears to succeed.
// Convert when a loading view is in use:
//
//	strata.WithDefaults(ConfigView(defaults))
//
// Values supplied here rank below [Defaulter.SetDefaults], below every
// configuration file, and below the environment.
func WithDefaults[T any](defaults T) Option {
	return func(o *loadOptions) {
		o.defaultsFunc = func(target any) error {
			if ptr, ok := target.(*T); ok {
				*ptr = defaults
			}

			return nil
		}
	}
}

// WithCodec registers a codec for a file extension on this load only.
//
// Registration affects both discovery and decoding. The extension joins the
// candidate list, so a project tier can be satisfied by a .json5 file once that
// codec is registered. An extension the registry already serves is replaced for
// this load.
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

// WithFormats restricts configuration file discovery and decoding to the
// specified formats or extensions.
//
// Formats can be specified by format name (e.g. "toml", "yaml", "json") or by
// file extension (e.g. ".toml", ".yaml", ".yml", ".json"). Passing "yaml"
// enables both .yaml and .yml extensions in that order, while passing ".yaml" or
// ".yml" enables only that specific extension.
//
// The order of arguments determines auto-discovery priority across all file
// tiers. Formats not specified are neither discovered nor decoded.
//
// If a format has no registered codec, the load fails with an error wrapping
// [ErrNoCodec].
func WithFormats(formats ...string) Option {
	return func(o *loadOptions) {
		o.formats = append([]string(nil), formats...)
		o.formatsSet = true
	}
}

// WithFormatAlias registers a file extension as an alias for an existing format.
//
// Discovery will search for files matching aliasExt and decode them using the
// target format's codec. When recording provenance, strata analyzes the file
// using the target format's syntax.
//
// For example, WithFormatAlias(".conf", "toml") searches for .conf files,
// decodes them using TOML, and populates provenance via the TOML reader.
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
// It accepts any function matching the standard func(data []byte, v any) error
// signature, allowing third-party parsers (e.g. json5, hcl) to be registered
// without implementing the [Codec] interface.
//
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
// The target parameter passed to fn is statically typed as *T, eliminating
// runtime type assertions for custom parsers.
//
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

// formatName reports the display name of a built-in extension.
func formatName(ext string) string {
	if ext == ".yml" {
		return "yaml"
	}

	return strings.TrimPrefix(ext, ".")
}

// defaultLoadOptions returns the option set a load starts from when the caller
// passes no options.
func defaultLoadOptions() *loadOptions {
	return &loadOptions{
		appName:       "",
		envPrefix:     "",
		explicitPath:  "",
		cwd:           "",
		stdinReader:   os.Stdin,
		codecReg:      codec.NewRegistry(),
		defaultsFunc:  nil,
		maxFileSize:   stream.DefaultMaxFileSize,
		withoutFiles:  false,
		formats:       nil,
		formatsSet:    false,
		formatAliases: nil,
		excludedExts:  nil,
	}
}
