// Package deepseektools implements DeepSeek's standalone application APIs.
// These services are independent of the model used for the conversation.
package deepseektools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const MaxResponseBytes = 16 << 20

// Client never retries a submitted request: cancelled uploads may still be billed.
// HTTP is optional, and allows deterministic wire tests without provider access.
type Client struct {
	APIKey string
	HTTP   *http.Client
}

type Page struct {
	Index        int    `json:"index"`
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
}
type OCRResult struct {
	PageCount int    `json:"page_count"`
	Pages     []Page `json:"pages"`
}

func (c Client) OCR(ctx context.Context, filename string, data []byte, purpose string) (OCRResult, error) {
	var result OCRResult
	err := c.upload(ctx, "ocr", filename, data, purpose, &result)
	if err != nil {
		return result, err
	}
	if result.PageCount < 1 || result.PageCount > 1000 || result.PageCount != len(result.Pages) {
		return OCRResult{}, fmt.Errorf("DeepSeek OCR returned invalid page count")
	}
	for i, p := range result.Pages {
		if p.Index != i || (p.FinishReason != "stop" && p.FinishReason != "length") {
			return OCRResult{}, fmt.Errorf("DeepSeek OCR returned invalid page metadata")
		}
	}
	return result, nil
}

type Transcript struct {
	Text           string `json:"text"`
	DurationMillis int64  `json:"duration_ms"`
}

func (c Client) Transcribe(ctx context.Context, filename string, data []byte) (Transcript, error) {
	var result Transcript
	err := c.upload(ctx, "asr", filename, data, "", &result)
	if err == nil && (result.DurationMillis <= 0 || result.DurationMillis > 120000) {
		err = fmt.Errorf("DeepSeek ASR returned invalid duration")
	}
	return result, err
}

type WebPage struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (c Client) Fetch(ctx context.Context, url string) (WebPage, error) {
	body, _ := json.Marshal(map[string]string{"url": url})
	var result WebPage
	err := c.request(ctx, "fetch", "application/json", bytes.NewReader(body), &result)
	return result, err
}

func (c Client) upload(ctx context.Context, operation, filename string, data []byte, purpose string, out any) error {
	// Only multipart framing is copied; the bounded file bytes are streamed between
	// its header and footer instead of allocating another full file buffer.
	var framing bytes.Buffer
	w := multipart.NewWriter(&framing)
	if purpose != "" {
		if err := w.WriteField("purpose", purpose); err != nil {
			return err
		}
	}
	if _, err := w.CreateFormFile("file", filepath.Base(filename)); err != nil {
		return err
	}
	header := append([]byte(nil), framing.Bytes()...)
	framing.Reset()
	if err := w.Close(); err != nil {
		return err
	}
	return c.request(ctx, operation, w.FormDataContentType(), io.MultiReader(bytes.NewReader(header), bytes.NewReader(data), bytes.NewReader(framing.Bytes())), out)
}

func (c Client) request(ctx context.Context, operation, contentType string, body io.Reader, out any) error {
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("DeepSeek tools require deepseek-tools-api-key or DEEPSEEK_API_KEY")
	}
	ctx, cancel := context.WithTimeout(ctx, 185*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/v1/tools/"+operation, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	client := http.Client{Timeout: 185 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// Never forward uploaded files or credentials to a redirected endpoint.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("DeepSeek %s request failed: %w", operation, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DeepSeek %s returned HTTP %d (not retried)", operation, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("DeepSeek %s response read failed: %w", operation, err)
	}
	if len(b) > MaxResponseBytes {
		return fmt.Errorf("DeepSeek %s response exceeds 16 MiB", operation)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("DeepSeek %s returned invalid JSON", operation)
	}
	return nil
}
