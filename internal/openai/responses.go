package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

var errResponsesStreamEnded = errors.New("OpenAI Responses stream ended before a terminal event")

func (c *Client) requestResponses(ctx context.Context, request proto.Request) stream.Stream {
	profile := c.profile()
	if profile == ProviderProfileDeepSeek && messagesContainImages(request.Messages) {
		return &responseStream{
			requestErr: fmt.Errorf("DeepSeek Responses API does not support image or file input; use endpoint: chat-completions or remove the attachment"),
			messages:   request.Messages,
		}
	}
	input, err := fromProtoResponseInput(request.Messages, profile)
	if err != nil {
		return &responseStream{requestErr: err, messages: request.Messages}
	}
	effort, hasEffort, err := c.responsesReasoningEffort()
	if err != nil {
		return &responseStream{requestErr: err, messages: request.Messages}
	}
	body := responses.ResponseNewParams{
		Model: shared.ResponsesModel(request.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		Tools: fromResponseToolSpecs(request.Tools),
		User:  openai.String(request.User),
	}
	if profile != ProviderProfileDeepSeek {
		body.Store = openai.Bool(false)
		body.Include = []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		}
	}
	if request.Temperature != nil {
		body.Temperature = openai.Float(*request.Temperature)
	}
	if request.MaxTokens != nil {
		body.MaxOutputTokens = openai.Int(*request.MaxTokens)
	}
	if request.ResponseFormat != nil && *request.ResponseFormat == "json" {
		body.Text.Format.OfJSONObject = &shared.ResponseFormatJSONObjectParam{}
	}
	if hasEffort {
		if profile == ProviderProfileDeepSeek {
			effort, err = deepSeekReasoningEffort(effort)
			if err != nil {
				return &responseStream{requestErr: err, messages: request.Messages}
			}
		}
		body.Reasoning.Effort = shared.ReasoningEffort(effort)
	}
	if c.config.ThinkTags && profile != ProviderProfileDeepSeek {
		body.Reasoning.Summary = shared.ReasoningSummaryAuto
	}

	opts := responsesRequestOptions(c.config.ExtraParams, profile)
	s := &responseStream{
		stream:     c.Responses.NewStreaming(ctx, body, opts...),
		request:    body,
		toolCall:   request.ToolCaller,
		messages:   request.Messages,
		trackUsage: request.TrackUsage,
		profile:    profile,
	}
	s.factory = func() *ssestream.Stream[responses.ResponseStreamEventUnion] {
		return c.Responses.NewStreaming(ctx, s.request, opts...)
	}
	return s
}

func messagesContainImages(messages []proto.Message) bool {
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			return true
		}
	}
	return false
}

func deepSeekReasoningEffort(effort string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none":
		return "none", nil
	case "minimal", "low":
		return "low", nil
	case "medium", "high", "xhigh":
		return "high", nil
	case "max":
		return "max", nil
	default:
		return "", fmt.Errorf("DeepSeek Responses reasoning_effort %q is invalid; expected none, low, high, or max", effort)
	}
}

func (c *Client) responsesReasoningEffort() (string, bool, error) {
	if value, ok := c.config.ExtraParams["reasoning_effort"]; ok {
		effort, ok := value.(string)
		if !ok {
			return "", false, fmt.Errorf("OpenAI Responses reasoning_effort must be a string")
		}
		if effort == "" {
			return "", false, nil
		}
		return effort, true, nil
	}
	if c.config.ReasoningEffort == "" {
		return "", false, nil
	}
	return string(c.config.ReasoningEffort), true, nil
}

func responsesRequestOptions(extra map[string]any, profile ResponsesProfile) []option.RequestOption {
	opts := make([]option.RequestOption, 0, len(extra)*2)
	flattenMap("", extra, func(key string, value any) {
		switch key {
		case "reasoning_effort", "store", "previous_response_id", "conversation", "include", "stream":
			return
		case "background", "metadata", "prompt", "truncation", "service_tier", "prompt_cache_key", "safety_identifier":
			if profile == ResponsesProfileDeepSeek {
				return
			}
			opts = append(opts, option.WithJSONSet(key, value))
		default:
			opts = append(opts, option.WithJSONSet(key, value))
		}
	})
	return opts
}

type responseStream struct {
	done       bool
	stream     *ssestream.Stream[responses.ResponseStreamEventUnion]
	factory    func() *ssestream.Stream[responses.ResponseStreamEventUnion]
	request    responses.ResponseNewParams
	messages   []proto.Message
	toolCall   proto.ToolCaller
	trackUsage bool
	profile    ResponsesProfile

	terminal     *responses.Response
	terminalSeen bool
	incomplete   bool
	roundContent strings.Builder
	roundUsage   proto.TokenUsage
	usage        proto.TokenUsage
	requestErr   error
	responseErr  error
	customInputs map[string]*strings.Builder
}

type pendingResponseToolCall struct {
	id        string
	name      string
	arguments []byte
	custom    bool
}

func (s *responseStream) pendingToolCalls() []pendingResponseToolCall {
	if s.terminal == nil || s.incomplete || s.responseErr != nil {
		return nil
	}
	var calls []pendingResponseToolCall
	for _, item := range s.terminal.Output {
		if item.Type == "function_call" {
			call := item.AsFunctionCall()
			calls = append(calls, pendingResponseToolCall{id: call.CallID, name: call.Name, arguments: []byte(call.Arguments)})
		} else if item.Type == "custom_tool_call" {
			call, err := customToolCallFromRaw(item.RawJSON())
			if err != nil {
				s.responseErr = err
				return nil
			}
			input := call.Input
			if input == "" && s.customInputs != nil && s.customInputs[item.ID] != nil {
				input = s.customInputs[item.ID].String()
			}
			calls = append(calls, pendingResponseToolCall{
				id: call.CallID, name: canonicalCustomToolName(call.Name),
				arguments: customToolArguments(call.Name, input), custom: true,
			})
		}
	}
	return calls
}

// PendingToolCalls implements stream.Stream.
func (s *responseStream) PendingToolCalls() int { return len(s.pendingToolCalls()) }

func (s *responseStream) CallTools() []proto.ToolCallStatus {
	calls := s.pendingToolCalls()
	statuses := make([]proto.ToolCallStatus, 0, len(calls))
	for i, call := range calls {
		msg, status := stream.CallTool(
			proto.ToolCallRequest{ID: call.id, Index: i + 1, Total: len(calls), Name: call.name, Arguments: call.arguments},
			s.toolCall,
		)
		if call.custom && len(msg.ToolCalls) > 0 {
			msg.ToolCalls[0].Type = "custom"
		}
		s.messages = append(s.messages, msg)
		statuses = append(statuses, status)
	}
	return statuses
}

func (s *responseStream) Close() error {
	if s.stream == nil {
		return nil
	}
	return s.stream.Close() //nolint:wrapcheck
}

func (s *responseStream) Current() (proto.Chunk, error) {
	event := s.stream.Current()
	switch event.Type {
	case "response.reasoning_text.delta":
		var delta struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(event.RawJSON()), &delta); err != nil {
			return proto.Chunk{}, fmt.Errorf("decode DeepSeek reasoning event: %w", err)
		}
		return proto.Chunk{Thought: delta.Delta}, nil
	case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		var inputEvent struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
			Input  string `json:"input"`
		}
		if err := json.Unmarshal([]byte(event.RawJSON()), &inputEvent); err != nil {
			return proto.Chunk{}, fmt.Errorf("decode DeepSeek custom tool input event: %w", err)
		}
		if inputEvent.ItemID != "" {
			if s.customInputs == nil {
				s.customInputs = map[string]*strings.Builder{}
			}
			builder := s.customInputs[inputEvent.ItemID]
			if builder == nil {
				builder = &strings.Builder{}
				s.customInputs[inputEvent.ItemID] = builder
			}
			if inputEvent.Input != "" {
				builder.Reset()
				builder.WriteString(inputEvent.Input)
			} else {
				builder.WriteString(inputEvent.Delta)
			}
		}
		return proto.Chunk{}, stream.ErrNoContent
	}
	switch value := event.AsAny().(type) {
	case responses.ResponseTextDeltaEvent:
		s.roundContent.WriteString(value.Delta)
		return proto.Chunk{Content: value.Delta}, nil
	case responses.ResponseReasoningSummaryTextDeltaEvent:
		return proto.Chunk{Thought: value.Delta}, nil
	case responses.ResponseRefusalDeltaEvent:
		s.roundContent.WriteString(value.Delta)
		return proto.Chunk{Content: value.Delta}, nil
	case responses.ResponseCompletedEvent:
		s.setTerminal(value.Response, false)
		if sources := responseSources(value.Response); sources != "" {
			s.roundContent.WriteString(sources)
			return proto.Chunk{Content: sources}, nil
		}
	case responses.ResponseIncompleteEvent:
		s.setTerminal(value.Response, true)
	case responses.ResponseFailedEvent:
		s.setTerminal(value.Response, false)
		s.responseErr = responseFailureError(value.Response, responsesProviderName(s.profile))
	case responses.ResponseErrorEvent:
		s.responseErr = fmt.Errorf("%s Responses API error %s: %s", responsesProviderName(s.profile), value.Code, value.Message)
	case responses.ResponseWebSearchCallInProgressEvent, responses.ResponseWebSearchCallSearchingEvent:
		return proto.Chunk{Activity: "Searching the web"}, nil
	case responses.ResponseWebSearchCallCompletedEvent:
		return proto.Chunk{Activity: "Web search completed"}, nil
	}
	return proto.Chunk{}, stream.ErrNoContent
}

func (s *responseStream) setTerminal(response responses.Response, incomplete bool) {
	s.terminal = &response
	s.terminalSeen = true
	s.incomplete = incomplete
	if s.trackUsage {
		s.roundUsage = proto.TokenUsage{
			InputTokens:           response.Usage.InputTokens,
			CachedInputTokens:     response.Usage.InputTokensDetails.CachedTokens,
			OutputTokens:          response.Usage.OutputTokens,
			ReasoningOutputTokens: response.Usage.OutputTokensDetails.ReasoningTokens,
			TotalTokens:           response.Usage.TotalTokens,
		}
	}
}

func responseSources(response responses.Response) string {
	seen := map[string]struct{}{}
	var sources []string
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				continue
			}
			for _, annotation := range content.Annotations {
				if annotation.Type != "url_citation" || annotation.URL == "" {
					continue
				}
				if _, ok := seen[annotation.URL]; ok {
					continue
				}
				seen[annotation.URL] = struct{}{}
				title := strings.TrimSpace(annotation.Title)
				if title == "" {
					title = annotation.URL
				}
				sources = append(sources, fmt.Sprintf("- [%s](%s)", title, annotation.URL))
			}
		}
	}
	if len(sources) == 0 {
		return ""
	}
	return "\n\nSources:\n" + strings.Join(sources, "\n")
}

func responsesProviderName(profile ResponsesProfile) string {
	if profile == ResponsesProfileDeepSeek {
		return "DeepSeek"
	}
	return "OpenAI"
}

func responseFailureError(response responses.Response, provider string) error {
	if response.Error.Message != "" {
		if response.Error.Code != "" {
			return fmt.Errorf("%s Responses API failed (%s): %s", provider, response.Error.Code, response.Error.Message)
		}
		return fmt.Errorf("%s Responses API failed: %s", provider, response.Error.Message)
	}
	return fmt.Errorf("%s Responses API failed", provider)
}

func (s *responseStream) Err() error {
	if s.requestErr != nil {
		return s.requestErr
	}
	if s.responseErr != nil {
		return s.responseErr
	}
	if s.stream == nil {
		return nil
	}
	return s.stream.Err() //nolint:wrapcheck
}

func (s *responseStream) Messages() []proto.Message { return s.messages }

func (s *responseStream) Usage() proto.TokenUsage { return s.usage }

func (s *responseStream) Next() bool {
	if s.requestErr != nil || s.responseErr != nil {
		return false
	}
	if s.done {
		if err := s.startFollowup(); err != nil {
			s.requestErr = err
			return false
		}
	}
	if s.stream.Next() {
		return true
	}
	if err := s.stream.Err(); err != nil {
		return false
	}

	s.done = true
	s.usage.Add(s.roundUsage)
	s.roundUsage = proto.TokenUsage{}
	if !s.terminalSeen {
		s.responseErr = errResponsesStreamEnded
		return false
	}
	if s.responseErr != nil || s.terminal == nil {
		return false
	}
	if s.incomplete {
		content := s.roundContent.String()
		if content == "" {
			content = responseVisibleText(*s.terminal)
		}
		s.messages = append(s.messages, proto.Message{
			Role:    proto.RoleAssistant,
			Content: content,
		})
		return false
	}
	msg, err := responseToProtoMessage(*s.terminal, s.roundContent.String(), s.profile)
	if err != nil {
		s.requestErr = err
		return false
	}
	s.messages = append(s.messages, msg)
	return false
}

func (s *responseStream) startFollowup() error {
	s.done = false
	input, err := fromProtoResponseInput(s.messages, s.profile)
	if err != nil {
		return err
	}
	s.request.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}
	s.stream = s.factory()
	s.terminal = nil
	s.terminalSeen = false
	s.incomplete = false
	s.roundContent.Reset()
	s.customInputs = nil
	s.responseErr = nil
	return nil
}
