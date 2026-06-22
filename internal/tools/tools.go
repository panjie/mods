package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
	Spec          proto.ToolSpec
	Call          Caller
	Kind          ToolKind
	TimeoutPolicy TimeoutPolicy
	Capabilities  ToolCapabilities
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
