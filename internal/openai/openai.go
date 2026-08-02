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

// ProviderProfile identifies provider-specific behavior layered on the
// OpenAI-compatible transport. It applies to both Responses and Chat
// Completions because providers such as DeepSeek have dialect-specific state
// that must be replayed across tool rounds.
type ProviderProfile string

const (
	ProviderProfileOpenAI   ProviderProfile = "openai"
	ProviderProfileDeepSeek ProviderProfile = "deepseek"

	// Backward-compatible names retained for packages that constructed Config
	// before provider profiles also applied to Chat Completions.
	ResponsesProfileOpenAI   = ProviderProfileOpenAI
	ResponsesProfileDeepSeek = ProviderProfileDeepSeek
)

type ResponsesProfile = ProviderProfile

var _ stream.Client = &Client{}

// Client is the openai client.
type Client struct {
	*openai.Client
	config Config
}

// Config represents the configuration for the OpenAI API client.
type Config struct {
	AuthToken string
	BaseURL   string
	// UseResponses selects the Responses API.
	UseResponses    bool
	ProviderProfile ProviderProfile
	// ResponsesProfile is deprecated. ProviderProfile takes precedence when
	// set and this field remains for source compatibility.
	ResponsesProfile ResponsesProfile
	HTTPClient       interface {
		Do(*http.Request) (*http.Response, error)
	}
	Headers         map[string]string
	APIType         string
	ReasoningEffort shared.ReasoningEffort
	ExtraParams     map[string]any
	// ThinkTags enables splitting <think>...</think> blocks out of the
	// content stream into the chunk's Thought field. Some OpenAI-compatible
	// providers (e.g. MiniMax) inline reasoning this way rather than using a
	// dedicated field.
	ThinkTags bool
	// ThoughtFields overrides the list of delta fields consulted for
	// reasoning/thinking content. Empty = use defaultThoughtFields.
	ThoughtFields []string
	// ThinkTag overrides the inline reasoning tag name (without brackets).
	// Empty = "think" (rendered as <think>...</think>).
	ThinkTag string
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
	for k, v := range config.Headers {
		opts = append(opts, option.WithHeader(k, v))
	}

	switch config.APIType {
	case "azure":
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

// Capabilities reports OpenAI-compatible backend features. The OpenAI
// adapter supports tool/function calling (CallTools implements the
// multi-round tool loop).
func (c *Client) Capabilities() stream.Capabilities {
	capabilities := stream.Capabilities{
		Tools: true, FunctionTools: true, JSONResponseFormat: true,
		Images: true, Reasoning: true, ReasoningReplay: c.config.UseResponses,
	}
	if c.profile() == ProviderProfileDeepSeek {
		capabilities.ReasoningReplay = true
	}
	if c.config.UseResponses && c.profile() == ProviderProfileDeepSeek {
		capabilities.NativeWebSearch = true
		capabilities.HostedWebSearch = true
		capabilities.CustomApplyPatch = true
		capabilities.CustomTools = true
		capabilities.Images = false
		capabilities.ReasoningReplay = true
	}
	return capabilities
}

func (c *Client) profile() ProviderProfile {
	if c.config.ProviderProfile != "" {
		return c.config.ProviderProfile
	}
	if c.config.ResponsesProfile != "" {
		return c.config.ResponsesProfile
	}
	return ProviderProfileOpenAI
}

// Request makes a new request and returns a stream.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	if c.config.UseResponses {
		return c.requestResponses(ctx, request)
	}
	body := openai.ChatCompletionNewParams{
		Model:    request.Model,
		User:     openai.String(request.User),
		Messages: fromProtoMessagesForProfile(request.Messages, c.profile()),
		Tools:    fromToolSpecs(request.Tools),
	}
	if request.TrackUsage {
		body.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	if c.config.ReasoningEffort != "" {
		body.ReasoningEffort = c.config.ReasoningEffort
	}

	if request.API != "perplexity" || !strings.Contains(request.Model, "online") {
		if request.Temperature != nil {
			body.Temperature = openai.Float(*request.Temperature)
		}
		if request.MaxTokens != nil {
			body.MaxTokens = openai.Int(*request.MaxTokens)
		}
		if request.ResponseFormat != nil && *request.ResponseFormat == "json" {
			body.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
			}
		}
	}

	opts := make([]option.RequestOption, 0, len(c.config.ExtraParams)*2)
	flattenMap("", c.config.ExtraParams, func(k string, v any) {
		opts = append(opts, option.WithJSONSet(k, v))
	})

	tag := c.config.ThinkTag
	if tag == "" {
		tag = defaultThinkTag
	}

	s := &Stream{
		stream:        c.Chat.Completions.NewStreaming(ctx, body, opts...),
		request:       body,
		toolCall:      request.ToolCaller,
		messages:      request.Messages,
		trackUsage:    request.TrackUsage,
		parseThink:    c.config.ThinkTags,
		thoughtFields: c.config.ThoughtFields,
		profile:       c.profile(),
		think: thinkParser{
			openTag:  "<" + tag + ">",
			closeTag: "</" + tag + ">",
		},
	}
	s.factory = func() *ssestream.Stream[openai.ChatCompletionChunk] {
		return c.Chat.Completions.NewStreaming(ctx, s.request, opts...)
	}
	return s
}

// Stream openai stream.
type Stream struct {
	done           bool
	request        openai.ChatCompletionNewParams
	stream         *ssestream.Stream[openai.ChatCompletionChunk]
	factory        func() *ssestream.Stream[openai.ChatCompletionChunk]
	message        openai.ChatCompletionAccumulator
	messages       []proto.Message
	toolCall       func(name string, data []byte) (string, error)
	parseThink     bool
	think          thinkParser
	thoughtFields  []string
	trackUsage     bool
	roundUsage     proto.TokenUsage
	usage          proto.TokenUsage
	profile        ProviderProfile
	reasoning      strings.Builder
	visible        strings.Builder
	finalChunk     *proto.Chunk
	finalDelivered bool
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
func (s *Stream) Close() error {
	if s.stream == nil {
		return nil
	}
	return s.stream.Close() //nolint:wrapcheck
}

// Current implements stream.Stream.
func (s *Stream) Current() (proto.Chunk, error) {
	if s.finalChunk != nil && !s.finalDelivered {
		s.finalDelivered = true
		return *s.finalChunk, nil
	}
	event := s.stream.Current()
	s.message.AddChunk(event)
	if s.trackUsage && event.JSON.Usage.Valid() {
		total := event.Usage.TotalTokens
		if total == 0 {
			total = event.Usage.PromptTokens + event.Usage.CompletionTokens
		}
		s.roundUsage = proto.TokenUsage{
			InputTokens:           event.Usage.PromptTokens,
			CachedInputTokens:     event.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:          event.Usage.CompletionTokens,
			ReasoningOutputTokens: event.Usage.CompletionTokensDetails.ReasoningTokens,
			TotalTokens:           total,
		}
		if s.roundUsage.CachedInputTokens == 0 {
			var providerUsage struct {
				PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
			}
			if err := json.Unmarshal([]byte(event.Usage.RawJSON()), &providerUsage); err == nil {
				s.roundUsage.CachedInputTokens = providerUsage.PromptCacheHitTokens
			}
		}
	}
	if len(event.Choices) == 0 {
		return proto.Chunk{}, stream.ErrNoContent
	}
	choice := event.Choices[0]
	content := choice.Delta.Content
	thought := s.extractThought(choice.Delta)
	if s.profile == ProviderProfileDeepSeek && thought != "" {
		s.reasoning.WriteString(thought)
	}
	if s.parseThink {
		c, t := s.think.feed(content)
		content = c
		thought += t
	}
	s.visible.WriteString(content)
	return proto.Chunk{
		Content: content,
		Thought: thought,
	}, nil
}

// defaultThoughtFields is the priority-ordered list of non-standard chunk
// delta fields that OpenAI-compatible providers use to stream reasoning or
// thinking content. The first field that is present and non-null wins.
var defaultThoughtFields = []string{"reasoning_content", "reasoning", "thinking", "thinking_content"}

const defaultThinkTag = "think"

// extractThought reads reasoning/thinking content from a chunk delta's
// raw JSON. OpenAI's native API does not surface this, but DeepSeek-style
// providers expose it under `reasoning_content` or similar non-standard
// keys. (MiniMax instead inlines <think> blocks in content; see thinkParser.)
//
// We use Delta.RawJSON() rather than Delta.JSON.ExtraFields because the
// openai-go SDK cannot decode non-typed JSON values into respjson.Field
// and marks them as invalid.
func (s *Stream) extractThought(delta openai.ChatCompletionChunkChoiceDelta) string {
	raw := delta.RawJSON()
	if raw == "" {
		return ""
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return ""
	}
	fields := s.thoughtFields
	if len(fields) == 0 {
		fields = defaultThoughtFields
	}
	for _, field := range fields {
		v, ok := extra[field]
		if !ok {
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			continue
		}
		if str != "" {
			return str
		}
	}
	return ""
}

// thinkParser is a streaming splitter that separates reasoning blocks
// (e.g. <think>...</think>) from the regular answer content. The tag is
// configurable to support providers that use non-standard tag names.
// It tolerates tags that are split across streamed chunks by holding back
// a small tail that could be the start of a tag.
type thinkParser struct {
	inThink  bool
	buf      string
	openTag  string
	closeTag string
}

// feed processes a content delta and returns the portion that is regular
// answer content and the portion that is reasoning/thinking content.
func (p *thinkParser) feed(text string) (content, thought string) {
	p.buf += text
	var cb, tb strings.Builder
	for {
		if !p.inThink {
			idx := strings.Index(p.buf, p.openTag)
			if idx >= 0 {
				cb.WriteString(p.buf[:idx])
				p.buf = p.buf[idx+len(p.openTag):]
				p.inThink = true
				continue
			}
			keep := partialTagSuffixLen(p.buf, p.openTag)
			cb.WriteString(p.buf[:len(p.buf)-keep])
			p.buf = p.buf[len(p.buf)-keep:]
			return cb.String(), tb.String()
		}
		idx := strings.Index(p.buf, p.closeTag)
		if idx >= 0 {
			tb.WriteString(p.buf[:idx])
			p.buf = p.buf[idx+len(p.closeTag):]
			p.inThink = false
			continue
		}
		keep := partialTagSuffixLen(p.buf, p.closeTag)
		tb.WriteString(p.buf[:len(p.buf)-keep])
		p.buf = p.buf[len(p.buf)-keep:]
		return cb.String(), tb.String()
	}
}

func (p *thinkParser) flush() (content, thought string) {
	if p.inThink {
		thought = p.buf
	} else {
		content = p.buf
	}
	p.buf = ""
	p.inThink = false
	return content, thought
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
func (s *Stream) Err() error {
	if s.stream == nil {
		return nil
	}
	return s.stream.Err() //nolint:wrapcheck
}

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message { return s.messages }

// Usage implements stream.Stream.
func (s *Stream) Usage() proto.TokenUsage { return s.usage }

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	if s.finalChunk != nil {
		if !s.finalDelivered {
			return true
		}
		s.finalChunk = nil
		s.finalDelivered = false
		return false
	}
	if s.done {
		s.done = false
		s.stream = s.factory()
		s.message = openai.ChatCompletionAccumulator{}
		s.reasoning.Reset()
		s.visible.Reset()
	}

	if s.stream.Next() {
		return true
	}

	s.done = true
	s.usage.Add(s.roundUsage)
	s.roundUsage = proto.TokenUsage{}
	if s.parseThink {
		content, thought := s.think.flush()
		s.visible.WriteString(content)
		if content != "" || thought != "" {
			s.finalChunk = &proto.Chunk{Content: content, Thought: thought}
		}
	}
	if len(s.message.Choices) > 0 {
		msg := s.message.Choices[0].Message.ToParam()
		protoMsg := toProtoMessage(msg)
		if s.parseThink {
			protoMsg.Content = s.visible.String()
		}
		if s.profile == ProviderProfileDeepSeek {
			attachDeepSeekChatState(&protoMsg, s.reasoning.String())
		}
		s.messages = append(s.messages, protoMsg)
		formatted := fromProtoMessagesForProfile([]proto.Message{protoMsg}, s.profile)
		if len(formatted) > 0 {
			s.request.Messages = append(s.request.Messages, formatted[0])
		}
	}

	return s.finalChunk != nil
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
