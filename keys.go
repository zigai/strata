package strata

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/defaulter"
)

const suggestionLengthPerEdit = 5

var keyTrees sync.Map // reflect.Type -> *keyTree

// UnknownKey is a key a configuration file set that the target type does not
// declare, usually a typo.
//
// Origin locates it; Origin.Key is the key as the file wrote it. The value is
// never kept, because a misspelled key may well hold a secret. Suggestion is the
// declared key it most resembles, or empty when none is close.
type UnknownKey struct {
	Origin

	Suggestion string
}

// A nil children map accepts any key below this node.
type keyNode struct {
	children map[string]*keyNode
	element  *keyNode
	kind     reflect.Kind
	secret   bool
}

type keyTree struct {
	root  *keyNode
	names []string
}

// String formats the unknown key and its did-you-mean suggestion, when present.
func (k UnknownKey) String() string {
	if k.Suggestion != "" {
		return fmt.Sprintf("unknown key %q (did you mean %q?)", k.Key, k.Suggestion)
	}

	return fmt.Sprintf("unknown key %q", k.Key)
}

// KeyName returns a field's strata key segment for validator tag-name hooks.
// It returns an empty string for a field excluded with a "-" tag.
func KeyName(field reflect.StructField) string {
	name := defaulter.FieldKey(field)
	if name == "-" {
		return ""
	}

	return name
}

func keyTreeFor(typ reflect.Type) *keyTree {
	if cached, ok := keyTrees.Load(typ); ok {
		tree, _ := cached.(*keyTree)
		return tree
	}

	tree := &keyTree{root: keyNodeFor(typ, make(map[reflect.Type]*keyNode)), names: nil}
	tree.collectNames(typ, "", nil)
	keyTrees.Store(typ, tree)

	return tree
}

func keyNodeFor(typ reflect.Type, nodes map[reflect.Type]*keyNode) *keyNode {
	typ = derefFieldType(typ)
	if node, exists := nodes[typ]; exists {
		return node
	}

	node := &keyNode{children: nil, element: nil, kind: typ.Kind(), secret: false}
	nodes[typ] = node

	if typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		node.element = keyNodeFor(typ.Elem(), nodes)
	} else if defaulter.IsNestedStructType(typ) {
		collectKeyNodes(node, typ, nodes)
	}

	return node
}

func collectKeyNodes(node *keyNode, typ reflect.Type, nodes map[reflect.Type]*keyNode) {
	node.children = make(map[string]*keyNode)

	var embeddedFields []reflect.StructField

	for field := range typ.Fields() {
		if field.Anonymous && defaulter.IsNestedStructType(derefFieldType(field.Type)) {
			embeddedFields = append(embeddedFields, field)
			continue
		}

		if !field.IsExported() {
			continue
		}

		key := defaulter.FieldKey(field)
		if key != "-" {
			node.children[key] = keyNodeForField(field, nodes)
		}
	}

	for _, field := range embeddedFields {
		child := keyNodeForField(field, nodes)
		for key, embedded := range child.children {
			if _, taken := node.children[key]; taken {
				continue
			}

			if child.secret {
				embedded = &keyNode{children: embedded.children, element: embedded.element, kind: embedded.kind, secret: true}
			}

			node.children[key] = embedded
		}
	}
}

func keyNodeForField(field reflect.StructField, nodes map[reflect.Type]*keyNode) *keyNode {
	child := keyNodeFor(field.Type, nodes)
	if defaulter.IsSecret(field) {
		return &keyNode{children: child.children, element: child.element, kind: child.kind, secret: true}
	}

	return child
}

func (t *keyTree) collectNames(typ reflect.Type, prefix string, active []reflect.Type) {
	if slices.Contains(active, typ) {
		return
	}

	active = append(active, typ)

	for field := range typ.Fields() {
		fieldType := derefFieldType(field.Type)

		if field.Anonymous && fieldType.Kind() == reflect.Struct && defaulter.IsNestedStructType(fieldType) {
			t.collectNames(fieldType, prefix, active)
			continue
		}

		if !field.IsExported() {
			continue
		}

		key := defaulter.FieldKey(field)
		if key == "-" {
			continue
		}

		dotted := key
		if prefix != "" {
			dotted = prefix + "." + key
		}

		t.names = append(t.names, dotted)

		if defaulter.IsNestedStructType(fieldType) {
			t.collectNames(fieldType, dotted, active)
		}
	}
}

func derefFieldType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}

func (t *keyTree) known(dotted string) bool {
	return t.knownSegments(strings.Split(dotted, "."))
}

func (t *keyTree) knownSegments(segments []string) bool {
	node := t.root

	for _, segment := range segments {
		if node.element != nil {
			node = node.element
			continue
		}

		if node.children == nil {
			return true
		}

		child, ok := node.children[segment]
		if !ok {
			return false
		}

		node = child
	}

	return true
}

func (t *keyTree) secret(dotted string) bool {
	return t.secretSegments(strings.Split(dotted, "."))
}

func (t *keyTree) secretSegments(segments []string) bool {
	if t == nil {
		return false
	}

	node := t.root
	for _, segment := range segments {
		if node.secret {
			return true
		}

		if node.element != nil {
			node = node.element
			continue
		}

		node = node.child(segment)
		if node == nil {
			return false
		}
	}

	return node.containsSecret(make(map[*keyNode]bool))
}

func (t *keyTree) sequenceSegments(segments []string) bool {
	if t == nil {
		return false
	}

	node := t.root
	for _, segment := range segments {
		if node.element != nil {
			node = node.element
			continue
		}

		node = node.child(segment)
		if node == nil {
			return false
		}
	}

	return node.kind == reflect.Slice || node.kind == reflect.Array
}

func (node *keyNode) child(segment string) *keyNode {
	if child, found := node.children[segment]; found {
		return child
	}

	for name, child := range node.children {
		if normalizeKey(name) == normalizeKey(segment) || normalizeKey(toSnakeCaseKey(name)) == normalizeKey(segment) || normalizeKey(strings.ReplaceAll(name, "_", "")) == normalizeKey(segment) {
			return child
		}
	}

	return nil
}

func (node *keyNode) containsSecret(visited map[*keyNode]bool) bool {
	if node == nil || visited[node] {
		return false
	}

	if node.secret {
		return true
	}

	visited[node] = true
	if node.element.containsSecret(visited) {
		return true
	}

	for _, child := range node.children {
		if child.containsSecret(visited) {
			return true
		}
	}

	return false
}

func (t *keyTree) suggest(dotted string) string {
	target := strings.ToLower(dotted)
	limit := 1 + len(target)/suggestionLengthPerEdit

	best, bestDistance := "", limit+1

	for _, name := range t.names {
		if d := editDistance(target, strings.ToLower(name)); d < bestDistance {
			best, bestDistance = name, d
		}
	}

	return best
}

// editDistance is the optimal string alignment distance between a and b: the
// number of insertions, deletions, substitutions, and adjacent transpositions
// that turn one into the other. A transposition counts once, so "prot" is one
// edit from "port".
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		cur[0] = i

		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)

			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}

		prev2, prev, cur = prev, cur, prev2
	}

	return prev[len(rb)]
}
