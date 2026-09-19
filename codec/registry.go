package codec

import (
	"slices"
	"strings"
	"sync"
)

// Registry maps file extensions to [Codec] values.
//
// A Registry is safe for concurrent use and may be shared across goroutines.
// Mutations take the write lock and reads take the read lock.
//
// A Registry MUST NOT be copied after first use.
//
// The zero value is an empty registry that is ready to use.
type Registry struct {
	codecs map[string]Codec
	order  []string
	mu     sync.RWMutex
}

// NewRegistry returns a registry holding the built-in TOML, YAML, and JSON
// codecs, discoverable through the extensions .toml, .yaml, .yml, and .json in
// that priority order.
func NewRegistry() *Registry {
	r := &Registry{
		codecs: make(map[string]Codec),
		order:  []string{".toml", ".yaml", ".yml", ".json"},
		mu:     sync.RWMutex{},
	}
	r.Register(".toml", NewTOMLCodec())
	r.Register(".yaml", NewYAMLCodec())
	r.Register(".yml", NewYAMLCodec())
	r.Register(".json", NewJSONCodec())

	return r
}

// Register installs a codec for the given file extension.
//
// The extension is trimmed, lowercased, and prefixed with a dot when it does
// not have one, so "toml", ".toml", and ".TOML" all name the same entry.
// Registering an extension that is already present replaces its codec and
// leaves its position in [Registry.Extensions] unchanged. A new extension is
// appended to the end of that order.
//
// Register panics if c is nil. A stored nil codec would make every later
// [Registry.Get] report the extension as unregistered.
func (r *Registry) Register(ext string, c Codec) {
	if c == nil {
		panic("codec: Register: nil Codec for extension " + ext)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.codecs == nil {
		r.codecs = make(map[string]Codec)
	}

	normalized := normalizeExt(ext)
	r.codecs[normalized] = c

	if slices.Contains(r.order, normalized) {
		return
	}

	r.order = append(r.order, normalized)
}

// Get returns the codec registered for the given file extension.
//
// The lookup normalizes the extension the way [Registry.Register] does, so
// "toml", ".TOML", and ".toml" are equivalent. The boolean reports whether an
// entry exists; when it is true the codec is non-nil.
//
// The caller MUST check the boolean before using the codec.
func (r *Registry) Get(ext string) (Codec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	c, ok := r.codecs[normalizeExt(ext)]

	return c, ok
}

// Extensions returns the registered file extensions in auto-discovery priority
// order. The first extension whose file exists in a tier wins for that tier.
//
// The returned slice is owned by the caller. Mutating it does not affect the
// registry.
func (r *Registry) Extensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exts := make([]string, len(r.order))
	copy(exts, r.order)

	return exts
}

// Restrict limits the registry to the given extensions, in the order specified.
//
// Extensions in exts that have no registered codec are skipped. Registered
// extensions not present in exts are removed. If exts is empty, all extensions
// are removed.
func (r *Registry) Restrict(exts ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	allowed := make(map[string]struct{}, len(exts))

	var newOrder []string

	for _, ext := range exts {
		norm := normalizeExt(ext)
		if norm == "" {
			continue
		}

		if _, seen := allowed[norm]; seen {
			continue
		}

		allowed[norm] = struct{}{}
		if _, exists := r.codecs[norm]; exists {
			newOrder = append(newOrder, norm)
		}
	}

	for ext := range r.codecs {
		if _, ok := allowed[ext]; !ok {
			delete(r.codecs, ext)
		}
	}

	r.order = newOrder
}

// normalizeExt returns the canonical key for a file extension: trimmed,
// lowercased, and prefixed with a dot when it does not have one.
//
// NB: an empty extension normalizes to the empty string.
func normalizeExt(ext string) string {
	trimmed := strings.ToLower(strings.TrimSpace(ext))
	if trimmed == "" {
		return ""
	}

	if !strings.HasPrefix(trimmed, ".") {
		return "." + trimmed
	}

	return trimmed
}
