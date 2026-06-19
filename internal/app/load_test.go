package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	ctx := context.Background()
	const content = "just text"
	t.Run("normal msg", func(t *testing.T) {
		msg, err := loadMsg(ctx, content)
		require.NoError(t, err)
		require.Equal(t, content, msg)
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "foo.txt")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		msg, err := loadMsg(ctx, "file://"+path)
		require.NoError(t, err)
		require.Equal(t, content, msg)
	})

	t.Run("http url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("MIT License"))
		}))
		defer srv.Close()
		msg, err := loadMsg(ctx, srv.URL)
		require.NoError(t, err)
		require.Contains(t, msg, "MIT License")
	})

	t.Run("https url", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("MIT License"))
		}))
		defer srv.Close()
		oldTransport := http.DefaultTransport
		http.DefaultTransport = srv.Client().Transport
		t.Cleanup(func() { http.DefaultTransport = oldTransport })
		msg, err := loadMsg(ctx, srv.URL)
		require.NoError(t, err)
		require.Contains(t, msg, "MIT License")
	})

	t.Run("http error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		}))
		defer srv.Close()
		_, err := loadMsg(ctx, srv.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "HTTP 404")
	})

	t.Run("http response too large", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxLoadMsgBytes+1)))
		}))
		defer srv.Close()
		_, err := loadMsg(ctx, srv.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds")
	})
}
