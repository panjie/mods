// Package openai implements [stream.Stream] for OpenAI.
package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

const ReasoningEffortMedium = shared.ReasoningEffortMedium

var _ stream.Client = &Client{}

// Client is the openai client.
type Client struct {
	*openai.Client
	config Config
}

// Config represents the configuration for the OpenAI API client.
type Config struct {
	AuthToken  string
	BaseURL    string
	HTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	APIType         string
	ReasoningEffort shared.ReasoningEffort
	ExtraParams     map[string]any
	// ThinkTags enables splitting <think>...</think> blocks out of the
	// content stream into the chunk's Thought field. Some OpenAI-compatible
	// providers (e.g. MiniMax) inline reasoning this way rather than using a
	// dedicated field.
	ThinkTags bool
}

// DefaultConfig returns the default configuration for the OpenAI API client.
func DefaultConfig(authToken string) Config {
	return Config{
		AuthToken: authToken,
	}
}

// New creates a new [Client] with the given [Config].
func New(config Config) *Client {
	opts := []option.RequestOption{}

	if config.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	}

	switch config.APIType {
	case "azure", "azure-ad":
		opts = append(opts, azure.WithAPIKey(config.AuthToken))
		if config.BaseURL != "" {
			opts = append(opts, azure.WithEndpoint(config.BaseURL, "v1"))
		}
	default:
		opts = append(opts, option.WithAPIKey(config.AuthToken))
		if config.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(config.BaseURL))
		}
	}
	client := openai.NewClient(opts...)
	return &Client{
		Client: &client,
		config: config,
	}
}

// Request makes a new request and returns a stream.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	body := openai.ChatCompletionNewParams{
		Model:    request.Model,
		User:     openai.String(request.User),
		Messages: fromProtoMessages(request.Messages),
		Tools:    fromToolSpecs(request.Tools),
	}

	if c.config.ReasoningEffort != "" {
		body.ReasoningEffort = c.config.ReasoningEffort
	}

	if request.API != "perplexity" || !strings.Contains(request.Model, "online") {
		if request.Temperature != nil {
			body.Temperature = openai.Float(*request.Temperature)
		}
		if request.TopP != nil {
			body.TopP = openai.Float(*request.TopP)
		}
		body.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: request.Stop,
		}
		if request.MaxTokens != nil {
			body.MaxTokens = openai.Int(*request.MaxTokens)
		}
		if request.API == "openai" && request.ResponseFormat != nil && *request.ResponseFormat == "json" {
			body.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
			}
		}
	}

	opts := make([]option.RequestOption, 0, len(c.config.ExtraParams)*2)
	flattenMap("", c.config.ExtraParams, func(k string, v any) {
		opts = append(opts, option.WithJSONSet(k, v))
	})

	s := &Stream{
		stream:     c.Chat.Completions.NewStreaming(ctx, body, opts...),
		request:    body,
		toolCall:   request.ToolCaller,
		messages:   request.Messages,
		parseThink: c.config.ThinkTags,
	}
	s.factory = func() *ssestream.Stream[openai.ChatCompletionChunk] {
		return c.Chat.Completions.NewStreaming(ctx, s.request, opts...)
	}
	return s
}

// Stream openai stream.
type Stream struct {
	done       bool
	request    openai.ChatCompletionNewParams
	stream     *ssestream.Stream[openai.ChatCompletionChunk]
	factory    func() *ssestream.Stream[openai.ChatCompletionChunk]
	message    openai.ChatCompletionAccumulator
	messages   []proto.Message
	toolCall   func(name string, data []byte) (string, error)
	parseThink bool
	think      thinkParser
}

func (s *Stream) pendingToolCalls() []openai.ChatCompletionMessageToolCall {
	if len(s.message.Choices) == 0 {
		return nil
	}
	return s.message.Choices[0].Message.ToolCalls
}

// CallTools implements stream.Stream.
func (s *Stream) CallTools() []proto.ToolCallStatus {
	calls := s.pendingToolCalls()
	statuses := make([]proto.ToolCallStatus, 0, len(calls))
	for _, call := range calls {
		msg, status := stream.CallTool(
			call.ID,
			call.Function.Name,
			[]byte(call.Function.Arguments),
			s.toolCall,
		)
		resp := openai.ToolMessage(
			msg.Content,
			call.ID,
		)
		s.request.Messages = append(s.request.Messages, resp)
		s.messages = append(s.messages, msg)
		statuses = append(statuses, status)
	}
	return statuses
}

// Close implements stream.Stream.
func (s *Stream) Close() error { return s.stream.Close() } //nolint:wrapcheck

// Current implements stream.Stream.
func (s *Stream) Current() (proto.Chunk, error) {
	event := s.stream.Current()
	s.message.AddChunk(event)
	if len(event.Choices) == 0 {
		return proto.Chunk{}, stream.ErrNoContent
	}
	choice := event.Choices[0]
	content := choice.Delta.Content
	thought := extractThought(choice.Delta)
	if s.parseThink {
		c, t := s.think.feed(content)
		content = c
		thought += t
	}
	return proto.Chunk{
		Content: content,
		Thought: thought,
	}, nil
}

// thoughtFields is the priority-ordered list of non-standard chunk delta
// fields that OpenAI-compatible providers use to stream reasoning or
// thinking content. The first field that is present and non-null wins.
var thoughtFields = []string{"reasoning_content", "reasoning", "thinking", "thinking_content"}

// extractThought reads reasoning/thinking content from a chunk delta's
// raw JSON. OpenAI's native API does not surface this, but DeepSeek-style
// providers expose it under `reasoning_content` or similar non-standard
// keys. (MiniMax instead inlines <think> blocks in content; see thinkParser.)
//
// We use Delta.RawJSON() rather than Delta.JSON.ExtraFields because the
// openai-go SDK cannot decode non-typed JSON values into respjson.Field
// and marks them as invalid.
func extractThought(delta openai.ChatCompletionChunkChoiceDelta) string {
	raw := delta.RawJSON()
	if raw == "" {
		return ""
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return ""
	}
	for _, field := range thoughtFields {
		v, ok := extra[field]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue
		}
		if s != "" {
			return s
		}
	}
	return ""
}

const (
	openThinkTag  = "<think>"
	closeThinkTag = "</think>"
)

// thinkParser is a streaming splitter that separates <think>...</think>
// blocks (used by MiniMax and other Anthropic-style OpenAI-compatible
// providers to inline reasoning) from the regular answer content. It
// tolerates tags that are split across streamed chunks by holding back a
// small tail that could be the start of a tag.
type thinkParser struct {
	inThink bool
	buf     string
}

// feed processes a content delta and returns the portion that is regular
// answer content and the portion that is reasoning/thinking content.
func (p *thinkParser) feed(text string) (content, thought string) {
	p.buf += text
	var cb, tb strings.Builder
	for {
		if !p.inThink {
			idx := strings.Index(p.buf, openThinkTag)
			if idx >= 0 {
				cb.WriteString(p.buf[:idx])
				p.buf = p.buf[idx+len(openThinkTag):]
				p.inThink = true
				continue
			}
			keep := partialTagSuffixLen(p.buf, openThinkTag)
			cb.WriteString(p.buf[:len(p.buf)-keep])
			p.buf = p.buf[len(p.buf)-keep:]
			return cb.String(), tb.String()
		}
		idx := strings.Index(p.buf, closeThinkTag)
		if idx >= 0 {
			tb.WriteString(p.buf[:idx])
			p.buf = p.buf[idx+len(closeThinkTag):]
			p.inThink = false
			continue
		}
		keep := partialTagSuffixLen(p.buf, closeThinkTag)
		tb.WriteString(p.buf[:len(p.buf)-keep])
		p.buf = p.buf[len(p.buf)-keep:]
		return cb.String(), tb.String()
	}
}

// partialTagSuffixLen returns the length of the longest suffix of s that is
// also a proper prefix of tag. This is the amount of trailing text that must
// be held back because it might be the beginning of a tag completed by the
// next streamed chunk.
func partialTagSuffixLen(s, tag string) int {
	maxLen := len(tag) - 1
	if maxLen > len(s) {
		maxLen = len(s)
	}
	for n := maxLen; n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}

// Err implements stream.Stream.
func (s *Stream) Err() error { return s.stream.Err() } //nolint:wrapcheck

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message { return s.messages }

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	if s.done {
		s.done = false
		s.stream = s.factory()
		s.message = openai.ChatCompletionAccumulator{}
	}

	if s.stream.Next() {
		return true
	}

	s.done = true
	if len(s.message.Choices) > 0 {
		msg := s.message.Choices[0].Message.ToParam()
		s.request.Messages = append(s.request.Messages, msg)
		s.messages = append(s.messages, toProtoMessage(msg))
	}

	return false
}

func flattenMap(prefix string, m map[string]any, fn func(k string, v any)) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenMap(key, val, fn)
		default:
			fn(key, val)
		}
	}
}
