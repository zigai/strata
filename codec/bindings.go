package codec

import (
	"reflect"
	"strings"

	"github.com/zigai/strata/internal/defaulter"
)

type fieldBinding struct {
	Name string
	Type reflect.Type
}

func keyBindings(typ reflect.Type, nativeName func(reflect.StructField) (string, bool)) map[string]fieldBinding {
	bindings := make(map[string]fieldBinding)
	collectKeyBindings(bindings, typ, nativeName, make(map[reflect.Type]bool))

	return bindings
}

func collectKeyBindings(bindings map[string]fieldBinding, typ reflect.Type, nativeName func(reflect.StructField) (string, bool), path map[reflect.Type]bool) {
	if path[typ] {
		return
	}

	path[typ] = true
	defer delete(path, typ)

	var embedded []reflect.StructField

	for field := range typ.Fields() {
		if field.Anonymous && derefType(field.Type).Kind() == reflect.Struct {
			embedded = append(embedded, field)
			continue
		}

		if !field.IsExported() {
			continue
		}

		key := defaulter.FieldKey(field)
		if key == "-" {
			continue
		}

		name, ok := nativeName(field)
		if !ok {
			continue
		}

		if _, taken := bindings[key]; !taken {
			bindings[key] = fieldBinding{Name: name, Type: field.Type}
		}
	}

	for _, field := range embedded {
		collectKeyBindings(bindings, derefType(field.Type), nativeName, path)
	}
}

func tagName(field reflect.StructField, tag string) (string, bool) {
	name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
	name = strings.TrimSpace(name)

	return name, name != ""
}
