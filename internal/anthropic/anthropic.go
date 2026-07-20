// Package anthropic implements [stream.Stream] for Anthropic.
package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

var _ stream.Client = &Client{}

// Client is a client for the Anthropic API.
type Client struct {
	*anthropic.Client
	config Config
}

// Capabilities reports Anthropic backend features. The Anthropic
// adapter supports tool/function calling via CallTools.
func (c *Client) Capabilities() stream.Capabilities { return stream.Capabilities{Tools: true} }

// Request implements stream.Client.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	if request.MessageBudgeter != nil {
		messages, err := request.MessageBudgeter(request.Messages)
		if err != nil {
			return &Stream{budgetErr: err, messages: request.Messages}
		}
		request.Messages = messages
	}
	system, messages, err := fromProtoMessages(request.Messages)
	if err != nil {
		return &Stream{budgetErr: err, messages: request.Messages}
	}
	body := anthropic.MessageNewParams{
		Model:    anthropic.Model(request.Model),
		Messages: messages,
		System:   system,
		Tools:    fromToolSpecs(request.Tools),
	}

	explicitMaxTokens := request.MaxTokens != nil
	if explicitMaxTokens {
		body.MaxTokens = *request.MaxTokens
	} else {
		body.MaxTokens = 4096
	}

	thinkingActive := c.config.ThinkingActive ||
		c.config.ThinkingType == "adaptive" ||
		c.config.ThinkingType == "enabled"
	if request.Temperature != nil && !thinkingActive {
		body.Temperature = anthropic.Float(*request.Temperature)
	}

	switch c.config.ThinkingType {
	case "":
	case "adaptive":
		adaptive := anthropic.NewThinkingConfigAdaptiveParam()
		body.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	case "disabled":
		disabled := anthropic.NewThinkingConfigDisabledParam()
		body.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	case "enabled":
		if c.config.ThinkingBudget < 1024 {
			return &Stream{
				budgetErr: fmt.Errorf(
					"Anthropic thinking-budget must be at least 1024 tokens, got %d",
					c.config.ThinkingBudget,
				),
				messages: request.Messages,
			}
		}
		if explicitMaxTokens && body.MaxTokens <= int64(c.config.ThinkingBudget) {
			return &Stream{
				budgetErr: fmt.Errorf(
					"Anthropic max-tokens (%d) must be greater than thinking-budget (%d)",
					body.MaxTokens,
					c.config.ThinkingBudget,
				),
				messages: request.Messages,
			}
		}
		if !explicitMaxTokens {
			body.MaxTokens = max(body.MaxTokens, int64(c.config.ThinkingBudget)+4096)
		}
		body.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(c.config.ThinkingBudget))
	default:
		return &Stream{
			budgetErr: fmt.Errorf("unsupported Anthropic thinking-type %q", c.config.ThinkingType),
			messages:  request.Messages,
		}
	}

	if c.config.ReasoningEffort != "" {
		body.OutputConfig.Effort = anthropic.OutputConfigEffort(c.config.ReasoningEffort)
	}

	s := &Stream{
		stream:     c.Messages.NewStreaming(ctx, body),
		request:    body,
		toolCall:   request.ToolCaller,
		messages:   request.Messages,
		budgeter:   request.MessageBudgeter,
		trackUsage: request.TrackUsage,
	}

	s.factory = func() *ssestream.Stream[anthropic.MessageStreamEventUnion] {
		return c.Messages.NewStreaming(ctx, s.request)
	}
	return s
}

// Config represents the configuration for the Anthropic API client.
type Config struct {
	AuthToken          string
	BaseURL            string
	HTTPClient         *http.Client
	EmptyMessagesLimit uint
	ThinkingBudget     int
	ThinkingType       string
	ThinkingActive     bool
	ReasoningEffort    string
}

// DefaultConfig returns the default configuration for the Anthropic API client.
func DefaultConfig(authToken string) Config {
	return Config{
		AuthToken:  authToken,
		HTTPClient: &http.Client{},
	}
}

// New anthropic client with the given configuration.
func New(config Config) *Client {
	opts := []option.RequestOption{
		option.WithAPIKey(config.AuthToken),
		option.WithHTTPClient(config.HTTPClient),
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(NormalizeBaseURL(config.BaseURL)))
	}
	client := anthropic.NewClient(opts...)
	return &Client{
		Client: &client,
		config: config,
	}
}

// NormalizeBaseURL strips a redundant Anthropic messages-endpoint path from a
// user-supplied base URL. The SDK always appends /v1/messages, so a base URL
// that already includes the endpoint — as many providers quote it in their
// docs (e.g. https://gateway/v1/messages) — must be trimmed to avoid a
// doubled path like /v1/messages/v1/messages. Trailing slashes are removed
// first; at most one path suffix is then stripped; any other path is
// preserved verbatim. Shared with the wizard's model discovery so the two
// never drift.
func NormalizeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/v1/messages", "/messages", "/v1"} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}

// Stream represents a stream for chat completion.
type Stream struct {
	done       bool
	stream     *ssestream.Stream[anthropic.MessageStreamEventUnion]
	request    anthropic.MessageNewParams
	factory    func() *ssestream.Stream[anthropic.MessageStreamEventUnion]
	message    anthropic.Message
	toolCall   func(name string, data []byte) (string, error)
	messages   []proto.Message
	trackUsage bool
	usage      proto.TokenUsage
	budgeter   proto.MessageBudgeter
	budgetErr  error
}

// CallTools implements stream.Stream.
func (s *Stream) CallTools() []proto.ToolCallStatus {
	var statuses []proto.ToolCallStatus
	var results []anthropic.ContentBlockParamUnion
	for _, block := range s.message.Content {
		switch call := block.AsAny().(type) {
		case anthropic.ToolUseBlock:
			msg, status := stream.CallTool(
				call.ID,
				call.Name,
				[]byte(call.JSON.Input.Raw()),
				s.toolCall,
			)
			results = append(
				results,
				newToolResultBlock(call.ID, msg.Content, status.Err != nil),
			)
			s.messages = append(s.messages, msg)
			statuses = append(statuses, status)
		}
	}
	if len(results) > 0 {
		s.request.Messages = append(s.request.Messages, anthropic.NewUserMessage(results...))
	}
	return statuses
}

// Close implements stream.Stream.
func (s *Stream) Close() error {
	if s.stream == nil {
		return nil
	}
	return s.stream.Close() //nolint:wrapcheck
}

// Current implements stream.Stream.
func (s *Stream) Current() (proto.Chunk, error) {
	event := s.stream.Current()
	if err := s.message.Accumulate(event); err != nil {
		return proto.Chunk{}, err //nolint:wrapcheck
	}
	switch eventVariant := event.AsAny().(type) {
	case anthropic.ContentBlockDeltaEvent:
		switch deltaVariant := eventVariant.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			return proto.Chunk{
				Content: deltaVariant.Text,
			}, nil
		case anthropic.ThinkingDelta:
			return proto.Chunk{
				Thought: deltaVariant.Thinking,
			}, nil
		}
	}
	return proto.Chunk{}, stream.ErrNoContent
}

// Err implements stream.Stream.
func (s *Stream) Err() error {
	if s.budgetErr != nil {
		return s.budgetErr
	}
	if s.stream == nil {
		return nil
	}
	return s.stream.Err() //nolint:wrapcheck
}

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message { return s.messages }

// Usage implements stream.Stream.
func (s *Stream) Usage() proto.TokenUsage { return s.usage }

func tokenUsageFromMessage(message anthropic.Message) proto.TokenUsage {
	input := message.Usage.InputTokens +
		message.Usage.CacheCreationInputTokens +
		message.Usage.CacheReadInputTokens
	output := message.Usage.OutputTokens
	return proto.TokenUsage{
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  input + output,
	}
}

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	if s.budgetErr != nil {
		return false
	}
	if s.done {
		s.done = false
		if s.budgeter != nil {
			messages, err := s.budgeter(s.messages)
			if err != nil {
				s.budgetErr = err
				return false
			}
			s.messages = messages
			system, requestMessages, err := fromProtoMessages(messages)
			if err != nil {
				s.budgetErr = err
				return false
			}
			s.request.System, s.request.Messages = system, requestMessages
		}
		s.stream = s.factory()
		s.message = anthropic.Message{}
	}

	if s.stream.Next() {
		return true
	}

	s.done = true
	if s.trackUsage {
		s.usage.Add(tokenUsageFromMessage(s.message))
	}
	s.request.Messages = append(s.request.Messages, s.message.ToParam())
	message, err := toProtoMessage(s.message)
	if err != nil {
		s.budgetErr = err
		return false
	}
	s.messages = append(s.messages, message)

	return false
}
