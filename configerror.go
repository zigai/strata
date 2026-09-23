package strata

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ConfigError reports a configuration failure together with the layer that
// supplied the offending value.
//
// When the failing key is known, the text form names the failure, the key, the
// raw input, and the responsible layer:
//
//	config error: port is above the privileged ceiling for port: "9090"
//	  --> set by /etc/demo.toml:2
//
// The key is known when a [MetadataValidator] reports the failure through
// [Metadata.NewConfigError]. A failure from a plain [Validator], or one that
// spans several fields, renders the message alone.
//
// It implements [errors.Unwrap], so the cause remains reachable with
// [errors.Is], [errors.As], and [errors.AsType].
type ConfigError struct {
	Key    string
	Err    error
	Origin Origin
}

// Error renders the failure. When the origin of the failing key is known, the
// rendering adds the key, the raw input, and the layer that supplied it.
func (e *ConfigError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config error: %v", e.Err)

	// An unknown-key failure already names the key, and carries no value.
	if e.Key != "" && !errors.Is(e.Err, ErrUnknownKey) {
		fmt.Fprintf(&b, " for %s", e.Key)
	}

	if e.Origin.RawValue != "" {
		fmt.Fprintf(&b, ": %q", e.Origin.RawValue)
	}

	if e.Origin.Key != "" || e.Origin.Source != "" {
		b.WriteString("\n  --> set by " + formatOriginSource(e.Origin))
	}

	return b.String()
}

// Unwrap returns the error the failure was built from.
func (e *ConfigError) Unwrap() error {
	return e.Err
}

// formatOriginSource renders the layer that supplied a value, for a
// [ConfigError] diagnostic.
func formatOriginSource(origin Origin) string {
	switch origin.Source {
	case SourceEnv:
		return "environment variable: " + origin.Path
	case SourceFlag:
		return "CLI flag: " + origin.Path
	case SourceDefault:
		return "struct defaults"
	case SourceStdin:
		return "standard input"
	case SourceSystem, SourceUser, SourceFile:
		if origin.Line > 0 {
			return origin.Path + ":" + strconv.Itoa(origin.Line)
		}

		return origin.Path
	default:
		return fmt.Sprintf("%s (%s)", origin.Source, origin.Path)
	}
}
