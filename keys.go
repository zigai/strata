package strata

import (
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/layout"
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

// A nil children map accepts any key below this node. rest receives the keys
// of an inline map: every token no child claims.
type keyNode struct {
	children map[string]*keyNode
	element  *keyNode
	rest     *keyNode
	kind     reflect.Kind
	secret   bool
}

type keyTree struct {
	root  *keyNode
	names []jsontext.Pointer
}

// String formats the unknown key and its did-you-mean suggestion, when present.
func (k UnknownKey) String() string {
	if k.Suggestion != "" {
		return fmt.Sprintf(`unknown key "%s" (did you mean "%s"?)`, k.Key, k.Suggestion)
	}

	return fmt.Sprintf(`unknown key "%s"`, k.Key)
}

// KeyName returns a field's strata key token for validator tag-name hooks.
// It returns an empty string for a field excluded with a "-" tag.
func KeyName(field reflect.StructField) string {
	name := layout.FieldKey(field)
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

	node := &keyNode{children: nil, element: nil, rest: nil, kind: typ.Kind(), secret: false}
	nodes[typ] = node

	if typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		node.element = keyNodeFor(typ.Elem(), nodes)
	} else if layout.IsNestedStructType(typ) {
		structLayout := layout.Of(typ)

		node.children = make(map[string]*keyNode, len(structLayout.Fields))
		for _, field := range structLayout.Fields {
			node.children[field.Key] = withSecret(keyNodeFor(field.Member.Field.Type, nodes), field.Secret)
		}

		if rest := structLayout.Rest; rest != nil {
			node.rest = withSecret(keyNodeFor(rest.Member.Field.Type.Elem(), nodes), rest.Secret)
		}
	}

	return node
}

func withSecret(node *keyNode, secret bool) *keyNode {
	if !secret {
		return node
	}

	return &keyNode{children: node.children, element: node.element, rest: node.rest, kind: node.kind, secret: true}
}

func (t *keyTree) collectNames(typ reflect.Type, prefix jsontext.Pointer, active []reflect.Type) {
	typ = derefFieldType(typ)
	if slices.Contains(active, typ) || !layout.IsNestedStructType(typ) {
		return
	}

	active = append(active, typ)

	for _, field := range layout.Of(typ).Fields {
		key := prefix.AppendToken(field.Key)
		t.names = append(t.names, key)
		t.collectNames(field.Member.Field.Type, key, active)
	}
}

func derefFieldType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}

// step follows one token: a declared key, an inline map entry, or an element
// of a map or sequence. A node that accepts any key returns itself.
func (node *keyNode) step(token string) (*keyNode, bool) {
	if node.element != nil {
		return node.element, true
	}

	if node.children == nil {
		return node, true
	}

	if child, ok := node.children[token]; ok {
		return child, true
	}

	if node.rest != nil {
		return node.rest, true
	}

	return nil, false
}

func (t *keyTree) known(key jsontext.Pointer) bool {
	if !key.IsValid() {
		return false
	}

	node := t.root
	for token := range key.Tokens() {
		next, ok := node.step(token)
		if !ok {
			return false
		}

		if next == node && node.children == nil {
			return true
		}

		node = next
	}

	return true
}

func (t *keyTree) secret(key jsontext.Pointer) bool {
	if t == nil || !key.IsValid() {
		return false
	}

	node := t.root
	for token := range key.Tokens() {
		if node.secret {
			return true
		}

		next, ok := node.step(token)
		if !ok {
			return false
		}

		if next == node && node.children == nil {
			break
		}

		node = next
	}

	return node.containsSecret(make(map[*keyNode]bool))
}

func (t *keyTree) sequence(key jsontext.Pointer) bool {
	if t == nil {
		return false
	}

	node := t.root
	for token := range key.Tokens() {
		next, ok := node.step(token)
		if !ok || next == node && node.children == nil {
			return false
		}

		node = next
	}

	return node.kind == reflect.Slice || node.kind == reflect.Array
}

// namedChild matches token against the declared keys exactly first, then
// ignoring surrounding whitespace, case, and underscores, and returns the
// declared spelling. Under an inline map only exact matches count, since any
// other token is a key of that map.
func (node *keyNode) namedChild(token string) (string, *keyNode) {
	if child, found := node.children[token]; found {
		return token, child
	}

	if node.rest != nil {
		return "", nil
	}

	folded := foldKey(token)
	for name, child := range node.children {
		if foldKey(name) == folded {
			return name, child
		}
	}

	return "", nil
}

// canonical spells the declared struct keys in key the way the target type
// declares them. Map keys, inline map entries, sequence indexes, and anything
// the type does not declare keep their exact spelling.
func (t *keyTree) canonical(key jsontext.Pointer) jsontext.Pointer {
	var node *keyNode
	if t != nil {
		node = t.root
	}

	var canonical jsontext.Pointer

	for token := range key.Tokens() {
		spelled := token

		switch {
		case node == nil:
		case node.element != nil:
			node = node.element
		case node.children == nil:
			node = nil
		default:
			name, child := node.namedChild(token)
			if child != nil {
				spelled = name
				node = child
			} else {
				node = node.rest
			}
		}

		canonical = canonical.AppendToken(spelled)
	}

	return canonical
}

func (node *keyNode) containsSecret(visited map[*keyNode]bool) bool {
	if node == nil || visited[node] {
		return false
	}

	if node.secret {
		return true
	}

	visited[node] = true
	if node.element.containsSecret(visited) || node.rest.containsSecret(visited) {
		return true
	}

	for _, child := range node.children {
		if child.containsSecret(visited) {
			return true
		}
	}

	return false
}

func (t *keyTree) suggest(key jsontext.Pointer) jsontext.Pointer {
	target := strings.ToLower(string(key))
	limit := 1 + len(target)/suggestionLengthPerEdit

	var best jsontext.Pointer

	bestDistance := limit + 1

	for _, name := range t.names {
		if d := editDistance(target, strings.ToLower(string(name))); d < bestDistance {
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
