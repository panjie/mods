package deepseektools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStandaloneWire(t *testing.T) {
	calls := 0
	c := Client{APIKey: "secret", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.Equal(t, "api.deepseek.com", r.URL.Host)
		require.Equal(t, "POST", r.Method)
		var body string
		switch r.URL.Path {
		case "/v1/tools/ocr", "/v1/tools/asr":
			mr, err := r.MultipartReader()
			require.NoError(t, err)
			if strings.HasSuffix(r.URL.Path, "ocr") {
				part, err := mr.NextPart()
				require.NoError(t, err)
				require.Equal(t, "purpose", part.FormName())
				data, err := io.ReadAll(part)
				require.NoError(t, err)
				require.Equal(t, "text_recognition", string(data))
			}
			part, err := mr.NextPart()
			require.NoError(t, err)
			require.Equal(t, "file", part.FormName())
			require.Equal(t, "sample", part.FileName())
			data, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, "file content", string(data))
			_, err = mr.NextPart()
			require.ErrorIs(t, err, io.EOF)
			if strings.HasSuffix(r.URL.Path, "ocr") {
				body = "\n\n" + `{"page_count":1,"pages":[{"index":0,"text":"文档","finish_reason":"length"}]}`
			} else {
				body = `{"text":"speech","duration_ms":1000}`
			}
		case "/v1/tools/fetch":
			body = `{"title":"title","content":"# Markdown"}`
		default:
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	ocr, err := c.OCR(context.Background(), "sample", []byte("file content"), "text_recognition")
	require.NoError(t, err)
	require.Equal(t, "length", ocr.Pages[0].FinishReason)
	asr, err := c.Transcribe(context.Background(), "sample", []byte("file content"))
	require.NoError(t, err)
	require.Equal(t, "speech", asr.Text)
	page, err := c.Fetch(context.Background(), "https://example.com")
	require.NoError(t, err)
	require.Equal(t, "# Markdown", page.Content)
	require.Equal(t, 3, calls)
}

func TestStandaloneFailuresNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"rate limit", "secret provider error", 429},
		{"redirect", "", 302},
		{"invalid JSON", "{", 200},
		{"mismatched pages", `{"page_count":2,"pages":[]}`, 200},
		{"bad index", `{"page_count":1,"pages":[{"index":9,"finish_reason":"stop"}]}`, 200},
		{"unknown finish", `{"page_count":1,"pages":[{"index":0,"finish_reason":"error"}]}`, 200},
		{"oversized", strings.Repeat(" ", MaxResponseBytes+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Client{APIKey: "key", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}}
			_, err := c.OCR(context.Background(), "x", []byte("x"), "text_recognition")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret provider error")
			require.Equal(t, 1, calls)
		})
	}
	_, err := (Client{}).Fetch(context.Background(), "https://example.com")
	require.ErrorContains(t, err, "require")
}
