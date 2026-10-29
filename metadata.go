package strata

import (
	"encoding/json/jsontext"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/zigai/strata/internal/keypath"
)

const (
	secretValue secretScope = iota + 1
	secretSubtree
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
	origins     map[jsontext.Pointer]Origin
	order       []jsontext.Pointer
	secrets     map[jsontext.Pointer]secretScope
	secretKeys  *keyTree
	activeFiles []string
	unknown     []UnknownKey
	mu          sync.RWMutex
}

type secretScope uint8

// NewMetadata returns an empty Metadata ready to record into. The zero value is
// equally usable.
func NewMetadata() *Metadata {
	return &Metadata{
		origins:     make(map[jsontext.Pointer]Origin),
		order:       nil,
		secrets:     make(map[jsontext.Pointer]secretScope),
		secretKeys:  nil,
		activeFiles: make([]string, 0),
		unknown:     nil,
		mu:          sync.RWMutex{},
	}
}

// Where returns the origin recorded for a dotted configuration key, such as
// "db.port". A literal dot or backslash inside one key is escaped as \. or
// \\, so `labels.a\.b` names the entry "a.b" under labels.
//
// An exactly recorded key matches first. Otherwise each token that names a
// declared field may differ in surrounding whitespace, letter case, and
// underscores, so "dbHost" finds "db_host". Map and inline map entries match
// only exactly. The boolean reports whether the key is known; a malformed key
// is never known. A nil receiver returns false.
func (m *Metadata) Where(dotted string) (Origin, bool) {
	key, err := keypath.Parse(dotted)
	if m == nil || err != nil {
		return Origin{Key: "", Source: "", Path: "", Line: 0, RawValue: ""}, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if origin, ok := m.origins[key]; ok {
		return origin, true
	}

	if origin, ok := m.origins[m.secretKeys.canonical(key)]; ok {
		return origin, true
	}

	return Origin{Key: "", Source: "", Path: "", Line: 0, RawValue: ""}, false
}

// Record stores the origin for a configuration key.
//
// An existing entry for the same key is replaced, so a Metadata always
// describes the layer whose value won. Tokens that name declared fields are
// spelled as the target type declares them, as [Metadata.Where] matches them;
// map entries keep their exact spelling. An origin whose key is malformed is
// ignored. A nil receiver is a no-op.
func (m *Metadata) Record(origin Origin) {
	key, err := keypath.Parse(origin.Key)
	if m == nil || err != nil {
		return
	}

	m.record(key, origin)
}

// RecordSecret marks a configuration key as secret, ensuring any origin recorded
// for that key or below it has its raw value redacted. A malformed key is
// ignored.
func (m *Metadata) RecordSecret(dotted string) {
	key, err := keypath.Parse(dotted)
	if m == nil || err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.initMapsLocked()
	m.markSecret(secretSubtree, key)

	for recordedKey, origin := range m.origins {
		if m.isSecretLocked(recordedKey) {
			origin.RawValue = "[REDACTED]"
			m.origins[recordedKey] = origin
		}
	}
}

// RecordActiveFile records a configuration file that contributed to the result.
//
// Paths are kept in first-seen order, and a repeated path is ignored. A nil
// receiver is a no-op.
func (m *Metadata) RecordActiveFile(path string) {
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

// Origins returns the origin of every resolved key in struct declaration order.
//
// It is the whole of what [Metadata.Where] answers one key at a time, for a
// caller that prints the effective configuration. The returned slice is a copy
// owned by the caller. A nil receiver returns nil.
func (m *Metadata) Origins() []Origin {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	origins := make([]Origin, 0, len(m.origins))
	for _, key := range m.order {
		origins = append(origins, m.origins[key])
	}

	return origins
}

// UnknownKeys returns the keys configuration files set that the target type
// does not declare, in the order they were read.
//
// These keys have no effect on the result, and are usually typos. Report them
// as warnings, or use [WithStrict] to fail the load instead. The returned slice
// is a copy owned by the caller. A nil receiver returns nil.
func (m *Metadata) UnknownKeys() []UnknownKey {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Clone(m.unknown)
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

func (m *Metadata) record(key jsontext.Pointer, origin Origin) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.initMapsLocked()

	key = m.secretKeys.canonical(key)
	origin.Key = keypath.Format(key)

	if origin.RawValue == "[REDACTED]" {
		m.markSecret(secretValue, key)
	}

	if m.isSecretLocked(key) {
		origin.RawValue = "[REDACTED]"
	}

	if m.secretKeys.sequence(key) {
		m.removeDescendantsLocked(key)
	}

	if _, exists := m.origins[key]; !exists {
		m.order = append(m.order, key)
	}

	m.origins[key] = origin
}

func (m *Metadata) removeDescendantsLocked(key jsontext.Pointer) {
	for recorded := range m.origins {
		if recorded != key && key.Contains(recorded) {
			delete(m.origins, recorded)
		}
	}

	m.order = slices.DeleteFunc(m.order, func(recorded jsontext.Pointer) bool {
		_, exists := m.origins[recorded]
		return !exists
	})
}

// orderBy sorts origins by the declaration order of names; a key below a
// declared key, such as a map entry, ranks with its nearest declared parent.
func (m *Metadata) orderBy(names []jsontext.Pointer) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ranks := make(map[jsontext.Pointer]int, len(names))
	for i, name := range names {
		ranks[name] = i
	}

	orderRanks := make(map[jsontext.Pointer]int, len(m.order))
	for _, key := range m.order {
		orderRanks[key] = len(names)

		for current := key; current != ""; current = current.Parent() {
			if rank, ok := ranks[current]; ok {
				orderRanks[key] = rank
				break
			}
		}
	}

	sort.SliceStable(m.order, func(i, j int) bool { return orderRanks[m.order[i]] < orderRanks[m.order[j]] })
}

func (m *Metadata) initMapsLocked() {
	if m.origins == nil {
		m.origins = make(map[jsontext.Pointer]Origin)
	}

	if m.secrets == nil {
		m.secrets = make(map[jsontext.Pointer]secretScope)
	}
}

func (m *Metadata) isSecretLocked(key jsontext.Pointer) bool {
	if m.secretKeys.secret(key) || m.secrets[key] != 0 {
		return true
	}

	for current := key; current != ""; {
		current = current.Parent()
		if m.secrets[current] == secretSubtree {
			return true
		}
	}

	return false
}

func (m *Metadata) markSecret(scope secretScope, key jsontext.Pointer) {
	key = m.secretKeys.canonical(key)
	m.secrets[key] = max(m.secrets[key], scope)
}

func foldKey(token string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(token), "_", ""))
}

func (m *Metadata) recordUnknown(unknown UnknownKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.unknown = append(m.unknown, unknown)
}
