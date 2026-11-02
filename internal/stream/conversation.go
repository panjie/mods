package stream

import (
	"context"
	"sync"

	"github.com/panjie/mods/internal/proto"
)

// Conversation owns the provider-neutral tool loop. A factory performs exactly
// one model request; only Next may start a subsequent request, after tool results
// have been committed. Close releases the current response, not the conversation.
type Conversation struct {
	transcript              *Transcript
	pending                 []proto.Message
	ctx                     context.Context
	request                 proto.Request
	factory                 func(context.Context, proto.Request) Round
	roundMu                 sync.RWMutex
	round                   Round
	mu                      sync.RWMutex
	usage                   proto.TokenUsage
	ended, followup, called bool
}

func NewConversation(ctx context.Context, request proto.Request, factory func(context.Context, proto.Request) Round) *Conversation {
	c := &Conversation{ctx: ctx, request: request, factory: factory}
	c.transcript, _ = ctx.Value(transcriptKey{}).(*Transcript)
	if c.transcript == nil {
		c.transcript = &Transcript{}
		c.transcript.Replace(request.Messages)
	}
	c.start()
	return c
}

func (c *Conversation) start() {
	request := c.request
	request.Messages = c.Messages()
	// Provider code cannot dispatch tools, including during request construction.
	request.ToolCaller, request.ResultCaller = nil, nil
	var round Round
	if err := c.ctx.Err(); err != nil {
		round = Failed(err, request.Messages)
	} else {
		round = c.factory(c.ctx, request)
	}
	c.roundMu.Lock()
	c.round = round
	c.roundMu.Unlock()
	if c.ctx.Err() != nil {
		_ = round.Close()
	}
	c.ended, c.followup, c.called = false, false, false
}

// Round exposes the current single request for protocol inspection.
func (c *Conversation) Round() Round {
	c.roundMu.RLock()
	defer c.roundMu.RUnlock()
	return c.round
}
func (c *Conversation) Next() bool {
	if c.followup {
		c.start()
	}
	if c.ended {
		return false
	}
	if c.Round().Next() {
		return true
	}
	c.ended = true
	c.mu.Lock()
	c.usage.Add(c.Round().Usage())
	if c.Round().Err() == nil {
		c.pending = proto.CloneMessages(c.Round().Messages())
		if len(c.pending) == 0 || len(c.pending[len(c.pending)-1].ToolCalls) == 0 {
			c.transcript.Replace(c.pending)
		}
	}
	c.mu.Unlock()
	return false
}
func (c *Conversation) Current() (proto.Chunk, error) { return c.Round().Current() }
func (c *Conversation) Err() error                    { return c.Round().Err() }
func (c *Conversation) Close() error                  { return c.Round().Close() }
func (c *Conversation) Messages() []proto.Message     { return c.transcript.Snapshot() }
func (c *Conversation) Usage() proto.TokenUsage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.usage
}
func (c *Conversation) CallTools() []proto.ToolCallStatus {
	if !c.ended || c.called || c.Err() != nil {
		return nil
	}
	c.called = true
	messages := c.pending
	if len(messages) == 0 {
		return nil
	}
	assistant := messages[len(messages)-1]
	if assistant.Role != proto.RoleAssistant {
		return nil
	}
	calls := assistant.ToolCalls
	var statuses []proto.ToolCallStatus
	var results []proto.Message
	for i, call := range calls {
		caller := c.request.ResultCaller
		if err := c.ctx.Err(); err != nil {
			caller = func(proto.ToolCallRequest) (proto.ToolResult, error) {
				return proto.ToolResult{Text: "Tool cancelled before execution."}, err
			}
		}
		result, status := CallTool(proto.ToolCallRequest{ID: call.ID, Index: i + 1, Total: len(calls), Name: call.Function.Name, Arguments: call.Function.Arguments}, c.request.ToolCaller, caller)
		result.ToolCalls[0].Type = call.Type
		results = append(results, result)
		statuses = append(statuses, status)
	}
	c.mu.Lock()
	c.transcript.Replace(append(messages, results...))
	c.mu.Unlock()
	c.followup = len(results) > 0 && c.ctx.Err() == nil
	return statuses
}
