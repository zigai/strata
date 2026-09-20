// Package plan describes the CLI-addressable leaves of a configuration struct.
//
// A plan is framework-neutral. [Build] walks a root type and reports one [Target]
// per leaf field, carrying the index path, the configuration key, and the
// generated flag name that locate it. A CLI bridge turns those targets into flags
// of its own framework and resolves them back onto a configuration value with
// [ResolveField] and [EnsureField]. The package also carries the value layer both
// bridges share, which moves a leaf between a configuration field and its flag's
// storage or text form.
//
// The package is internal to the module, and its exported identifiers are not
// part of the published API.
package plan
