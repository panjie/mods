package openai

import (
	"context"
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
	if request.MessageBudgeter != nil {
		messages, err := request.MessageBudgeter(request.Messages)
		if err != nil {
			return &responseStream{requestErr: err, messages: request.Messages}
		}
		request.Messages = messages
	}
	input, err := fromProtoResponseInput(request.Messages)
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
		Store: openai.Bool(false),
		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		},
		User: openai.String(request.User),
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
		body.Reasoning.Effort = shared.ReasoningEffort(effort)
	}
	if c.config.ThinkTags {
		body.Reasoning.Summary = shared.ReasoningSummaryAuto
	}

	opts := responsesRequestOptions(c.config.ExtraParams)
	s := &responseStream{
		stream:     c.Responses.NewStreaming(ctx, body, opts...),
		request:    body,
		toolCall:   request.ToolCaller,
		messages:   request.Messages,
		budgeter:   request.MessageBudgeter,
		trackUsage: request.TrackUsage,
	}
	s.factory = func() *ssestream.Stream[responses.ResponseStreamEventUnion] {
		return c.Responses.NewStreaming(ctx, s.request, opts...)
	}
	return s
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

func responsesRequestOptions(extra map[string]any) []option.RequestOption {
	opts := make([]option.RequestOption, 0, len(extra)*2)
	flattenMap("", extra, func(key string, value any) {
		switch key {
		case "reasoning_effort", "store", "previous_response_id", "conversation", "include", "stream":
			return
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
	toolCall   func(name string, data []byte) (string, error)
	budgeter   proto.MessageBudgeter
	trackUsage bool

	terminal     *responses.Response
	terminalSeen bool
	incomplete   bool
	roundContent strings.Builder
	roundUsage   proto.TokenUsage
	usage        proto.TokenUsage
	requestErr   error
	responseErr  error
}

func (s *responseStream) pendingToolCalls() []responses.ResponseFunctionToolCall {
	if s.terminal == nil || s.incomplete || s.responseErr != nil {
		return nil
	}
	var calls []responses.ResponseFunctionToolCall
	for _, item := range s.terminal.Output {
		if item.Type == "function_call" {
			calls = append(calls, item.AsFunctionCall())
		}
	}
	return calls
}

func (s *responseStream) CallTools() []proto.ToolCallStatus {
	calls := s.pendingToolCalls()
	statuses := make([]proto.ToolCallStatus, 0, len(calls))
	for _, call := range calls {
		msg, status := stream.CallTool(
			call.CallID,
			call.Name,
			[]byte(call.Arguments),
			s.toolCall,
		)
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
	case responses.ResponseIncompleteEvent:
		s.setTerminal(value.Response, true)
	case responses.ResponseFailedEvent:
		s.setTerminal(value.Response, false)
		s.responseErr = responseFailureError(value.Response)
	case responses.ResponseErrorEvent:
		s.responseErr = fmt.Errorf("OpenAI Responses API error %s: %s", value.Code, value.Message)
	}
	return proto.Chunk{}, stream.ErrNoContent
}

func (s *responseStream) setTerminal(response responses.Response, incomplete bool) {
	s.terminal = &response
	s.terminalSeen = true
	s.incomplete = incomplete
	if s.trackUsage {
		s.roundUsage = proto.TokenUsage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
			TotalTokens:  response.Usage.TotalTokens,
		}
	}
}

func responseFailureError(response responses.Response) error {
	if response.Error.Message != "" {
		if response.Error.Code != "" {
			return fmt.Errorf("OpenAI Responses API failed (%s): %s", response.Error.Code, response.Error.Message)
		}
		return fmt.Errorf("OpenAI Responses API failed: %s", response.Error.Message)
	}
	return errors.New("OpenAI Responses API failed")
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
	msg, err := responseToProtoMessage(*s.terminal, s.roundContent.String())
	if err != nil {
		s.requestErr = err
		return false
	}
	s.messages = append(s.messages, msg)
	return false
}

func (s *responseStream) startFollowup() error {
	s.done = false
	if s.budgeter != nil {
		messages, err := s.budgeter(s.messages)
		if err != nil {
			return err
		}
		s.messages = messages
	}
	input, err := fromProtoResponseInput(s.messages)
	if err != nil {
		return err
	}
	s.request.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}
	s.stream = s.factory()
	s.terminal = nil
	s.terminalSeen = false
	s.incomplete = false
	s.roundContent.Reset()
	s.responseErr = nil
	return nil
}
