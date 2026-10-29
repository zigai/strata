package codec

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	errYAMLDuplicateKey = errors.New("mapping key already defined")
	errYAMLMerge        = errors.New("map merge requires a mapping or a sequence of mappings")
)

type yamlNode struct {
	node *yaml.Node
}

func newYAMLNode(node *yaml.Node) yamlNode {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}

	return yamlNode{node: node}
}

func (n yamlNode) kind() nodeKind {
	switch n.node.Kind {
	case yaml.MappingNode:
		return nodeMapping
	case yaml.SequenceNode:
		return nodeSequence
	case yaml.ScalarNode:
		if n.node.ShortTag() == "!!null" {
			return nodeNull
		}
	case yaml.DocumentNode, yaml.AliasNode:
	}

	return nodeScalar
}

func (n yamlNode) line() int { return n.node.Line }

func (n yamlNode) items() []overlayNode {
	items := make([]overlayNode, len(n.node.Content))
	for index, item := range n.node.Content {
		items[index] = newYAMLNode(item)
	}

	return items
}

func (n yamlNode) decodesItself(typ reflect.Type) bool {
	return decodesItself(typ) || typ.Kind() == reflect.Pointer && decodesItself(typ.Elem())
}

// decode hands a leaf to yaml.v3, which reports each failure with its line.
func (n yamlNode) decode(target reflect.Value) error {
	if target.Type() == reflect.TypeFor[time.Duration]() {
		if n.node.Kind != yaml.ScalarNode || n.node.ShortTag() != "!!str" {
			return errDurationString
		}

		parsed, err := time.ParseDuration(n.node.Value)
		if err != nil {
			return fmt.Errorf("parse duration: %w", err)
		}

		target.SetInt(int64(parsed))

		return nil
	}

	err := n.node.Decode(target.Addr().Interface())

	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err //nolint:wrapcheck // the overlay attributes it to a key
	}

	errs := make([]error, 0, len(typeErr.Errors))
	for _, message := range typeErr.Errors {
		line, text := splitYAMLLine(message, n.node.Line)
		errs = append(errs, &KeyError{Key: "", Line: line, Err: errors.New(text)}) //nolint:err113 // yaml.v3 reports failures as text
	}

	return errors.Join(errs...)
}

// entries lists a mapping's pairs with merge keys applied: an explicit key
// overrides a merged one, and an earlier merge source overrides a later one,
// as yaml.v3 decodes them.
func (n yamlNode) entries(o *overlay, key jsontext.Pointer) []overlayEntry {
	var explicit, merged []overlayEntry

	defined := make(map[string]int)

	for index := 0; index+1 < len(n.node.Content); index += 2 {
		keyNode := newYAMLNode(n.node.Content[index])
		value := newYAMLNode(n.node.Content[index+1])

		if keyNode.node.Kind == yaml.ScalarNode && keyNode.node.Value == "<<" && keyNode.node.ShortTag() == "!!merge" {
			merged = append(merged, value.mergeSources(o, key)...)
			continue
		}

		if keyNode.node.Kind != yaml.ScalarNode {
			o.fail(key, keyNode.node.Line, errInvalidYAMLMappingKey)
			continue
		}

		name := keyNode.node.Value
		if line, seen := defined[name]; seen {
			o.fail(key, keyNode.node.Line, fmt.Errorf("%w: %q at line %d", errYAMLDuplicateKey, name, line))
			continue
		}

		defined[name] = keyNode.node.Line

		explicit = append(explicit, overlayEntry{name: name, key: keyNode, value: value})
	}

	for _, entry := range merged {
		if _, seen := defined[entry.name]; seen {
			continue
		}

		defined[entry.name] = entry.key.line()

		explicit = append(explicit, entry)
	}

	return explicit
}

func (n yamlNode) mergeSources(o *overlay, key jsontext.Pointer) []overlayEntry {
	switch n.node.Kind {
	case yaml.MappingNode:
		return n.entries(o, key)
	case yaml.SequenceNode:
		var entries []overlayEntry

		seen := make(map[string]bool)

		for _, item := range n.node.Content {
			for _, entry := range newYAMLNode(item).mergeSources(o, key) {
				if !seen[entry.name] {
					seen[entry.name] = true
					entries = append(entries, entry)
				}
			}
		}

		return entries
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
	}

	o.fail(key, n.node.Line, errYAMLMerge)

	return nil
}

func splitYAMLLine(message string, fallback int) (int, string) {
	rest, found := strings.CutPrefix(message, "line ")
	if !found {
		return fallback, message
	}

	number, text, found := strings.Cut(rest, ": ")
	if !found {
		return fallback, message
	}

	line, err := strconv.Atoi(number)
	if err != nil {
		return fallback, message
	}

	return line, text
}
