// Package google implements [stream.Stream] for Google.
package google

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

var _ stream.Client = &Client{}

const emptyMessagesLimit uint = 300

var (
	googleHeaderData = []byte("data: ")
	errorPrefix      = []byte(`event: error`)
)

// Config represents the configuration for the Google API client.
type Config struct {
	BaseURL        string
	HTTPClient     *http.Client
	AuthToken      string
	ThinkingBudget int
	// ThinkingBudgetExplicit forces ThinkingBudget to be sent even when it is
	// zero, so callers can explicitly disable Gemini's thinking (which is on
	// by default) by setting budget=0 + Explicit=true.
	ThinkingBudgetExplicit bool
}

// DefaultConfig returns the default configuration for the Google API client.
func DefaultConfig(model, authToken string) Config {
	return Config{
		BaseURL: fmt.Sprintf(
			"https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse",
			url.PathEscape(model),
		),
		HTTPClient: &http.Client{},
		AuthToken:  authToken,
	}
}

// Part is a datatype containing media that is part of a multi-part Content message.
//
// When Gemini's thinking is enabled, the API returns separate parts for the
// model's internal reasoning. Such parts carry Thought=true and the reasoning
// text inside Text; non-thought parts contain the response intended for the
// user. Thought is *bool so a missing field (the common case for non-thought
// parts) is distinguishable from an explicit false. See
// https://ai.google.dev/gemini-api/docs/thinking
type Part struct {
	Text       string `json:"text,omitempty"`
	InlineData *Blob  `json:"inlineData,omitempty"`
	Thought    *bool  `json:"thought,omitempty"`
}

// Blob contains raw media bytes to be sent inline to the model.
type Blob struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64-encoded
}

// Content is the base structured datatype containing multi-part content of a message.
type Content struct {
	Parts []Part `json:"parts,omitempty"`
	Role  string `json:"role,omitempty"`
}

// ThinkingConfig - for more details see https://ai.google.dev/gemini-api/docs/thinking#rest .
type ThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

// GenerationConfig are the options for model generation and outputs. Not all parameters are configurable for every model.
type GenerationConfig struct {
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	CandidateCount   uint            `json:"candidateCount,omitempty"`
	MaxOutputTokens  uint            `json:"maxOutputTokens,omitempty"`
	Temperature      float64         `json:"temperature,omitempty"`
	TopP             float64         `json:"topP,omitempty"`
	TopK             int64           `json:"topK,omitempty"`
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// MessageCompletionRequest represents the valid parameters and value options for the request.
type MessageCompletionRequest struct {
	Contents          []Content        `json:"contents,omitempty"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
	GenerationConfig  GenerationConfig `json:"generationConfig,omitempty"`
}

// RequestBuilder is an interface for building HTTP requests for the Google API.
type RequestBuilder interface {
	Build(ctx context.Context, method, url string, body any, header http.Header) (*http.Request, error)
}

// NewRequestBuilder creates a new HTTPRequestBuilder.
func NewRequestBuilder() *HTTPRequestBuilder {
	return &HTTPRequestBuilder{
		marshaller: &JSONMarshaller{},
	}
}

// googleErrorResponse represents the nested error JSON structure returned by the Gemini API.
type googleErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// APIError represents an error response from the Google API.
type APIError struct {
	Message    string `json:"message"`
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("google API error: %s (HTTP %d)", e.Message, e.StatusCode)
}

// Client is a client for the Google API.
type Client struct {
	config Config

	requestBuilder RequestBuilder
}

// Capabilities reports Google backend features. The Google adapter
// does not implement tool/function calling (CallTools is a no-op).
func (c *Client) Capabilities() stream.Capabilities { return stream.Capabilities{Tools: false} }

// Request implements stream.Client.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	stream := new(Stream)
	sysInstr, contents := fromProtoMessages(request.Messages)
	body := MessageCompletionRequest{
		Contents:          contents,
		SystemInstruction: sysInstr,
		GenerationConfig: GenerationConfig{
			ResponseMimeType: "",
			CandidateCount:   1,
			StopSequences:    request.Stop,
			MaxOutputTokens:  4096,
		},
	}

	if request.Temperature != nil {
		body.GenerationConfig.Temperature = *request.Temperature
	}
	if request.TopP != nil {
		body.GenerationConfig.TopP = *request.TopP
	}
	if request.TopK != nil {
		body.GenerationConfig.TopK = *request.TopK
	}

	if request.MaxTokens != nil {
		body.GenerationConfig.MaxOutputTokens = uint(*request.MaxTokens) //nolint:gosec
	}

	if c.config.ThinkingBudget != 0 || c.config.ThinkingBudgetExplicit {
		body.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			ThinkingBudget: c.config.ThinkingBudget,
		}
	}

	req, err := c.newRequest(ctx, http.MethodPost, c.config.BaseURL, withBody(body))
	if err != nil {
		stream.err = err
		// Mark the stream finished here so callers do not advance via
		// Next() into Current(), which would dereference the nil reader
		// and panic. The companion error path below already sets this;
		// the symmetric guard keeps both failure modes consistent.
		stream.isFinished = true
		return stream
	}

	stream, err = googleSendRequestStream(c, req)
	if err != nil {
		stream.err = err
		stream.isFinished = true
		return stream
	}
	stream.messages = append([]proto.Message(nil), request.Messages...)
	return stream
}

// New creates a new Client with the given configuration.
func New(config Config) *Client {
	return &Client{
		config:         config,
		requestBuilder: NewRequestBuilder(),
	}
}

func (c *Client) newRequest(ctx context.Context, method, url string, setters ...requestOption) (*http.Request, error) {
	// Default Options
	args := &requestOptions{
		body:   MessageCompletionRequest{},
		header: make(http.Header),
	}
	for _, setter := range setters {
		setter(args)
	}
	req, err := c.requestBuilder.Build(ctx, method, url, args.body, args.header)
	if err != nil {
		return new(http.Request), err
	}
	return req, nil
}

func (c *Client) handleErrorResp(resp *http.Response) error {
	var errRes googleErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errRes); err != nil {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    err.Error(),
		}
	}
	message := errRes.Error.Message
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    message,
	}
}

// Candidate represents a response candidate generated from the model.
type Candidate struct {
	Content      Content `json:"content,omitempty"`
	FinishReason string  `json:"finishReason,omitempty"`
	TokenCount   uint    `json:"tokenCount,omitempty"`
	Index        uint    `json:"index,omitempty"`
}

// CompletionMessageResponse represents a response to an Google completion message.
type CompletionMessageResponse struct {
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Stream struct represents a stream of messages from the Google API.
type Stream struct {
	isFinished bool

	reader      *bufio.Reader
	response    *http.Response
	err         error
	unmarshaler Unmarshaler
	message     string
	messages    []proto.Message
}

// CallTools implements stream.Stream.
func (s *Stream) CallTools() []proto.ToolCallStatus {
	// No tool calls in Gemini/Google API yet.
	return nil
}

// Err implements stream.Stream.
func (s *Stream) Err() error { return s.err }

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message {
	messages := append([]proto.Message(nil), s.messages...)
	if s.message != "" {
		messages = append(messages, proto.Message{
			Role:    proto.RoleAssistant,
			Content: s.message,
		})
	}
	return messages
}

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	return !s.isFinished
}

// Close closes the stream.
func (s *Stream) Close() error {
	if s.response == nil {
		return nil
	}
	return s.response.Body.Close() //nolint:wrapcheck
}

// Current implements stream.Stream.
//
//nolint:gocognit
func (s *Stream) Current() (proto.Chunk, error) {
	var (
		emptyMessagesCount uint
		hasError           bool
	)

	for {
		rawLine, readErr := s.reader.ReadBytes('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				s.isFinished = true
				return proto.Chunk{}, stream.ErrNoContent // signals end of stream, not a real error
			}
			return proto.Chunk{}, fmt.Errorf("googleStreamReader.processLines: %w", readErr)
		}

		noSpaceLine := bytes.TrimSpace(rawLine)

		if bytes.HasPrefix(noSpaceLine, errorPrefix) {
			hasError = true
			// NOTE: Continue to the next event to get the error data.
			continue
		}

		if !bytes.HasPrefix(noSpaceLine, googleHeaderData) || hasError {
			if hasError {
				noSpaceLine = bytes.TrimPrefix(noSpaceLine, googleHeaderData)
				return proto.Chunk{}, fmt.Errorf("googleStreamReader.processLines: %s", noSpaceLine)
			}
			emptyMessagesCount++
			if emptyMessagesCount > emptyMessagesLimit {
				return proto.Chunk{}, ErrTooManyEmptyStreamMessages
			}
			continue
		}

		noPrefixLine := bytes.TrimPrefix(noSpaceLine, googleHeaderData)

		var chunk CompletionMessageResponse
		unmarshalErr := s.unmarshaler.Unmarshal(noPrefixLine, &chunk)
		if unmarshalErr != nil {
			return proto.Chunk{}, fmt.Errorf("googleStreamReader.processLines: %w", unmarshalErr)
		}
		if len(chunk.Candidates) == 0 {
			return proto.Chunk{}, stream.ErrNoContent
		}
		parts := chunk.Candidates[0].Content.Parts
		if len(parts) == 0 {
			return proto.Chunk{}, stream.ErrNoContent
		}

		var text, thought string
		for _, part := range parts {
			if part.Text == "" {
				continue
			}
			// Gemini marks reasoning parts with Thought=true; the reasoning
			// text lives in Text on the same part. Non-thought parts contain
			// the response intended for the user. Dispatch each part's Text
			// to exactly one of {content, thought}; never both.
			if part.Thought != nil && *part.Thought {
				thought += part.Text
				continue
			}
			text += part.Text
		}
		// Persist only the user-facing answer for replay in subsequent turns;
		// internal reasoning must not contaminate Messages() history.
		s.message += text

		return proto.Chunk{Content: text, Thought: thought}, nil
	}
}

func googleSendRequestStream(client *Client, req *http.Request) (*Stream, error) {
	req.Header.Set("content-type", "application/json")
	if client.config.AuthToken != "" {
		req.Header.Set("x-goog-api-key", client.config.AuthToken)
	}

	resp, err := client.config.HTTPClient.Do(req) //nolint:bodyclose // body is closed in stream.Close()
	if err != nil {
		return new(Stream), err
	}
	if isFailureStatusCode(resp) {
		err := client.handleErrorResp(resp)
		_ = resp.Body.Close()
		return new(Stream), err
	}
	return &Stream{
		reader:      bufio.NewReader(resp.Body),
		response:    resp,
		unmarshaler: &JSONUnmarshaler{},
	}, nil
}
