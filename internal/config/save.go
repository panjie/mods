package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// FieldUpdate describes a single YAML field update. Path is already split into
// YAML mapping keys, so keys may safely contain dots, slashes, or colons.
type FieldUpdate struct {
	Path  []string
	Value any
}

// SaveFields updates specific fields in the YAML config file at path,
// preserving comments and formatting by round-tripping through yaml.Node.
//
// updates maps dotted key paths to values (string/bool/int). Nested paths
// navigate mapping nodes; missing intermediate mappings are created. A nil
// value deletes the field when present.
//
// Example:
//
//	SaveFields(path, map[string]any{
//	    "default-api":               "deepseek",
//	    "default-model":             "deepseek-v4-flash",
//	    "review-mode":               "mutable",
//	    "builtin-tools.filesystem":  "auto",
//	    "builtin-tools.shell":       false,
//	    "apis.openai.api-key-env":   "OPENAI_API_KEY",
//	})
func SaveFields(path string, updates map[string]any) error {
	pathUpdates := make([]FieldUpdate, 0, len(updates))
	for keyPath, value := range updates {
		pathUpdates = append(pathUpdates, FieldUpdate{
			Path:  strings.Split(keyPath, "."),
			Value: value,
		})
	}
	return SaveFieldPaths(path, pathUpdates)
}

// SaveFieldPaths updates specific fields using explicit YAML path parts. Use
// this when a key itself may contain dots, such as model names.
func SaveFieldPaths(path string, updates []FieldUpdate) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	mapping := rootMapping(&doc)

	for _, update := range updates {
		if update.Value == nil {
			deleteNestedValue(mapping, update.Path)
			continue
		}
		setNestedValue(mapping, update.Path, update.Value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// deleteNestedValue walks the mapping tree by path parts and removes the leaf
// key when present. Missing paths are ignored.
func deleteNestedValue(mapping *yaml.Node, parts []string) {
	if len(parts) == 0 || mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}

	key := parts[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if len(parts) == 1 {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
		deleteNestedValue(mapping.Content[i+1], parts[1:])
		return
	}
}

// HasAPIKey reports whether the default provider has a resolvable API key.
// Ollama is treated as always-OK (local, no key needed).
func HasAPIKey(config *Config) bool {
	if config.API == "ollama" {
		return true
	}
	for _, api := range config.APIs {
		if api.Name != config.API {
			continue
		}
		if api.APIKey != "" {
			return true
		}
		if api.APIKeyEnv != "" && os.Getenv(api.APIKeyEnv) != "" {
			return true
		}
		if api.APIKeyCmd != "" {
			return true
		}
		return false
	}
	return false
}

// applyEnvCustomProvider adds or updates a "custom" API profile from the
// MODS_BASE_URL and MODS_API_KEY environment variables. If both are set and
// the current default provider has no resolvable key, the default is switched
// to "custom" so the user can start using mods immediately without editing
// the config file.
func applyEnvCustomProvider(c *Config) {
	baseURL := os.Getenv("MODS_BASE_URL")
	apiKey := os.Getenv("MODS_API_KEY")
	if baseURL == "" || apiKey == "" {
		return
	}

	// Update an existing "custom" profile in-place, or append a new one.
	for i := range c.APIs {
		if c.APIs[i].Name == "custom" {
			c.APIs[i].BaseURL = baseURL
			c.APIs[i].APIKey = apiKey
			goto checkDefault
		}
	}
	c.APIs = append(c.APIs, API{
		Name:    "custom",
		BaseURL: baseURL,
		APIKey:  apiKey,
		Models: map[string]Model{
			"default": {Name: "default", MaxChars: 1000000},
		},
	})

checkDefault:
	// If the current default provider has no working key, switch to custom.
	if !HasAPIKey(c) {
		c.API = "custom"
		if c.Model == "" {
			c.Model = "default"
		}
	}
}

// rootMapping extracts the top-level mapping node from a parsed document,
// creating one if the document is empty or malformed.
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if doc.Content[0].Kind == yaml.MappingNode {
			return doc.Content[0]
		}
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	doc.Kind = yaml.DocumentNode
	doc.Content = []*yaml.Node{mapping}
	return mapping
}

// findInMapping returns the (keyNode, valueNode) pair for the given key,
// or (nil, nil) if not found.
func findInMapping(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// setNestedValue walks the mapping tree by path parts and sets the leaf value,
// creating intermediate mappings as needed.
func setNestedValue(mapping *yaml.Node, parts []string, value any) {
	if len(parts) == 0 || mapping == nil {
		return
	}

	key := parts[0]
	_, valNode := findInMapping(mapping, key)

	if len(parts) == 1 {
		// Leaf: set or create the value.
		if valNode != nil {
			setScalarValue(valNode, value)
		} else {
			mapping.Content = append(mapping.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				scalarNodeFor(value),
			)
		}
		return
	}

	// Intermediate: navigate or create a child mapping.
	if valNode == nil {
		child := &yaml.Node{Kind: yaml.MappingNode}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			child,
		)
		setNestedValue(child, parts[1:], value)
		return
	}

	if valNode.Kind == yaml.MappingNode {
		setNestedValue(valNode, parts[1:], value)
		return
	}

	// Existing node is not a mapping (e.g., a scalar); replace in-place
	// so the rest of the tree (and comments on sibling nodes) is unaffected.
	*valNode = yaml.Node{Kind: yaml.MappingNode}
	setNestedValue(valNode, parts[1:], value)
}

// setScalarValue overwrites an existing scalar node's value and type tag.
func setScalarValue(node *yaml.Node, value any) {
	node.Kind = yaml.ScalarNode
	node.Content = nil
	switch v := value.(type) {
	case bool:
		node.Tag = "!!bool"
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	case int:
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case string:
		node.Tag = "!!str"
		node.Value = v
	default:
		node.Tag = "!!str"
		node.Value = fmt.Sprintf("%v", v)
	}
}

// scalarNodeFor creates a fresh scalar node for the given value.
func scalarNodeFor(value any) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode}
	setScalarValue(node, value)
	return node
}
