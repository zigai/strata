package strata

import (
	"reflect"

	"github.com/zigai/strata/internal/env"
)

// EnvVar is one environment variable a configuration type reads.
type EnvVar struct {
	// Key is the dotted configuration key the variable sets.
	Key string

	// Name is the variable to document: the env tag when the field declares
	// one, and otherwise the prefix followed by the upper-snake key, such as
	// MYAPP_DATABASE_PORT.
	Name string

	// Names lists every variable the load tries for the key, in order. The
	// first one set wins. A nested key is also read with "__" between
	// segments, as in MYAPP_DATABASE__PORT, which is tried first.
	Names []string

	// Secret reports whether the field is tagged secret.
	Secret bool
}

// EnvVars lists the environment variables that [WithEnvPrefix] with prefix
// would bind for T, in field declaration order.
//
// Use it to document the variables a program accepts, for example in --help
// output. Without a prefix, only fields with an env tag are listed, since those
// are the only ones bound.
func EnvVars[T any](prefix string) []EnvVar {
	described := env.Describe(reflect.TypeFor[T](), prefix)

	vars := make([]EnvVar, len(described))
	for i, v := range described {
		vars[i] = EnvVar{Key: v.Key, Name: v.Name, Names: v.Names, Secret: v.Secret}
	}

	return vars
}
