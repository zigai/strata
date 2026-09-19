package strata

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/strata/codec"
	"github.com/zigai/strata/internal/cascade"
	"github.com/zigai/strata/internal/defaulter"
	"github.com/zigai/strata/internal/env"
	"github.com/zigai/strata/internal/origins"
	"github.com/zigai/strata/internal/stream"
)

// Load reads every configured tier into a new T, and returns it with the
// provenance of each resolved key.
//
// Tiers are applied in ascending precedence: struct defaults, system file, user
// file, project file, environment, and finally the CLI overlay applied by the
// bridge packages. A document overlays the result so far rather than replacing
// it, so a sparse file changes only the keys it mentions.
//
// Finding no configuration file is not an error. Defaults and environment still
// apply and the call succeeds. An unreadable, malformed, or over-sized file is
// an error, as is a validation failure.
//
// On error the returned T is not the zero value. Defaults are applied before any
// file is read, so a load that fails at a later tier leaves a fully defaulted
// value. Callers MUST check the error before reading T, or use [LoadInto] when
// the partially merged state is wanted.
func Load[T any](opts ...Option) (T, *Metadata, error) {
	var target T

	meta, err := LoadInto(&target, opts...)
	if err != nil {
		return target, nil, err
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

	meta := NewMetadata()

	if options.defaultsFunc != nil {
		if err := options.defaultsFunc(target); err != nil {
			return nil, fmt.Errorf("apply defaults instance: %w", err)
		}
	}

	// 1. Apply defaults declared by the Defaulter interface. These rank below
	//    every other tier.
	err := defaulter.Apply(target, func(key, rawVal string) {
		meta.Record(Origin{
			Key:      key,
			Source:   SourceDefault,
			Path:     "",
			Line:     0,
			RawValue: rawVal,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("apply defaults: %w", err)
	}

	// 2. Discover and merge configuration files, in ascending precedence.
	if err := discoverAndApplyLayers(target, options, meta); err != nil {
		return nil, err
	}

	// 3. Bind environment variables. These rank above every file.
	err = env.Apply(target, env.Options{
		Prefix: options.envPrefix,
		Lookup: options.envLookup,
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
	// 4. Validate the merged result.
	if vErr := runValidation(target, meta); vErr != nil {
		return nil, vErr
	}

	return meta, nil
}

// runValidation calls whichever validation interface target implements.
// [MetadataValidator] can attribute a failure to a key, so it takes precedence
// when a type implements both.
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

// validateWithMetadata calls a MetadataValidator and normalizes its failure.
//
// A failure already carrying an attributed key is returned as built, so its
// origin survives. A failure that names no key is reported the way a [Validator]
// failure is. Every validation failure therefore leaves [LoadInto] as a
// [ConfigError], which lets a caller classify one with [errors.As] without
// knowing which interface produced it.
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

// applyLayer merges one discovered layer into target and records the origin of
// each key it supplies. A layer that carries its data inline, as the explicit
// path and standard input do, is not read from disk.
func applyLayer(target any, layer cascade.Layer, opts *loadOptions, meta *Metadata) (err error) {
	data := layer.Data

	if data == nil {
		f, oErr := os.Open(layer.Path)
		if oErr != nil {
			return fmt.Errorf("open file %s: %w", layer.Path, oErr)
		}

		defer func() {
			if cErr := f.Close(); cErr != nil && err == nil {
				err = fmt.Errorf("close file %s: %w", layer.Path, cErr)
			}
		}()

		var rErr error

		data, rErr = stream.ReadBounded(f, opts.maxFileSize)
		if rErr != nil {
			return fmt.Errorf("read file %s: %w", layer.Path, rErr)
		}
	}

	ext := filepath.Ext(layer.Path)

	codecInstance, ok := opts.codecReg.Get(ext)
	if !ok || ext == "" {
		codecInstance = detectCodec(data, opts)
	}

	if codecInstance == nil {
		return fmt.Errorf("%w for layer %s", ErrNoCodec, layer.Path)
	}

	if err := codecInstance.Decode(data, target); err != nil {
		return fmt.Errorf("decode layer %s: %w", layer.Path, err)
	}

	meta.AddActiveFile(layer.Path)

	syntaxExt := ext
	if aliasTarget, ok := opts.formatAliases[ext]; ok {
		syntaxExt = aliasTarget
	}

	recordLayerOrigins(data, layer, syntaxExt, meta)

	return nil
}

// detectCodec selects a codec from the shape of the data, for a layer whose
// extension is absent or unregistered. An empty layer is TOML, a leading brace
// or bracket selects JSON, a layer that decodes as TOML is TOML, and anything
// else is YAML. It returns nil when the registry holds none of those.
func detectCodec(data []byte, opts *loadOptions) Codec {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		if c, ok := opts.codecReg.Get(".toml"); ok {
			return c
		}

		exts := opts.codecReg.Extensions()
		if len(exts) > 0 {
			c, _ := opts.codecReg.Get(exts[0])
			return c
		}

		return nil
	}

	if trimmed[0] == '{' || trimmed[0] == '[' {
		if c, ok := opts.codecReg.Get(".json"); ok {
			return c
		}
	}

	if c, ok := opts.codecReg.Get(".toml"); ok {
		var dummy any
		if err := c.Decode(data, &dummy); err == nil {
			return c
		}
	}

	if c, ok := opts.codecReg.Get(".yaml"); ok {
		return c
	}

	return nil
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

		lowered := strings.ToLower(trimmed)
		if lowered == "yaml" {
			if err := add(".yaml", f); err != nil {
				return nil, err
			}

			if _, ok := reg.Get(".yml"); ok {
				_ = add(".yml", f)
			}

			continue
		}

		if !strings.HasPrefix(lowered, ".") {
			lowered = "." + lowered
		}

		if err := add(lowered, f); err != nil {
			return nil, err
		}
	}

	return resolved, nil
}

func resolveFormatAliases(options *loadOptions) error {
	for alias, target := range options.formatAliases {
		resolvedTarget := target

		for range len(options.formatAliases) {
			if next, ok := options.formatAliases[resolvedTarget]; ok {
				resolvedTarget = next
			} else {
				break
			}
		}

		targetCodec, ok := options.codecReg.Get(resolvedTarget)
		if !ok {
			return fmt.Errorf("%w for format alias target %q", ErrNoCodec, target)
		}

		options.codecReg.Register(alias, targetCodec)
	}

	return nil
}

func prepareRegistry(options *loadOptions) error {
	if len(options.formatAliases) > 0 {
		if err := resolveFormatAliases(options); err != nil {
			return err
		}
	}

	if options.formatsSet {
		resolvedExts, err := applyFormats(options.formats, options.codecReg)
		if err != nil {
			return err
		}

		options.codecReg.Restrict(resolvedExts...)
	}

	return nil
}

func discoverAndApplyLayers(target any, options *loadOptions, meta *Metadata) error {
	layers, err := cascade.Discover(cascade.Params{
		AppName:      options.appName,
		ExplicitPath: options.explicitPath,
		CWD:          options.cwd,
		WithoutFiles: options.withoutFiles,
		StdinReader:  options.stdinReader,
		MaxFileSize:  options.maxFileSize,
		Extensions:   options.codecReg.Extensions(),
	})
	if err != nil {
		return fmt.Errorf("discover configuration layers: %w", err)
	}

	for _, layer := range layers {
		if err := applyLayer(target, layer, options, meta); err != nil {
			return fmt.Errorf("apply configuration layer %s (%s): %w", layer.Path, layer.Source, err)
		}
	}

	return nil
}

// recordLayerOrigins records one [Origin] per key the layer supplies.
//
// The layer is named explicitly rather than taken from the document, because a
// document carries no record of which tier it was discovered in.
func recordLayerOrigins(data []byte, layer cascade.Layer, ext string, meta *Metadata) {
	origins.Read(data, ext, func(record origins.Record) {
		meta.Record(Origin{
			Key:      record.Key,
			Source:   SourceKind(layer.Source),
			Path:     layer.Path,
			Line:     record.Line,
			RawValue: record.RawValue,
		})
	})
}
