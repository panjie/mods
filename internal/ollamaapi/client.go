package ollamaapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

const maxStreamBuffer = 8 * 1024 * 1024

type Client struct {
	base *url.URL
	http *http.Client
}

func NewClient(base *url.URL, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{base: base, http: client}
}

type ChatResponseFunc func(ChatResponse) error

func (c *Client) Chat(ctx context.Context, req *ChatRequest, fn ChatResponseFunc) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath("/api/chat").String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/x-ndjson")

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamBuffer)
	if response.StatusCode >= http.StatusBadRequest {
		// The HTTP status is authoritative even when the server sends no body.
		var errorResponse struct {
			Error     string `json:"error"`
			SigninURL string `json:"signin_url"`
		}
		if scanner.Scan() {
			if err := json.Unmarshal(scanner.Bytes(), &errorResponse); err != nil {
				errorResponse.Error = scanner.Text()
			}
		} else if err := scanner.Err(); err != nil {
			errorResponse.Error = err.Error()
		}
		if response.StatusCode == http.StatusUnauthorized {
			return AuthorizationError{StatusCode: response.StatusCode, Status: response.Status, SigninURL: errorResponse.SigninURL}
		}
		return StatusError{StatusCode: response.StatusCode, Status: response.Status, ErrorMessage: errorResponse.Error}
	}
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var errorResponse struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(line, &errorResponse); err != nil {
			return err
		}
		if errorResponse.Error != "" {
			return errors.New(errorResponse.Error)
		}
		var chat ChatResponse
		if err := json.Unmarshal(line, &chat); err != nil {
			return err
		}
		if err := fn(chat); err != nil {
			return err
		}
	}
	return scanner.Err()
}
