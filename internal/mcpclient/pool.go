package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sync/errgroup"
)

type poolKey struct{}

// WithSessionPool retains MCP transports across sequential chat turns. Only
// transports and discovery results are shared; tool registries and approval
// contexts remain request-local. Call close after the chat loop exits.
func WithSessionPool(ctx context.Context) (context.Context, func() error) {
	lifetime, cancel := context.WithCancel(ctx)
	p := &sessionPool{ctx: lifetime, cancel: cancel}
	return context.WithValue(ctx, poolKey{}, p), p.close
}

type sessionPool struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	session *ToolSession
	key     [32]byte
	busy    bool
	closed  bool
}

func (p *sessionPool) close() error {
	// Cancel first so an in-flight discovery or tool call cannot hold up close.
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return p.session.Close()
}

func acquireSession(ctx context.Context, cfg *Config) (*ToolSession, func() error, error) {
	p, _ := ctx.Value(poolKey{}).(*sessionPool)
	if p == nil || len(cfg.MCPServers) == 0 {
		s, err := NewToolSession(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		return s, s.Close, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if p.closed || p.ctx.Err() != nil {
		return nil, nil, context.Canceled
	}
	if p.busy {
		// Concurrent requests must not refresh or close another request's tools.
		s, err := NewToolSession(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		return s, s.Close, nil
	}
	env := make(map[string][]string)
	for name, server := range EnabledServers(cfg) {
		env[name] = mcpSubprocessEnv(server)
	}
	data, err := json.Marshal(struct {
		Servers map[string]MCPServerConfig
		Env     map[string][]string
	}{cfg.MCPServers, env})
	if err != nil {
		return nil, nil, err
	}
	key := sha256.Sum256(data)
	if p.session != nil && (p.key != key || p.session.invalid.Load()) {
		_ = p.session.Close()
		p.session = nil
	}
	if p.session != nil {
		// Refresh schemas/annotations each turn, also checking transport health.
		// A stale readOnlyHint must never survive a server-side tool change.
		if err := p.session.refresh(ctx, cfg); err != nil {
			_ = p.session.Close()
			p.session = nil
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
		}
	}
	if p.session == nil {
		lifetime, cancel := context.WithCancel(p.ctx)
		stop := context.AfterFunc(ctx, cancel)
		s, err := NewToolSession(lifetime, cfg)
		stopped := stop()
		if err != nil || !stopped || ctx.Err() != nil {
			cancel()
			_ = s.Close()
			if err == nil {
				err = ctx.Err()
				if err == nil {
					err = context.Canceled
				}
			}
			return nil, nil, err
		}
		s.poolCancel = cancel
		p.session, p.key = s, key
	}
	p.busy = true
	var once sync.Once
	release := func() error {
		once.Do(func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() })
		return nil
	}
	return p.session, release, nil
}

func (s *ToolSession) refresh(ctx context.Context, cfg *Config) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.MCPTimeout)
	defer cancel()
	var group errgroup.Group
	for name, server := range s.servers {
		group.Go(func() error {
			server.mu.Lock()
			defer server.mu.Unlock()
			if server.client == nil {
				return fmt.Errorf("MCP %s is closed", name)
			}
			result, err := server.client.ListTools(ctx, mcp.ListToolsRequest{})
			if err != nil {
				return err
			}
			server.tools = result.Tools
			return nil
		})
	}
	return group.Wait()
}
