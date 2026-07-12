package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	mu      sync.Mutex
	entries map[string]*entry
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

func ContainsRef(data []byte) bool { return bytes.Contains(data, []byte(prefix)) }

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
	for _, e := range s.entries {
		value = strings.ReplaceAll(value, string(e.value), "[REDACTED]")
	}
	return value
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
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
