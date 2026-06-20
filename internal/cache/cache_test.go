package cache

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestCache(t *testing.T) {
	t.Run("read non-existent", func(t *testing.T) {
		cache, err := NewConversations(t.TempDir())
		require.NoError(t, err)
		err = cache.Read("super-fake", &[]proto.Message{})
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("write", func(t *testing.T) {
		cache, err := NewConversations(t.TempDir())
		require.NoError(t, err)
		messages := []proto.Message{
			{
				Role:    proto.RoleUser,
				Content: "first 4 natural numbers",
			},
			{
				Role:    proto.RoleAssistant,
				Content: "1, 2, 3, 4",
			},
		}
		require.NoError(t, cache.Write("fake", &messages))

		result := []proto.Message{}
		require.NoError(t, cache.Read("fake", &result))

		require.ElementsMatch(t, messages, result)
	})

	t.Run("delete", func(t *testing.T) {
		cache, err := NewConversations(t.TempDir())
		require.NoError(t, err)
		cache.Write("fake", &[]proto.Message{})
		require.NoError(t, cache.Delete("fake"))
		require.ErrorIs(t, cache.Read("fake", nil), os.ErrNotExist)
	})

	t.Run("invalid id", func(t *testing.T) {
		t.Run("write", func(t *testing.T) {
			cache, err := NewConversations(t.TempDir())
			require.NoError(t, err)
			require.ErrorIs(t, cache.Write("", nil), errInvalidID)
		})
		t.Run("delete", func(t *testing.T) {
			cache, err := NewConversations(t.TempDir())
			require.NoError(t, err)
			require.ErrorIs(t, cache.Delete(""), errInvalidID)
		})
		t.Run("read", func(t *testing.T) {
			cache, err := NewConversations(t.TempDir())
			require.NoError(t, err)
			require.ErrorIs(t, cache.Read("", nil), errInvalidID)
		})
	})
}

func TestExpiringCache(t *testing.T) {
	t.Run("write and read", func(t *testing.T) {
		cache, err := NewExpiring[string](t.TempDir())
		require.NoError(t, err)

		// Write a value with expiry
		data := "test data"
		expiresAt := time.Now().Add(time.Hour).Unix()
		err = cache.Write("test", expiresAt, func(w io.Writer) error {
			_, err := w.Write([]byte(data))
			return err
		})
		require.NoError(t, err)

		// Read it back
		var result string
		err = cache.Read("test", func(r io.Reader) error {
			b, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			result = string(b)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, data, result)
	})

	t.Run("expired token", func(t *testing.T) {
		cache, err := NewExpiring[string](t.TempDir())
		require.NoError(t, err)

		// Write a value that's already expired
		data := "test data"
		expiresAt := time.Now().Add(-time.Hour).Unix() // expired 1 hour ago
		err = cache.Write("test", expiresAt, func(w io.Writer) error {
			_, err := w.Write([]byte(data))
			return err
		})
		require.NoError(t, err)

		// Try to read it
		err = cache.Read("test", func(r io.Reader) error {
			return nil
		})
		require.Error(t, err)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("overwrite token", func(t *testing.T) {
		cache, err := NewExpiring[string](t.TempDir())
		require.NoError(t, err)

		// Write initial value
		data1 := "test data 1"
		expiresAt1 := time.Now().Add(time.Hour).Unix()
		err = cache.Write("test", expiresAt1, func(w io.Writer) error {
			_, err := w.Write([]byte(data1))
			return err
		})
		require.NoError(t, err)

		// Write new value
		data2 := "test data 2"
		expiresAt2 := time.Now().Add(2 * time.Hour).Unix()
		err = cache.Write("test", expiresAt2, func(w io.Writer) error {
			_, err := w.Write([]byte(data2))
			return err
		})
		require.NoError(t, err)

		// Read it back - should get the new value
		var result string
		err = cache.Read("test", func(r io.Reader) error {
			b, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			result = string(b)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, data2, result)
	})
}

// TestCacheWriteAtomic verifies the tmp+fsync+rename write strategy. The key
// invariant: a write that fails mid-stream must NOT corrupt or truncate an
// existing valid entry, and must not leave a stale .tmp file behind.
func TestCacheWriteAtomic(t *testing.T) {
	t.Run("preserves existing entry on writeFn failure", func(t *testing.T) {
		cache, err := New[[]byte](t.TempDir(), ConversationCache)
		require.NoError(t, err)

		// Seed a valid entry.
		require.NoError(t, cache.Write("id1", func(w io.Writer) error {
			_, err := w.Write([]byte("original"))
			return err
		}))

		// Attempt a write that fails partway. Under the previous non-atomic
		// os.Create implementation this would have truncated "id1" to zero
		// bytes before the failure; the atomic strategy must leave it intact.
		writeErr := cache.Write("id1", func(w io.Writer) error {
			_, _ = w.Write([]byte("partial-junk"))
			return errors.New("simulated write failure")
		})
		require.Error(t, writeErr)

		// Original content survives.
		var content bytes.Buffer
		require.NoError(t, cache.Read("id1", func(r io.Reader) error {
			_, err := content.ReadFrom(r)
			return err
		}))
		require.Equal(t, "original", content.String(), "existing entry must be untouched")

		// No stale temp file left behind.
		matches, _ := filepath.Glob(filepath.Join(cache.dir(), "*.tmp"))
		require.Empty(t, matches, "no .tmp files should remain after a failed write")
	})

	t.Run("successful write replaces content and leaves no tmp", func(t *testing.T) {
		cache, err := New[[]byte](t.TempDir(), ConversationCache)
		require.NoError(t, err)

		require.NoError(t, cache.Write("id1", func(w io.Writer) error {
			_, err := w.Write([]byte("v1"))
			return err
		}))
		require.NoError(t, cache.Write("id1", func(w io.Writer) error {
			_, err := w.Write([]byte("v2"))
			return err
		}))

		var content bytes.Buffer
		require.NoError(t, cache.Read("id1", func(r io.Reader) error {
			_, err := content.ReadFrom(r)
			return err
		}))
		require.Equal(t, "v2", content.String())

		matches, _ := filepath.Glob(filepath.Join(cache.dir(), "*.tmp"))
		require.Empty(t, matches)
	})

	t.Run("ExpiringCache preserves existing entry on writeFn failure", func(t *testing.T) {
		cache, err := NewExpiring[[]byte](t.TempDir())
		require.NoError(t, err)
		expires := time.Now().Add(time.Hour).Unix()

		require.NoError(t, cache.Write("tok", expires, func(w io.Writer) error {
			_, err := w.Write([]byte("original-token"))
			return err
		}))

		writeErr := cache.Write("tok", expires, func(w io.Writer) error {
			_, _ = w.Write([]byte("partial"))
			return errors.New("simulated write failure")
		})
		require.Error(t, writeErr)

		var content bytes.Buffer
		require.NoError(t, cache.Read("tok", func(r io.Reader) error {
			_, err := content.ReadFrom(r)
			return err
		}))
		require.Equal(t, "original-token", content.String())

		matches, _ := filepath.Glob(filepath.Join(cache.cache.dir(), "*.tmp"))
		require.Empty(t, matches)
	})
}
