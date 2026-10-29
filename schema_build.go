package strata

import (
	"encoding"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/zigai/strata/internal/layout"
)

// schemaFieldTags are the tags invopop/jsonschema reads keywords from. A field
// keeps them when its schema is reflected under its configuration key.
var schemaFieldTags = [...]string{"jsonschema", "jsonschema_description", "jsonschema_extras"}

// A type describes its own schema with the methods invopop/jsonschema reads,
// on either receiver.
type schemaDescriber interface {
	JSONSchema() *jsonschema.Schema
}

type schemaExtender interface {
	JSONSchemaExtend(schema *jsonschema.Schema)
}

// A struct may also replace a property's schema with that of a sample value,
// and describe its fields itself, keyed by configuration key and by Go field
// name, on either receiver.
type (
	schemaPropertyAliaser interface {
		JSONSchemaProperty(prop string) any
	}

	schemaFieldDocumenter interface {
		GetFieldDocString(fieldName string) string
	}
)

// schemaBuilder describes the configuration files strata reads: every property
// is named by its configuration key, embedded structs follow the same rule as
// loading, and durations and text-decoded values are strings. invopop/jsonschema
// reflects each struct's fields from a stand-in struct whose json tags are the
// configuration keys, so field tags such as jsonschema:"description=..." keep
// working.
type schemaBuilder struct {
	reflector   *jsonschema.Reflector
	comments    map[string]string
	definitions jsonschema.Definitions
	names       map[reflect.Type]string
	taken       map[string]reflect.Type
	standIns    map[reflect.Type]schemaStandIn
}

type schemaStandIn struct {
	source reflect.Type
	fields []schemaSourceField
}

// schemaSourceField is the declaration a stand-in field came from: the struct
// that declares it, which holds its doc comment and schema hooks, and the
// field itself, whose type decides whether null is allowed.
type schemaSourceField struct {
	owner reflect.Type
	field reflect.StructField
	key   string
}

func newSchemaBuilder(comments map[string]string) *schemaBuilder {
	b := &schemaBuilder{
		reflector:   nil,
		comments:    comments,
		definitions: make(jsonschema.Definitions),
		names:       make(map[reflect.Type]string),
		taken:       make(map[string]reflect.Type),
		standIns:    make(map[reflect.Type]schemaStandIn),
	}

	b.reflector = &jsonschema.Reflector{
		Anonymous:      true,
		ExpandedStruct: true,
		DoNotReference: true,
		// Every key is optional in a config file, so none is required unless its
		// field says so with a jsonschema:"required" tag.
		RequiredFromJSONSchemaTags: true,
		Mapper:                     b.mapType,
		LookupComment:              b.lookupComment,
	}

	return b
}

func (b *schemaBuilder) root(typ reflect.Type) *jsonschema.Schema {
	if typ.Kind() == reflect.Interface {
		return nil
	}

	base := typ
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}

	if !layout.IsNestedStructType(base) || ownSchema(base) != nil {
		schema := b.reflector.ReflectFromType(typ)
		schema.Version = jsonschema.Version
		schema.ID = jsonschema.ID("https://" + base.PkgPath()).Add(layout.ToSnakeCase(base.Name()))

		return schema
	}

	schema := b.structSchema(base)
	schema.Version = jsonschema.Version
	schema.ID = jsonschema.ID("https://" + base.PkgPath()).Add(layout.ToSnakeCase(base.Name()))

	// Init writes a JSON template's schema URL into this property.
	schema.Properties.Set(jsonSchemaProperty, &jsonschema.Schema{Type: "string"})

	if len(b.definitions) > 0 {
		schema.Definitions = b.definitions
	}

	return schema
}

// mapType takes over the types whose file form strata decides.
func (b *schemaBuilder) mapType(typ reflect.Type) *jsonschema.Schema {
	if _, standIn := b.standIns[typ]; standIn {
		return nil
	}

	if own := ownSchema(typ); own != nil {
		return own
	}

	if typ == reflect.TypeFor[time.Duration]() || typ == reflect.TypeFor[Duration]() {
		return &jsonschema.Schema{Type: "string"}
	}

	if decodesFromText(typ) {
		return &jsonschema.Schema{Type: "string"}
	}

	if !layout.IsNestedStructType(typ) {
		return nil
	}

	if typ.Name() == "" {
		return b.structSchema(typ)
	}

	name := b.definitionName(typ)
	if _, built := b.definitions[name]; !built {
		// Reserve the name first, so a type that refers to itself ends in a
		// reference instead of recursing.
		b.definitions[name] = &jsonschema.Schema{}
		*b.definitions[name] = *b.structSchema(typ)
	}

	return &jsonschema.Schema{Ref: "#/$defs/" + name}
}

func (b *schemaBuilder) structSchema(typ reflect.Type) *jsonschema.Schema {
	standIn, fields, restKey := schemaStandInType(typ)
	b.standIns[standIn] = schemaStandIn{source: typ, fields: fields}

	schema := b.reflector.ReflectFromType(standIn)
	schema.Version = ""
	schema.ID = ""
	schema.Definitions = nil
	schema.Description = b.typeComment(typ)

	for _, source := range fields {
		property, ok := schema.Properties.Get(source.key)
		if !ok {
			continue
		}

		if documenter, ok := reflect.TypeAssert[schemaFieldDocumenter](reflect.New(source.owner)); ok {
			property.Description = documenter.GetFieldDocString(source.field.Name)
		}

		// A nil pointer is written as null, and null resets a field when loaded.
		if source.field.Type.Kind() == reflect.Pointer {
			description := property.Description
			property.Description = ""
			schema.Properties.Set(source.key, &jsonschema.Schema{AnyOf: []*jsonschema.Schema{property, {Type: "null"}}, Description: description})
		}
	}

	// An inline map accepts every key no field claims, with its value type.
	if restKey != "" {
		rest, _ := schema.Properties.Get(restKey)
		schema.Properties.Delete(restKey)
		schema.AdditionalProperties = rest
	}

	if extender, ok := reflect.TypeAssert[schemaExtender](reflect.New(typ)); ok {
		extender.JSONSchemaExtend(schema)
	}

	return schema
}

func (b *schemaBuilder) definitionName(typ reflect.Type) string {
	if name, ok := b.names[typ]; ok {
		return name
	}

	name := typ.Name()
	if owner, taken := b.taken[name]; taken && owner != typ {
		name = strings.ReplaceAll(typ.PkgPath(), "/", ".") + "." + name
	}

	b.names[typ] = name
	b.taken[name] = typ

	return name
}

func (b *schemaBuilder) lookupComment(typ reflect.Type, field string) string {
	standIn, ok := b.standIns[typ]
	if !ok || field == "" {
		return ""
	}

	index, err := strconv.Atoi(strings.TrimPrefix(field, "F"))
	if err != nil || index >= len(standIn.fields) {
		return ""
	}

	source := standIn.fields[index]

	return b.comments[source.owner.PkgPath()+"."+source.owner.Name()+"."+source.field.Name]
}

func (b *schemaBuilder) typeComment(typ reflect.Type) string {
	if typ.Name() == "" {
		return ""
	}

	return b.comments[typ.PkgPath()+"."+typ.Name()]
}

// schemaStandInType builds a struct with one field per configuration key of
// typ, tagged with the key, and returns the declaration of each field. A
// property alias replaces a field's type with its sample's. An inline map gets
// one more field, whose generated key is returned so its schema can become
// additionalProperties.
func schemaStandInType(typ reflect.Type) (reflect.Type, []schemaSourceField, string) {
	structLayout := layout.Of(typ)

	fields := make([]reflect.StructField, 0, len(structLayout.Fields)+1)
	sources := make([]schemaSourceField, 0, len(structLayout.Fields))

	for _, field := range structLayout.Fields {
		fieldType := field.Member.Field.Type
		if aliaser, ok := reflect.TypeAssert[schemaPropertyAliaser](reflect.New(field.Owner)); ok {
			if alias := aliaser.JSONSchemaProperty(field.Key); alias != nil {
				fieldType = reflect.TypeOf(alias)
			}
		}

		fields = append(fields, reflect.StructField{
			Name:      "F" + strconv.Itoa(len(fields)),
			PkgPath:   "",
			Type:      fieldType,
			Tag:       schemaFieldTag(field.Member.Field, field.Key),
			Offset:    0,
			Index:     nil,
			Anonymous: false,
		})
		sources = append(sources, schemaSourceField{owner: field.Owner, field: field.Member.Field, key: field.Key})
	}

	if structLayout.Rest == nil {
		return reflect.StructOf(fields), sources, ""
	}

	restKey := "*"
	for _, taken := structLayout.Field(restKey); taken; _, taken = structLayout.Field(restKey) {
		restKey += "*"
	}

	fields = append(fields, reflect.StructField{
		Name:      "F" + strconv.Itoa(len(fields)),
		PkgPath:   "",
		Type:      structLayout.Rest.Member.Field.Type.Elem(),
		Tag:       reflect.StructTag(`json:` + strconv.Quote(restKey)),
		Offset:    0,
		Index:     nil,
		Anonymous: false,
	})

	return reflect.StructOf(fields), sources, restKey
}

func schemaFieldTag(field reflect.StructField, key string) reflect.StructTag {
	var tag strings.Builder

	tag.WriteString(`json:` + strconv.Quote(key))

	for _, name := range schemaFieldTags {
		if value, ok := field.Tag.Lookup(name); ok {
			tag.WriteString(" " + name + ":" + strconv.Quote(value))
		}
	}

	return reflect.StructTag(tag.String())
}

func ownSchema(typ reflect.Type) *jsonschema.Schema {
	if describer, ok := reflect.TypeAssert[schemaDescriber](reflect.New(typ)); ok {
		return describer.JSONSchema()
	}

	return nil
}

// decodesFromText reports whether strata reads typ from a string through its
// text decoder. [time.Time] keeps the date-time format invopop gives it.
func decodesFromText(typ reflect.Type) bool {
	if typ == reflect.TypeFor[time.Time]() {
		return false
	}

	textUnmarshaler := reflect.TypeFor[encoding.TextUnmarshaler]()

	return typ.Implements(textUnmarshaler) || typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(textUnmarshaler)
}
