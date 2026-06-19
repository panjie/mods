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

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/panjie/mods/internal/debug"
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
			if !IsEnabled(cfg, name) {
				continue
			}
			if !yield(name, cfg.MCPServers[name]) {
				return
			}
		}
	}
}

func IsEnabled(cfg *Config, name string) bool {
	if len(cfg.MCPEnable) > 0 {
		return slices.Contains(cfg.MCPEnable, name)
	}
	return !slices.Contains(cfg.MCPDisable, "*") &&
		!slices.Contains(cfg.MCPDisable, name)
}

func List(cfg *Config) {
	for name := range cfg.MCPServers {
		s := name
		if IsEnabled(cfg, name) {
			s += ui.StdoutStyles().Timeago.Render(" (enabled)")
		}
		fmt.Println(s)
	}
}

func ListTools(ctx context.Context, cfg *Config) error {
	servers, err := Tools(ctx, cfg)
	if err != nil {
		return err
	}
	for sname, tools := range servers {
		for _, tool := range tools {
			fmt.Print(ui.StdoutStyles().Timeago.Render(sname + " > "))
			fmt.Println(tool.Name)
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
	servers, err := Tools(ctx, cfg)
	if err != nil {
		return err
	}
	for sname, serverTools := range servers {
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
			capturedServer := cfg.MCPServers[sname]
			if err := registry.Register(toolregistry.Tool{
				Spec:          spec,
				Kind:          toolregistry.ToolKindMCP,
				TimeoutPolicy: toolregistry.TimeoutPolicyCaller,
				Call: func(ctx context.Context, data json.RawMessage) (string, error) {
					return ToolCallDirect(ctx, cfg, capturedSname, capturedToolName, capturedServer, data)
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
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
		cli, err = client.NewStdioMCPClientWithOptions(
			server.Command,
			append(os.Environ(), server.Env...),
			server.Args,
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = append(os.Environ(), env...)
				platform.HideCommandWindow(cmd)
				return cmd, nil
			}),
		)
	case "sse":
		cli, err = client.NewSSEMCPClient(server.URL)
	case "http":
		cli, err = client.NewStreamableHttpClient(server.URL)
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

// ToolCallDirect executes an MCP tool using pre-resolved server and tool names,
// avoiding string splitting that breaks when server names contain underscores.
func ToolCallDirect(ctx context.Context, cfg *Config, sname, tool string, server MCPServerConfig, data []byte) (string, error) {
	if !IsEnabled(cfg, sname) {
		return "", fmt.Errorf("mcp: server is disabled: %q", sname)
	}

	var args map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &args); err != nil {
			return "", fmt.Errorf("mcp: %w: %s", err, string(data))
		}
	}

	fullName := fmt.Sprintf("%s_%s", sname, tool)
	var result *mcp.CallToolResult
	var lastErr error
	for attempt := range 2 {
		client, err := InitClient(ctx, server)
		if err != nil {
			lastErr = fmt.Errorf("mcp: %w", err)
			continue
		}

		request := mcp.CallToolRequest{}
		request.Params.Name = tool
		request.Params.Arguments = args
		result, err = client.CallTool(ctx, request)
		client.Close() //nolint:errcheck
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		debug.Printf("MCP retry %d/2 for %s: %v", attempt+1, fullName, err)
	}
	if lastErr != nil {
		return "", fmt.Errorf("mcp: %w", lastErr)
	}

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
