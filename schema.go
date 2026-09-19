package strata

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/zigai/strata/internal/defaulter"
)

// ErrReflectSchema is returned when a schema cannot be reflected from a Go type.
var ErrReflectSchema = errors.New("failed to reflect schema")

type schemaOptions struct {
	id    string
	title string
}

// SchemaOption configures [Schema].
type SchemaOption func(*schemaOptions)

// WithSchemaID sets the root $id of the generated schema.
//
// The value is normally the URL the schema is served from, which editors use to
// resolve references.
func WithSchemaID(id string) SchemaOption {
	return func(o *schemaOptions) {
		o.id = id
	}
}

// WithSchemaTitle sets the title of the generated schema, which schema-aware
// editors display.
func WithSchemaTitle(title string) SchemaOption {
	return func(o *schemaOptions) {
		o.title = title
	}
}

// Schema returns the JSON Schema for T as indented JSON.
//
// Field names are derived from Go field names with the same snake_case
// conversion used elsewhere in the package, and a doc comment on a field of T
// becomes that field's description in the schema.
//
// [WithSchemaID] and [WithSchemaTitle] set the root $id and the title. Without
// them the reflector derives an $id from the type's package path.
//
// A T the reflector cannot describe is reported as an error wrapping
// [ErrReflectSchema]. A panic raised by the reflector is converted to that same
// error and does not escape. An interface-typed T has no fields to reflect, and
// a type containing a channel, function, or complex field has no JSON Schema.
func Schema[T any](opts ...SchemaOption) ([]byte, error) {
	options := &schemaOptions{
		id:    "",
		title: "",
	}
	for _, opt := range opts {
		opt(options)
	}

	var target T

	typ := reflect.TypeOf(target)
	if typ == nil {
		return nil, fmt.Errorf("%w for %T: no concrete type to reflect", ErrReflectSchema, target)
	}

	s, err := reflectSchema(typ)
	if err != nil {
		return nil, err
	}

	if options.id != "" {
		s.ID = jsonschema.ID(options.id)
	}

	if options.title != "" {
		s.Title = options.title
	}

	data, err := json.Marshal(s, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("marshal json schema: %w", err)
	}

	return data, nil
}

// reflectSchema returns the [jsonschema.Schema] for typ.
//
// The reflector panics on types it has no schema for. The panic is recovered and
// returned as an error wrapping [ErrReflectSchema]. A panic escaping the library
// call would terminate the process unless the caller recovered it.
func reflectSchema(typ reflect.Type) (*jsonschema.Schema, error) {
	reflector := &jsonschema.Reflector{
		ExpandedStruct: true,
		KeyNamer:       defaulter.ToSnakeCase,
	}

	var (
		schema   *jsonschema.Schema
		panicErr error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("%w for %s: %v", ErrReflectSchema, typ, r)
			}
		}()

		schema = reflector.ReflectFromType(typ)
	}()

	if panicErr != nil {
		return nil, panicErr
	}

	if schema == nil {
		return nil, fmt.Errorf("%w for %s", ErrReflectSchema, typ)
	}

	return schema, nil
}
