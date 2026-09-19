package strata

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

	// SourceProject is the project-local configuration file layer, discovered
	// from .<app>.* in the working directory.
	SourceProject SourceKind = "project"

	// SourceEnv is the environment variable layer. It ranks above every file.
	SourceEnv SourceKind = "env"

	// SourceStdin is the layer read from standard input, enabled by
	// [WithExplicitPath] with "-".
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

	// RawValue is the value as text, for diagnostics. It is not guaranteed to
	// parse back into the field's type.
	//
	// The environment and flag layers report the text the caller supplied. A file
	// layer reports the literal the document contained where the format exposes
	// one, so a large integer keeps its exact digits in YAML and JSON. The TOML
	// reader reports a rendering of the decoded value instead.
	//
	// A value from Go defaults has no source text. It is rendered with fmt's %v,
	// so a [time.Duration] default reads "10s" and a slice reads "[a b]". Those
	// are Go renderings, not the syntax of any configuration format.
	//
	// A field marked secret by its tag always reports "[REDACTED]".
	RawValue string
}
