package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
)

// Caller executes a tool call.
type Caller func(context.Context, json.RawMessage) (string, error)

// ToolKind identifies a tool's execution backend.
type ToolKind string

const (
	ToolKindBuiltin ToolKind = "builtin"
	ToolKindShell   ToolKind = "shell"
	ToolKindMCP     ToolKind = "mcp"
)

// TimeoutPolicy describes who owns the tool call timeout.
type TimeoutPolicy string

const (
	TimeoutPolicyCaller TimeoutPolicy = "caller"
	TimeoutPolicySelf   TimeoutPolicy = "self"
)

// ToolCapabilities describe safety and policy-relevant behavior.
type ToolCapabilities struct {
	ReadOnly       bool
	Mutable        bool
	ShellExecution bool
}

// Tool is a registered executable tool.
type Tool struct {
	Spec            proto.ToolSpec
	Call            Caller
	Kind            ToolKind
	TimeoutPolicy   TimeoutPolicy
	Capabilities    ToolCapabilities
	IntentExtractor func(json.RawMessage) approval.AccessIntent
}

// Registry stores tools by name and exposes a provider-neutral call router.
type Registry struct {
	tools   map[string]Tool
	closers []func() error
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: map[string]Tool{},
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) error {
	if tool.Spec.Name == "" {
		return fmt.Errorf("tool: name is required")
	}
	if tool.Call == nil {
		return fmt.Errorf("tool %q: caller is required", tool.Spec.Name)
	}
	if _, ok := r.tools[tool.Spec.Name]; ok {
		return fmt.Errorf("tool %q: already registered", tool.Spec.Name)
	}
	if tool.Kind == "" {
		tool.Kind = ToolKindBuiltin
	}
	if tool.TimeoutPolicy == "" {
		tool.TimeoutPolicy = TimeoutPolicyCaller
	}
	r.tools[tool.Spec.Name] = tool
	return nil
}

// Specs returns registered tool definitions in a stable order.
func (r *Registry) Specs() []proto.ToolSpec {
	specs := make([]proto.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	slices.SortFunc(specs, func(a, b proto.ToolSpec) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return specs
}

// Call executes a registered tool by name.
func (r *Registry) Call(ctx context.Context, name string, data []byte) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("tool: unknown tool %q", name)
	}
	return tool.Call(ctx, json.RawMessage(data))
}

// Tool returns registered metadata for a tool.
func (r *Registry) Tool(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// TimeoutPolicy returns the timeout policy for a registered tool.
func (r *Registry) TimeoutPolicy(name string) TimeoutPolicy {
	tool, ok := r.Tool(name)
	if !ok || tool.TimeoutPolicy == "" {
		return TimeoutPolicyCaller
	}
	return tool.TimeoutPolicy
}

// Capabilities returns policy metadata for a registered tool.
func (r *Registry) Capabilities(name string) ToolCapabilities {
	tool, ok := r.Tool(name)
	if !ok {
		return ToolCapabilities{}
	}
	return tool.Capabilities
}

// ReadOnly reports whether a tool is safe for read-only contexts like plan mode.
func (r *Registry) ReadOnly(name string) bool {
	return r.Capabilities(name).ReadOnly
}

// Mutable reports whether a tool may mutate external state and should be reviewed.
func (r *Registry) Mutable(name string) bool {
	return r.Capabilities(name).Mutable
}

// ShellExecution reports whether a tool executes shell commands.
func (r *Registry) ShellExecution(name string) bool {
	return r.Capabilities(name).ShellExecution
}

// IntentExtractor returns the tool's access-intent extractor, which maps a
// tool call's arguments to an approval.AccessIntent (read/write plus the
// directories it touches). Tools without an extractor (e.g. shell tools,
// whose intent is derived dynamically) report ok=false.
func (r *Registry) IntentExtractor(name string) (func(json.RawMessage) approval.AccessIntent, bool) {
	tool, ok := r.tools[name]
	if !ok || tool.IntentExtractor == nil {
		return nil, false
	}
	return tool.IntentExtractor, true
}

// ValidateRequiredArgs reports whether data supplies every field the tool's
// schema marks as required. String fields must be present and non-empty;
// other field types need only be present. Unknown tools and tools whose
// schema has no "required" entry pass unchanged. Malformed JSON is left for
// the tool's own decodeArgs to report, so this only enforces presence and
// string emptiness.
//
// Intended to run before approval: it lets a malformed tool call (e.g. a
// missing required path) fail fast with a clear error instead of rendering a
// misleading review prompt for an operation that could never succeed.
func (r *Registry) ValidateRequiredArgs(name string, data []byte) error {
	tool, ok := r.Tool(name)
	if !ok {
		return nil
	}
	required, _ := tool.Spec.InputSchema["required"].([]string)
	if len(required) == 0 {
		return nil
	}
	var args map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &args); err != nil {
			return nil
		}
	}
	for _, field := range required {
		value, present := args[field]
		if !present {
			return fmt.Errorf("tool %q: %s is required", name, field)
		}
		if s, ok := value.(string); ok && s == "" {
			return fmt.Errorf("tool %q: %s is required", name, field)
		}
	}
	return nil
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	return len(r.tools)
}

// AddCloser registers cleanup work owned by the registry.
func (r *Registry) AddCloser(close func() error) {
	if close != nil {
		r.closers = append(r.closers, close)
	}
}

// Close releases resources owned by registered tools.
func (r *Registry) Close() error {
	var errs []error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if err := r.closers[i](); err != nil {
			errs = append(errs, err)
		}
	}
	r.closers = nil
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
