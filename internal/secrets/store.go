package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const prefix = "mods-secret://"

type Target struct {
	Tool string
	Path string
}

type entry struct {
	value  []byte
	target Target
}

// Store is a task-scoped in-memory credential vault.
type Store struct {
	mu         sync.Mutex
	entries    map[string]*entry
	redactions [][]byte
}

func New() *Store { return &Store{entries: map[string]*entry{}} }

func (s *Store) Put(value string, target Target) (string, error) {
	if value == "" {
		return "", fmt.Errorf("secret cannot be empty")
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret reference: %w", err)
	}
	ref := prefix + hex.EncodeToString(buf)
	s.mu.Lock()
	s.entries[ref] = &entry{value: []byte(value), target: target}
	s.mu.Unlock()
	return ref, nil
}

func IsRef(value string) bool { return strings.HasPrefix(value, prefix) }

// Resolve replaces exact secret-reference string values in a JSON document.
// Each reference is usable only at the tool/path bound when it was created.
func (s *Store) Resolve(tool string, data []byte) ([]byte, bool, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false, fmt.Errorf("resolve secret references: %w", err)
	}
	used := false
	var walk func(any, string) (any, error)
	walk = func(value any, path string) (any, error) {
		switch value := value.(type) {
		case string:
			if !IsRef(value) {
				return value, nil
			}
			s.mu.Lock()
			e, ok := s.entries[value]
			if ok && (e.target.Tool != tool || e.target.Path != path) {
				ok = false
			}
			var secret string
			if ok {
				secret = string(e.value)
			}
			s.mu.Unlock()
			if !ok {
				return nil, fmt.Errorf("secret reference is expired or not authorized for %s at %s", tool, path)
			}
			used = true
			return secret, nil
		case map[string]any:
			for key, child := range value {
				next, err := walk(child, path+"/"+escapePointer(key))
				if err != nil {
					return nil, err
				}
				value[key] = next
			}
		case []any:
			for i, child := range value {
				next, err := walk(child, fmt.Sprintf("%s/%d", path, i))
				if err != nil {
					return nil, err
				}
				value[i] = next
			}
		}
		return value, nil
	}
	resolved, err := walk(root, "")
	if err != nil {
		return nil, false, err
	}
	if !used {
		return data, false, nil
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, false, fmt.Errorf("encode resolved tool arguments: %w", err)
	}
	return out, true, nil
}

func (s *Store) Redact(value string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var values []string
	for _, e := range s.entries {
		values = append(values, string(e.value))
	}
	for _, secret := range s.redactions {
		values = append(values, string(secret))
	}
	// process_run serializes stdout/stderr as JSON before the app redacts it.
	// Match that representation too, and longer secrets first so a username
	// that prefixes a password cannot expose the rest of the password.
	for _, secret := range values {
		encoded, _ := json.Marshal(secret)
		if escaped := string(encoded[1 : len(encoded)-1]); escaped != secret {
			values = append(values, escaped)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, 2*len(values))
	for _, secret := range values {
		pairs = append(pairs, secret, "[REDACTED]")
	}
	return strings.NewReplacer(pairs...).Replace(value)
}

// RememberForRedaction protects runtime credentials without creating a tool
// reference that could subsequently be resolved by a model-supplied call.
func (s *Store) RememberForRedaction(value string) {
	if value == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.redactions {
		if string(existing) == value {
			return
		}
	}
	s.redactions = append(s.redactions, []byte(value))
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, secret := range s.redactions {
		clear(secret)
	}
	s.redactions = nil
	for ref, e := range s.entries {
		for i := range e.value {
			e.value[i] = 0
		}
		delete(s.entries, ref)
	}
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
