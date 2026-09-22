package strata

import (
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/zigai/strata/internal/defaulter"
)

// Metadata records which files were merged, and where each resolved key came
// from.
//
// A Metadata is safe for concurrent use, and its zero value is ready to record
// into. Origins are reachable only through its methods, so the mutex covers
// every access.
//
// All methods tolerate a nil receiver. A caller may therefore treat a missing
// Metadata as an empty one without a nil check.
type Metadata struct {
	origins     map[string]Origin
	secrets     map[string]bool
	canonical   map[string]string
	activeFiles []string
	mu          sync.RWMutex
}

// NewMetadata returns an empty Metadata ready to record into. The zero value is
// equally usable.
func NewMetadata() *Metadata {
	return &Metadata{
		origins:     make(map[string]Origin),
		secrets:     make(map[string]bool),
		canonical:   make(map[string]string),
		activeFiles: make([]string, 0),
		mu:          sync.RWMutex{},
	}
}

// Where returns the origin recorded for a dotted configuration key.
//
// Lookup is case-insensitive and trims surrounding whitespace, matching the
// normalization applied by [Metadata.Record]. The boolean reports whether the key
// is known. A nil receiver returns false.
func (m *Metadata) Where(key string) (Origin, bool) {
	if m == nil {
		return Origin{Key: "", Source: "", Path: "", Line: 0, RawValue: ""}, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	normalized := normalizeKey(key)
	if canon, ok := m.canonical[normalized]; ok {
		normalized = normalizeKey(canon)
	} else if canon, ok := m.canonical[normalizeKey(toSnakeCaseKey(key))]; ok {
		normalized = normalizeKey(canon)
	} else if canon, ok := m.canonical[normalizeKey(strings.ReplaceAll(key, "_", ""))]; ok {
		normalized = normalizeKey(canon)
	}

	origin, ok := m.origins[normalized]
	if !ok {
		origin, ok = m.origins[normalizeKey(key)]
	}

	return origin, ok
}

// Record stores the origin for a configuration key.
//
// An existing entry for the same normalized key is replaced, so a Metadata
// always describes the layer whose value won. The key is trimmed and lowercased
// before storage. A nil receiver is a no-op.
func (m *Metadata) Record(origin Origin) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.initMapsLocked()

	normKey := normalizeKey(origin.Key)
	snakeKey := normalizeKey(toSnakeCaseKey(origin.Key))
	cleanKey := normalizeKey(strings.ReplaceAll(origin.Key, "_", ""))

	if origin.RawValue == "[REDACTED]" {
		m.markSecret(normKey, snakeKey, cleanKey)
	}

	if origin.Source == SourceDefault {
		m.canonical[normKey] = origin.Key
		m.canonical[snakeKey] = origin.Key
		m.canonical[cleanKey] = origin.Key
	}

	origin.Key = m.resolveCanonical(normKey, snakeKey, cleanKey, origin.Key)

	if m.secrets[normalizeKey(origin.Key)] || m.secrets[snakeKey] || m.secrets[cleanKey] {
		origin.RawValue = "[REDACTED]"
		m.markSecret(normalizeKey(origin.Key), snakeKey, cleanKey)
	}

	m.origins[normalizeKey(origin.Key)] = origin
}

// RecordSecret marks a configuration key as secret, ensuring any origin recorded
// for that key has its raw value redacted.
func (m *Metadata) RecordSecret(key string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.initMapsLocked()

	normKey := normalizeKey(key)
	snakeKey := normalizeKey(toSnakeCaseKey(key))
	cleanKey := normalizeKey(strings.ReplaceAll(key, "_", ""))

	m.markSecret(normKey, snakeKey, cleanKey)

	m.canonical[normKey] = key
	m.canonical[snakeKey] = key
	m.canonical[cleanKey] = key
}

// AddActiveFile records a configuration file that contributed to the result.
//
// Paths are kept in first-seen order, and a repeated path is ignored. A nil
// receiver is a no-op.
func (m *Metadata) AddActiveFile(path string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if slices.Contains(m.activeFiles, path) {
		return
	}

	m.activeFiles = append(m.activeFiles, path)
}

// ActiveFiles returns the configuration files that contributed to the result, in
// first-seen order.
//
// The returned slice is a copy owned by the caller. Modifying it does not affect
// the Metadata. A nil receiver returns nil.
func (m *Metadata) ActiveFiles() []string {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Clone(m.activeFiles)
}

// NewConfigError builds a [ConfigError] for a key that failed validation,
// attaching the origin already recorded for that key when there is one.
//
// The raw input and the responsible layer come from the recorded origin, so a
// validator states which key failed and nothing else. Reporting a value the
// configuration never supplied is not expressible, and the diagnostic cannot
// disagree with the provenance it describes.
//
// The returned error unwraps to err, so callers keep working with [errors.Is].
// A nil receiver still produces a usable error, without origin context.
func (m *Metadata) NewConfigError(key string, err error) *ConfigError {
	var origin Origin

	if m != nil {
		origin, _ = m.Where(key)
	}

	return &ConfigError{
		Key:    key,
		Err:    err,
		Origin: origin,
	}
}

func (m *Metadata) initMapsLocked() {
	if m.origins == nil {
		m.origins = make(map[string]Origin)
	}

	if m.secrets == nil {
		m.secrets = make(map[string]bool)
	}

	if m.canonical == nil {
		m.canonical = make(map[string]string)
	}
}

func (m *Metadata) markSecret(keys ...string) {
	for _, k := range keys {
		m.secrets[k] = true
	}
}

func (m *Metadata) resolveCanonical(normKey, snakeKey, cleanKey, defaultKey string) string {
	if canon, ok := m.canonical[normKey]; ok {
		return canon
	}

	if canon, ok := m.canonical[snakeKey]; ok {
		return canon
	}

	if canon, ok := m.canonical[cleanKey]; ok {
		return canon
	}

	m.canonical[normKey] = defaultKey
	m.canonical[snakeKey] = defaultKey
	m.canonical[cleanKey] = defaultKey

	return defaultKey
}

// normalizeKey trims surrounding whitespace and lowercases, so that two spellings
// of one key resolve to the same entry.
func normalizeKey(s string) string {
	trimmed := strings.TrimSpace(s)
	hasUpper := false

	for _, r := range trimmed {
		if unicode.IsUpper(r) {
			hasUpper = true
			break
		}
	}

	if !hasUpper {
		return trimmed
	}

	return strings.ToLower(trimmed)
}

func toSnakeCaseKey(s string) string {
	parts := strings.Split(s, ".")
	for i, part := range parts {
		parts[i] = defaulter.ToSnakeCase(part)
	}

	return strings.Join(parts, ".")
}
