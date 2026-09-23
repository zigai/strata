package strata

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/defaulter"
)

// suggestionLengthPerEdit is how many characters of a key buy one more edit of
// tolerance when suggesting a correction: a four-letter key tolerates one edit,
// a ten-letter key three.
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

// keyNode is one level of the key tree a type declares. A nil children map
// marks a node that accepts any key below it: a map, an interface, or a leaf.
type keyNode struct {
	children map[string]*keyNode
}

// keyTree describes the keys a configuration type accepts, and the dotted names
// used to suggest a correction.
type keyTree struct {
	root  *keyNode
	names []string
}

func keyTreeFor(typ reflect.Type) *keyTree {
	if cached, ok := keyTrees.Load(typ); ok {
		tree, _ := cached.(*keyTree)
		return tree
	}

	tree := &keyTree{root: &keyNode{children: make(map[string]*keyNode)}, names: nil}
	tree.collect(tree.root, typ, "", nil)
	keyTrees.Store(typ, tree)

	return tree
}

func (t *keyTree) collect(node *keyNode, typ reflect.Type, prefix string, active []reflect.Type) {
	if slices.Contains(active, typ) {
		// A recursive type accepts anything below the repetition.
		node.children = nil
		return
	}

	active = append(active, typ)

	for field := range typ.Fields() {
		fieldType := derefFieldType(field.Type)

		if field.Anonymous && fieldType.Kind() == reflect.Struct && defaulter.IsNestedStructType(fieldType) {
			t.collect(node, fieldType, prefix, active)
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

		child := &keyNode{children: nil}
		if defaulter.IsNestedStructType(fieldType) {
			child.children = make(map[string]*keyNode)
			t.collect(child, fieldType, dotted, active)
		}

		for _, alias := range keyAliases(field, key) {
			node.children[alias] = child
		}
	}
}

// keyAliases lists every spelling a decoder binds to the field, lowercased,
// since at least one decoder matches names case-insensitively.
func keyAliases(field reflect.StructField, key string) []string {
	aliases := []string{key, field.Name, defaulter.ToSnakeCase(field.Name)}

	for _, tag := range []string{"strata", "toml", "yaml", "json"} {
		name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if name = strings.TrimSpace(name); name != "" && name != "-" {
			aliases = append(aliases, name)
		}
	}

	for i, alias := range aliases {
		aliases[i] = strings.ToLower(alias)
	}

	return aliases
}

func derefFieldType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ
}

// known reports whether a dotted key, as a document wrote it, names a declared
// field or lies below one that accepts any key.
func (t *keyTree) known(dotted string) bool {
	node := t.root

	for segment := range strings.SplitSeq(dotted, ".") {
		if node.children == nil {
			return true
		}

		child, ok := node.children[strings.ToLower(segment)]
		if !ok {
			return false
		}

		node = child
	}

	return true
}

// suggest returns the declared key closest to dotted, or empty when none is
// close enough to be a likely typo.
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
