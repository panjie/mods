// Package ollama implements [stream.Stream] for Ollama.
package ollama

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	api "github.com/panjie/mods/internal/ollamaapi"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

var _ stream.Client = &Client{}

// Config represents the configuration for the Ollama API client.
type Config struct {
	BaseURL            string
	HTTPClient         *http.Client
	EmptyMessagesLimit uint
}

// DefaultConfig returns the default configuration for the Ollama API client.
func DefaultConfig() Config {
	return Config{
		BaseURL:    "http://localhost:11434/",
		HTTPClient: &http.Client{},
	}
}

// Client ollama client.
type Client struct {
	*api.Client
}

// New creates a new [Client] with the given [Config].
func New(config Config) (*Client, error) {
	u, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	client := api.NewClient(u, config.HTTPClient)
	return &Client{
		Client: client,
	}, nil
}

// Capabilities reports Ollama backend features. The Ollama adapter
// supports tool/function calling via CallTools.
func (c *Client) Capabilities() stream.Capabilities { return stream.Capabilities{Tools: true} }

// Request implements stream.Client.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	s := &Stream{
		toolCall:   request.ToolCaller,
		trackUsage: request.TrackUsage,
	}
	body := newChatRequest(request)
	s.request = body
	s.messages = request.Messages
	s.factory = func() {
		s.mu.Lock()
		s.run++
		run := s.run
		s.done = false
		s.err = nil
		s.closed = false
		s.respCh = make(chan api.ChatResponse, 1)
		ch := s.respCh
		s.mu.Unlock()
		go func() {
			s.finish(run, ch, c.Chat(ctx, &s.request, func(resp api.ChatResponse) error {
				return s.fn(run, ch, resp)
			}))
		}()
	}
	s.factory()
	return s
}

func newChatRequest(request proto.Request) api.ChatRequest {
	b := true
	body := api.ChatRequest{
		Model:    request.Model,
		Messages: fromProtoMessages(request.Messages),
		Stream:   &b,
		Tools:    fromToolSpecs(request.Tools),
		Options:  map[string]any{},
	}

	if request.MaxTokens != nil {
		body.Options["num_predict"] = *request.MaxTokens
	}
	if request.Temperature != nil {
		body.Options["temperature"] = *request.Temperature
	}
	return body
}

// Stream ollama stream.
type Stream struct {
	mu         sync.Mutex
	closed     bool
	request    api.ChatRequest
	err        error
	done       bool
	run        uint64
	factory    func()
	respCh     chan api.ChatResponse
	message    api.Message
	content    strings.Builder
	toolCall   func(name string, data []byte) (string, error)
	messages   []proto.Message
	trackUsage bool
	usage      proto.TokenUsage
}

func (s *Stream) fn(run uint64, ch chan api.ChatResponse, resp api.ChatResponse) error {
	defer func() { _ = recover() }()
	s.mu.Lock()
	if s.closed || s.run != run {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	ch <- resp
	return nil
}

func (s *Stream) finish(run uint64, ch chan api.ChatResponse, err error) {
	s.mu.Lock()
	if s.run != run {
		s.mu.Unlock()
		return
	}
	if err != nil {
		s.err = err
	}
	if !s.closed {
		s.closed = true
		close(ch)
	}
	s.mu.Unlock()
}

// CallTools implements stream.Stream.
func (s *Stream) CallTools() []proto.ToolCallStatus {
	statuses := make([]proto.ToolCallStatus, 0, len(s.message.ToolCalls))
	for _, call := range s.message.ToolCalls {
		msg, status := stream.CallTool(
			strconv.Itoa(call.Function.Index),
			call.Function.Name,
			[]byte(call.Function.Arguments.String()),
			s.toolCall,
		)
		s.request.Messages = append(s.request.Messages, fromProtoMessage(msg))
		s.messages = append(s.messages, msg)
		statuses = append(statuses, status)
	}
	if len(statuses) > 0 {
		s.message = api.Message{}
		s.content.Reset()
		s.factory()
	}
	return statuses
}

// Close implements stream.Stream.
func (s *Stream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.respCh)
	s.done = true
	s.mu.Unlock()
	return nil
}

// Current implements stream.Stream.
func (s *Stream) Current() (proto.Chunk, error) {
	resp, ok := <-s.respCh
	if !ok {
		s.mu.Lock()
		s.done = true
		s.mu.Unlock()
		return proto.Chunk{}, stream.ErrNoContent
	}
	chunk := proto.Chunk{
		Content: resp.Message.Content,
	}
	s.mu.Lock()
	s.content.WriteString(resp.Message.Content)
	s.message.Content = s.content.String()
	s.message.ToolCalls = append(s.message.ToolCalls, resp.Message.ToolCalls...)
	if resp.Done {
		if s.trackUsage {
			s.usage.Add(proto.TokenUsage{
				InputTokens:  int64(resp.PromptEvalCount),
				OutputTokens: int64(resp.EvalCount),
				TotalTokens:  int64(resp.PromptEvalCount + resp.EvalCount),
			})
		}
		s.done = true
	}
	s.mu.Unlock()
	return chunk, nil
}

// Err implements stream.Stream.
func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message { return s.messages }

// Usage implements stream.Stream.
func (s *Stream) Usage() proto.TokenUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false
	}
	if s.done {
		s.messages = append(s.messages, toProtoMessage(s.message))
		s.request.Messages = append(s.request.Messages, s.message)
		return false
	}
	return true
}
