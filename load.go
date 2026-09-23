package strata

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/cascade"
	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/env"
	"github.com/zigai/strata/internal/origins"
	"github.com/zigai/strata/internal/stream"
)

// Load reads every configured layer into a new T.
//
// Finding no system or user file is not an error; defaults and environment
// still apply and the call succeeds. A missing [WithPath] file, an unreadable,
// malformed, or over-sized file, and a validation failure are errors, and on
// error the zero T is returned.
//
// Use [LoadWithMetadata] to also learn where each value came from.
func Load[T any](opts ...Option) (T, error) {
	target, _, err := LoadWithMetadata[T](opts...)

	return target, err
}

// LoadWithMetadata is [Load] that also returns the provenance of each resolved
// key, the files that contributed, and any unknown keys the files set.
func LoadWithMetadata[T any](opts ...Option) (T, *Metadata, error) {
	var target T

	meta, err := LoadInto(&target, opts...)
	if err != nil {
		var zero T

		return zero, nil, err
	}

	return target, meta, nil
}

// LoadInto merges every configured tier into the struct that target points at,
// and returns the provenance of each resolved key.
//
// Unlike [Load], the starting point is the value target already holds, so a
// caller can seed state before loading. A key that no tier mentions keeps the
// value it started with, which is what lets a documented default survive a
// sparse file.
//
// target MUST be a non-nil pointer to a struct; anything else returns
// [ErrTargetNotPointer]. Validation runs at the end of the merge.
func LoadInto[T any](target *T, opts ...Option) (*Metadata, error) {
	if target == nil {
		return nil, ErrTargetNotPointer
	}

	options := defaultLoadOptions()
	for _, opt := range opts {
		opt(options)
	}

	if err := prepareRegistry(options); err != nil {
		return nil, err
	}

	options.keys = keyTreeFor(reflect.TypeFor[T]())

	meta := NewMetadata()
	registerSecretsFromTarget(target, meta)

	if err := applyDefaults(target, options, meta); err != nil {
		return nil, err
	}

	if err := discoverAndApplyLayers(target, options, meta); err != nil {
		return nil, err
	}

	if options.strict {
		if err := unknownKeysError(meta); err != nil {
			return nil, err
		}
	}

	err := env.Apply(target, env.Options{
		Prefix: options.envPrefix,
		Lookup: os.LookupEnv,
		OnBind: func(key, envVar, rawVal string) {
			meta.Record(Origin{
				Key:      key,
				Source:   SourceEnv,
				Path:     envVar,
				Line:     0,
				RawValue: rawVal,
			})
		},
		ParseDuration: ParseDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("bind environment variables: %w", err)
	}

	for _, contrib := range options.contributions {
		if err := contrib(target, meta); err != nil {
			return nil, err
		}
	}

	if vErr := runValidation(target, meta); vErr != nil {
		return nil, vErr
	}

	return meta, nil
}

// applyDefaults runs the struct's SetDefaults, then the value given to
// WithDefaults, and records the result as the default layer.
func applyDefaults(target any, opts *loadOptions, meta *Metadata) error {
	recordDefault := func(key, rawVal string) {
		meta.Record(Origin{
			Key:      key,
			Source:   SourceDefault,
			Path:     "",
			Line:     0,
			RawValue: rawVal,
		})
	}

	if opts.defaultsFunc == nil {
		if err := defaulter.Apply(target, recordDefault); err != nil {
			return fmt.Errorf("apply defaults: %w", err)
		}

		return nil
	}

	if err := defaulter.Apply(target, nil); err != nil {
		return fmt.Errorf("apply defaults: %w", err)
	}

	if err := opts.defaultsFunc(target); err != nil {
		return err
	}

	if err := defaulter.Report(target, recordDefault); err != nil {
		return fmt.Errorf("apply defaults: %w", err)
	}

	return nil
}

func runValidation(target any, meta *Metadata) error {
	if v, ok := target.(MetadataValidator); ok {
		return validateWithMetadata(v, meta)
	}

	if v, ok := target.(Validator); ok {
		if err := v.Validate(); err != nil {
			return meta.NewConfigError("", fmt.Errorf("validation failed: %w", err))
		}
	}

	return nil
}

func validateWithMetadata(v MetadataValidator, meta *Metadata) error {
	err := v.ValidateWith(meta)
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*ConfigError](err); ok {
		//nolint:wrapcheck // already a ConfigError carrying its own origin; wrapping would nest it
		return err
	}

	return meta.NewConfigError("", fmt.Errorf("validation failed: %w", err))
}

func readLayerData(layer cascade.Layer, maxFileSize int64) ([]byte, error) {
	if layer.Data != nil {
		return layer.Data, nil
	}

	f, oErr := os.Open(layer.Path)
	if oErr != nil {
		return nil, fmt.Errorf("open file %s: %w", layer.Path, oErr)
	}

	var closed bool

	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	data, rErr := stream.ReadBounded(f, maxFileSize)
	if rErr != nil {
		return nil, fmt.Errorf("read file %s: %w", layer.Path, rErr)
	}

	closed = true

	if cErr := f.Close(); cErr != nil {
		return nil, fmt.Errorf("close file %s: %w", layer.Path, cErr)
	}

	return data, nil
}

func resolveLayerCodec(data []byte, layer cascade.Layer, opts *loadOptions) (Codec, string, error) {
	ext := normalizeExt(filepath.Ext(layer.Path))

	if ext != "" && opts.excludedExts != nil && opts.excludedExts[ext] {
		return nil, "", fmt.Errorf("%w for layer %s", ErrNoCodec, layer.Path)
	}

	codecInstance, ok := opts.codecReg.Get(ext)
	syntaxExt := ext

	if !ok || ext == "" {
		codecInstance, syntaxExt = detectCodec(data, opts)
	}

	if codecInstance == nil {
		return nil, "", fmt.Errorf("%w for layer %s", ErrNoCodec, layer.Path)
	}

	return codecInstance, syntaxExt, nil
}

func applyLayer(target any, layer cascade.Layer, opts *loadOptions, meta *Metadata) error {
	data, err := readLayerData(layer, opts.maxFileSize)
	if err != nil {
		return err
	}

	codecInstance, syntaxExt, err := resolveLayerCodec(data, layer, opts)
	if err != nil {
		return err
	}

	if err := codecInstance.Decode(data, target); err != nil {
		return fmt.Errorf("decode layer %s: %w", layer.Path, err)
	}

	meta.AddActiveFile(layer.Path)

	if aliasTarget, ok := opts.formatAliases[syntaxExt]; ok {
		syntaxExt = aliasTarget
	}

	recordLayerOrigins(data, layer, syntaxExt, opts.keys, meta)

	return nil
}

func detectEmptyCodec(reg *codec.Registry) (Codec, string) {
	if c, ok := reg.Get(".toml"); ok {
		return c, ".toml"
	}

	exts := reg.Extensions()
	if len(exts) > 0 {
		c, _ := reg.Get(exts[0])
		return c, exts[0]
	}

	return nil, ""
}

func tryDecodeCodec(reg *codec.Registry, ext string, data []byte) (Codec, string, bool) {
	if c, ok := reg.Get(ext); ok {
		var dummy any
		if err := c.Decode(data, &dummy); err == nil {
			return c, ext, true
		}
	}

	return nil, "", false
}

func detectOtherCodec(reg *codec.Registry, data []byte) (Codec, string) {
	for _, ext := range reg.Extensions() {
		if ext == ".json" || ext == ".toml" || ext == ".yaml" || ext == ".yml" {
			continue
		}

		if c, ext, ok := tryDecodeCodec(reg, ext, data); ok {
			return c, ext
		}
	}

	return nil, ""
}

func detectCodec(data []byte, opts *loadOptions) (Codec, string) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return detectEmptyCodec(opts.codecReg)
	}

	if trimmed[0] == '{' || trimmed[0] == '[' {
		if c, ext, ok := tryDecodeCodec(opts.codecReg, ".json", data); ok {
			return c, ext
		}
	}

	if c, ext, ok := tryDecodeCodec(opts.codecReg, ".toml", data); ok {
		return c, ext
	}

	if c, ok := opts.codecReg.Get(".yaml"); ok {
		return c, ".yaml"
	}

	if c, ok := opts.codecReg.Get(".yml"); ok {
		return c, ".yml"
	}

	return detectOtherCodec(opts.codecReg, data)
}

func applyFormats(formats []string, reg *codec.Registry) ([]string, error) {
	var resolved []string

	seen := make(map[string]struct{}, len(formats))
	add := func(ext, raw string) error {
		if _, ok := reg.Get(ext); !ok {
			return fmt.Errorf("%w: unknown format %q", ErrNoCodec, raw)
		}

		if _, ok := seen[ext]; !ok {
			seen[ext] = struct{}{}
			resolved = append(resolved, ext)
		}

		return nil
	}

	for _, f := range formats {
		trimmed := strings.TrimSpace(f)
		if trimmed == "" {
			continue
		}

		if strings.EqualFold(trimmed, "yaml") {
			if err := add(".yaml", f); err != nil {
				return nil, err
			}

			if _, ok := reg.Get(".yml"); ok {
				_ = add(".yml", f)
			}

			continue
		}

		if err := add(normalizeExt(trimmed), f); err != nil {
			return nil, err
		}
	}

	return resolved, nil
}

func resolveFormatAliases(opts *loadOptions) error {
	for alias, target := range opts.formatAliases {
		resolvedTarget := target

		for range len(opts.formatAliases) {
			if next, ok := opts.formatAliases[resolvedTarget]; ok {
				resolvedTarget = next
			} else {
				break
			}
		}

		targetCodec, ok := opts.codecReg.Get(resolvedTarget)
		if !ok {
			return fmt.Errorf("%w for format alias target %q", ErrNoCodec, target)
		}

		opts.codecReg.Register(alias, targetCodec)
	}

	return nil
}

func prepareRegistry(opts *loadOptions) error {
	if len(opts.formatAliases) > 0 {
		if err := resolveFormatAliases(opts); err != nil {
			return err
		}
	}

	if opts.formatsSet {
		allExts := opts.codecReg.Extensions()

		resolvedExts, err := applyFormats(opts.formats, opts.codecReg)
		if err != nil {
			return err
		}

		opts.codecReg.Restrict(resolvedExts...)

		opts.excludedExts = make(map[string]bool)

		for _, std := range []string{".toml", ".yaml", ".yml", ".json"} {
			if !slices.Contains(resolvedExts, std) {
				opts.excludedExts[std] = true
			}
		}

		for _, e := range allExts {
			if !slices.Contains(resolvedExts, e) {
				opts.excludedExts[e] = true
			}
		}
	}

	return nil
}

func discoverAndApplyLayers(target any, opts *loadOptions, meta *Metadata) error {
	layers, err := cascade.Discover(cascade.Params{
		AppName:      opts.appName,
		ExplicitPath: opts.explicitPath,
		OptionalPath: opts.optionalPath,
		WithoutFiles: opts.withoutFiles,
		StdinReader:  opts.stdinReader,
		MaxFileSize:  opts.maxFileSize,
		Extensions:   opts.codecReg.Extensions(),
	})
	if err != nil {
		return fmt.Errorf("discover configuration layers: %w", err)
	}

	for _, layer := range layers {
		if err := applyLayer(target, layer, opts, meta); err != nil {
			return fmt.Errorf("apply configuration layer %s (%s): %w", layer.Path, layer.Source, err)
		}
	}

	return nil
}

func recordLayerOrigins(data []byte, layer cascade.Layer, ext string, keys *keyTree, meta *Metadata) {
	origins.Read(data, ext, func(record origins.Record) {
		origin := Origin{
			Key:      record.Key,
			Source:   SourceKind(layer.Source),
			Path:     layer.Path,
			Line:     record.Line,
			RawValue: record.RawValue,
		}

		if keys.known(record.Key) {
			meta.Record(origin)
			return
		}

		origin.RawValue = ""
		meta.recordUnknown(UnknownKey{Origin: origin, Suggestion: keys.suggest(record.Key)})
	})
}

// unknownKeysError reports every unknown key as a [ConfigError] wrapping
// [ErrUnknownKey], or nil when there are none.
func unknownKeysError(meta *Metadata) error {
	unknown := meta.UnknownKeys()
	if len(unknown) == 0 {
		return nil
	}

	errs := make([]error, 0, len(unknown))

	for _, key := range unknown {
		cause := fmt.Errorf("%w %q", ErrUnknownKey, key.Key)
		if key.Suggestion != "" {
			cause = fmt.Errorf("%w %q (did you mean %q?)", ErrUnknownKey, key.Key, key.Suggestion)
		}

		errs = append(errs, &ConfigError{Key: key.Key, Err: cause, Origin: key.Origin})
	}

	return errors.Join(errs...)
}

func registerSecretsFromTarget(target any, meta *Metadata) {
	if target == nil || meta == nil {
		return
	}

	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return
		}

		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return
	}

	walkRegisterSecrets(val.Type(), "", false, meta, make(map[reflect.Type]bool))
}

func fullSecretFieldKey(prefix, key string, anonymous bool) string {
	if anonymous {
		return prefix
	}

	if prefix != "" {
		return prefix + "." + key
	}

	return key
}

func registerFieldSecret(f reflect.StructField, prefix string, inheritedSecret bool, meta *Metadata, visited map[reflect.Type]bool) {
	if !f.IsExported() && !f.Anonymous {
		return
	}

	key := defaulter.FieldKey(f)
	if key == "-" {
		return
	}

	fieldSecret := inheritedSecret || defaulter.IsSecret(f)
	fullKey := fullSecretFieldKey(prefix, key, f.Anonymous)

	ft := f.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}

	if defaulter.IsNestedStructType(ft) {
		walkRegisterSecrets(ft, fullKey, fieldSecret, meta, visited)
		return
	}

	if fieldSecret && fullKey != "" {
		meta.RecordSecret(fullKey)
	}
}

func walkRegisterSecrets(typ reflect.Type, prefix string, inheritedSecret bool, meta *Metadata, visited map[reflect.Type]bool) {
	if typ == nil || visited[typ] {
		return
	}

	visited[typ] = true
	defer delete(visited, typ)

	for f := range typ.Fields() {
		registerFieldSecret(f, prefix, inheritedSecret, meta, visited)
	}
}
