package codec

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/strata/internal/duration"
)

var tomlLeafTypes sync.Map // reflect.Type -> reflect.Type

type tomlNode struct {
	value any
}

func (n tomlNode) kind() nodeKind {
	switch n.value.(type) {
	case map[string]any:
		return nodeMapping
	case []any:
		return nodeSequence
	default:
		return nodeScalar
	}
}

func (n tomlNode) line() int { return 0 }

func (n tomlNode) decodesItself(typ reflect.Type) bool {
	return typ.Implements(textUnmarshalerType) || reflect.PointerTo(typ).Implements(textUnmarshalerType)
}

func (n tomlNode) items() []overlayNode {
	values, _ := n.value.([]any)

	items := make([]overlayNode, len(values))
	for index, value := range values {
		items[index] = tomlNode{value: value}
	}

	return items
}

func (n tomlNode) entries(_ *overlay, _ jsontext.Pointer) []overlayEntry {
	table, _ := n.value.(map[string]any)

	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}

	slices.Sort(names)

	entries := make([]overlayEntry, len(names))
	for index, name := range names {
		value := tomlNode{value: table[name]}
		entries[index] = overlayEntry{name: name, key: textKey{name: name, parent: value}, value: value}
	}

	return entries
}

// decode converts one TOML value with go-toml by decoding a one-key document
// into a struct holding only the target's type.
func (n tomlNode) decode(target reflect.Value) error {
	typ := target.Type()

	if typ.Kind() == reflect.Interface && (n.value == nil || reflect.TypeOf(n.value).AssignableTo(typ)) {
		if n.value == nil {
			target.SetZero()
		} else {
			target.Set(reflect.ValueOf(n.value))
		}

		return nil
	}

	if typ == reflect.TypeFor[time.Duration]() {
		raw, ok := n.value.(string)
		if !ok {
			return errDurationString
		}

		parsed, err := duration.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}

		target.SetInt(int64(parsed))

		return nil
	}

	encoded, err := toml.Marshal(map[string]any{"value": n.value})
	if err != nil {
		return fmt.Errorf("toml marshal: %w", err)
	}

	holder := reflect.New(tomlLeafType(typ))
	holder.Elem().Field(0).Set(target)

	if err := toml.Unmarshal(encoded, holder.Interface()); err != nil {
		return tomlLeafError(err, typ)
	}

	target.Set(holder.Elem().Field(0))

	return nil
}

func tomlLeafType(typ reflect.Type) reflect.Type {
	if cached, ok := tomlLeafTypes.Load(typ); ok {
		holder, _ := cached.(reflect.Type)
		return holder
	}

	holder := reflect.StructOf([]reflect.StructField{{
		Name:      "Value",
		PkgPath:   "",
		Type:      typ,
		Tag:       `toml:"value"`,
		Offset:    0,
		Index:     nil,
		Anonymous: false,
	}})
	tomlLeafTypes.Store(typ, holder)

	return holder
}

// go-toml names the holder struct's field in its type errors; the overlay
// names the configuration key instead.
func tomlLeafError(err error, typ reflect.Type) error {
	var decodeErr *toml.DecodeError
	if !errors.As(err, &decodeErr) {
		return err
	}

	message := strings.TrimPrefix(decodeErr.Error(), "toml: ")
	if before, _, found := strings.Cut(message, " into struct field "); found {
		message = before + " into " + typ.String()
	}

	return errors.New(message) //nolint:err113 // go-toml reports failures as text
}
