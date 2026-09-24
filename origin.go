package strata

import "fmt"

const (
	// SourceDefault is the layer supplied by Go struct defaults or a [Defaulter].
	// It ranks below every other layer.
	SourceDefault SourceKind = "default"

	// SourceSystem is the system-wide configuration file layer. It is the
	// lowest-precedence file tier, discovered under /etc/xdg on Unix and
	// %ProgramData% on Windows.
	SourceSystem SourceKind = "system"

	// SourceUser is the user configuration file layer, discovered under
	// ~/.config on Unix and %APPDATA% on Windows.
	SourceUser SourceKind = "user"

	// SourceFile is the configuration file named by [WithPath] or
	// [WithOptionalPath]. It ranks above the system and user files.
	SourceFile SourceKind = "file"

	// SourceEnv is the environment variable layer. It ranks above every file.
	SourceEnv SourceKind = "env"

	// SourceStdin is the layer read from standard input, enabled by
	// [WithPath] with "-".
	SourceStdin SourceKind = "stdin"

	// SourceFlag is the layer applied by the CLI bridge packages. It ranks above
	// everything else.
	SourceFlag SourceKind = "flag"
)

// SourceKind identifies the configuration layer that supplied a value. Values
// appear in ascending precedence order.
type SourceKind string

// Origin records where one configuration key came from.
type Origin struct {
	// Key is the dotted configuration key as the supplying layer named it,
	// normally in snake_case. Lookups through [Metadata.Where] are
	// case-insensitive, so the casing here is informational.
	Key string

	// Source identifies the layer that supplied the value.
	Source SourceKind

	// Path names the file that carried the value, or the environment variable
	// name or flag name for those layers.
	Path string

	// Line is the line within Path that defined the key.
	//
	// NB: Only the YAML reader populates this. It walks a node tree that carries
	// positions; the TOML and JSON readers report the file with no line.
	Line int

	// RawValue is diagnostic text, not necessarily valid configuration syntax.
	// Environment and flag values retain their input text. YAML and JSON retain
	// file literals; TOML and Go defaults use rendered values. Secrets are always
	// "[REDACTED]".
	RawValue string
}

// Location formats this origin as a layer and location, such as
// "user /path/config.toml:3", "env MYAPP_PORT", "flag --port", or "default".
func (o Origin) Location() string {
	if o.Line > 0 {
		return fmt.Sprintf("%s %s:%d", o.Source, o.Path, o.Line)
	}

	if o.Path != "" {
		return fmt.Sprintf("%s %s", o.Source, o.Path)
	}

	return string(o.Source)
}
