package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
	"golang.org/x/sync/errgroup"
)

func EnabledServers(cfg *Config) iter.Seq2[string, MCPServerConfig] {
	return func(yield func(string, MCPServerConfig) bool) {
		names := slices.Collect(maps.Keys(cfg.MCPServers))
		slices.Sort(names)
		for _, name := range names {
			if !yield(name, cfg.MCPServers[name]) {
				return
			}
		}
	}
}

func List(cfg *Config) {
	for name := range cfg.MCPServers {
		s := name + ui.StdoutStyles().Timeago.Render(" (enabled)")
		_, _ = lipgloss.Fprintln(os.Stdout, s)
	}
}

func ListTools(ctx context.Context, cfg *Config) error {
	servers, err := Tools(ctx, cfg)
	if err != nil {
		return err
	}
	for sname, tools := range servers {
		for _, tool := range tools {
			_, _ = lipgloss.Fprint(os.Stdout, ui.StdoutStyles().Timeago.Render(sname+" > "))
			_, _ = lipgloss.Fprintln(os.Stdout, tool.Name)
		}
	}
	return nil
}

func Tools(ctx context.Context, cfg *Config) (map[string][]mcp.Tool, error) {
	var mu sync.Mutex
	var wg errgroup.Group
	result := map[string][]mcp.Tool{}
	for sname, server := range EnabledServers(cfg) {
		wg.Go(func() error {
			serverTools, err := ToolsFor(ctx, sname, server)
			if errors.Is(err, context.DeadlineExceeded) {
				return modsError{
					Err:        fmt.Errorf("timeout while listing tools for %q - make sure the configuration is correct. If your server requires a docker container, make sure it's running", sname),
					ReasonText: "Could not list tools",
				}
			}
			if err != nil {
				return modsError{
					Err:        err,
					ReasonText: "Could not list tools",
				}
			}
			mu.Lock()
			result[sname] = append(result[sname], serverTools...)
			mu.Unlock()
			return nil
		})
	}
	if err := wg.Wait(); err != nil {
		return nil, err //nolint:wrapcheck
	}
	return result, nil
}

func RegisterTools(ctx context.Context, cfg *Config, registry *toolregistry.Registry) error {
	session, err := NewToolSession(ctx, cfg)
	if err != nil {
		return err
	}
	registry.AddCloser(session.Close)
	for sname, serverSession := range session.servers {
		serverTools := serverSession.tools
		for _, tool := range serverTools {
			name := fmt.Sprintf("%s_%s", sname, tool.Name)
			spec := proto.ToolSpec{
				Name:        name,
				Description: tool.Description,
				InputSchema: InputSchema(tool),
			}
			// Capture sname and tool.Name explicitly so the closure does not
			// rely on string splitting, which breaks when server names contain
			// underscores.
			capturedSname := sname
			capturedToolName := tool.Name
			if err := registry.Register(toolregistry.Tool{
				Spec:          spec,
				Kind:          toolregistry.ToolKindMCP,
				TimeoutPolicy: toolregistry.TimeoutPolicyCaller,
				Capabilities:  inferCapabilities(tool),
				Call: func(ctx context.Context, data json.RawMessage) (string, error) {
					return session.ToolCall(ctx, capturedSname, capturedToolName, data)
				},
			}); err != nil {
				_ = session.Close()
				return err
			}
		}
	}
	return nil
}

// inferCapabilities maps an MCP tool's self-declared annotations to mods'
// internal ToolCapabilities. The MCP protocol's readOnlyHint signals that a
// tool does not modify its environment; when a server explicitly sets it to
// true, mods treats the tool as read-only so it can skip interactive review
// (matching built-in read-only tools like fs_read_file and web_search).
//
// When the hint is absent or false the tool degrades fail-closed to mutable,
// preserving the conservative default so unannotated or write-capable tools
// still require approval.
func inferCapabilities(tool mcp.Tool) toolregistry.ToolCapabilities {
	if hint := tool.Annotations.ReadOnlyHint; hint != nil && *hint {
		return toolregistry.ToolCapabilities{ReadOnly: true}
	}
	return toolregistry.ToolCapabilities{Mutable: true}
}

// IsReadOnly reports whether an MCP tool self-declares as read-only via its
// readOnlyHint annotation. It is the public projection of inferCapabilities
// for callers (e.g. --list-tools) that only need the read/mutable distinction
// without importing the tools registry types.
func IsReadOnly(tool mcp.Tool) bool {
	return inferCapabilities(tool).ReadOnly
}

type ToolSession struct {
	servers map[string]*toolServerSession
}

type toolServerSession struct {
	client *client.Client
	tools  []mcp.Tool
	mu     sync.Mutex
}

func NewToolSession(ctx context.Context, cfg *Config) (*ToolSession, error) {
	var mu sync.Mutex
	var wg errgroup.Group
	session := &ToolSession{servers: map[string]*toolServerSession{}}
	for sname, server := range EnabledServers(cfg) {
		sname, server := sname, server
		wg.Go(func() error {
			cli, err := InitClient(ctx, server)
			if errors.Is(err, context.DeadlineExceeded) {
				return modsError{
					Err:        fmt.Errorf("timeout while listing tools for %q - make sure the configuration is correct. If your server requires a docker container, make sure it's running", sname),
					ReasonText: "Could not list tools",
				}
			}
			if err != nil {
				return modsError{
					Err:        fmt.Errorf("could not setup %s: %w", sname, err),
					ReasonText: "Could not list tools",
				}
			}
			tools, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
			if err != nil {
				cli.Close() //nolint:errcheck
				return modsError{
					Err:        fmt.Errorf("could not setup %s: %w", sname, err),
					ReasonText: "Could not list tools",
				}
			}
			mu.Lock()
			session.servers[sname] = &toolServerSession{client: cli, tools: tools.Tools}
			mu.Unlock()
			return nil
		})
	}
	if err := wg.Wait(); err != nil {
		_ = session.Close()
		return nil, err //nolint:wrapcheck
	}
	return session, nil
}

func (s *ToolSession) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	for name, server := range s.servers {
		server.mu.Lock()
		if server.client != nil {
			if err := server.client.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close MCP %s: %w", name, err))
			}
			server.client = nil
		}
		server.mu.Unlock()
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (s *ToolSession) ToolCall(ctx context.Context, sname, tool string, data []byte) (string, error) {
	server, ok := s.servers[sname]
	if !ok {
		return "", fmt.Errorf("mcp: server is not available: %q", sname)
	}
	var args map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &args); err != nil {
			return "", fmt.Errorf("mcp: %w: %s", err, string(data))
		}
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.client == nil {
		return "", fmt.Errorf("mcp: server is closed: %q", sname)
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = tool
	request.Params.Arguments = args
	result, err := server.client.CallTool(ctx, request)
	if err != nil {
		return "", fmt.Errorf("mcp: %w", err)
	}
	return toolResultText(result)
}

func InputSchema(tool mcp.Tool) map[string]any {
	schema := map[string]any{
		"type": "object",
	}
	if tool.InputSchema.Type != "" {
		schema["type"] = tool.InputSchema.Type
	}
	if tool.InputSchema.Properties != nil {
		schema["properties"] = tool.InputSchema.Properties
	}
	if len(tool.InputSchema.Required) > 0 {
		schema["required"] = tool.InputSchema.Required
	}
	return schema
}

// InitClient creates and initializes an MCP client.
func InitClient(ctx context.Context, server MCPServerConfig) (*client.Client, error) {
	var cli *client.Client
	var err error

	switch server.Type {
	case "", "stdio":
		env := mcpSubprocessEnv(server)
		cli, err = client.NewStdioMCPClientWithOptions(
			server.Command,
			env,
			server.Args,
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				// env passed in here is the filtered env from above; it
				// already contains everything we want the subprocess to
				// see, so don't append os.Environ() again.
				cmd.Env = env
				platform.HideCommandWindow(cmd)
				return cmd, nil
			}),
		)
	case "sse":
		if err = validateMCPRemoteURL(server.URL); err == nil {
			cli, err = client.NewSSEMCPClient(server.URL, client.WithHTTPClient(mcpHTTPClient()))
		}
	case "http":
		if err = validateMCPRemoteURL(server.URL); err == nil {
			cli, err = client.NewStreamableHttpClient(server.URL, transport.WithHTTPBasicClient(mcpHTTPClient()))
		}
	default:
		return nil, fmt.Errorf("unsupported MCP server type: %q, supported types are: stdio, sse, http", server.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	if err := cli.Start(ctx); err != nil {
		cli.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("failed to start MCP client: %w", err)
	}

	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		cli.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	return cli, nil
}

func ToolsFor(ctx context.Context, name string, server MCPServerConfig) ([]mcp.Tool, error) {
	cli, err := InitClient(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("could not setup %s: %w", name, err)
	}
	defer cli.Close() //nolint:errcheck

	tools, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("could not setup %s: %w", name, err)
	}
	return tools.Tools, nil
}

func toolResultText(result *mcp.CallToolResult) (string, error) {
	var sb strings.Builder
	for _, content := range result.Content {
		switch content := content.(type) {
		case mcp.TextContent:
			sb.WriteString(content.Text)
		default:
			sb.WriteString("[Non-text content]")
		}
	}
	if result.IsError {
		return "", errors.New(sb.String())
	}
	return sb.String(), nil
}
