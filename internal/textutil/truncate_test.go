package textutil

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncateUTF8Bytes(t *testing.T) {
	input := "a你好🙂z"
	for limit := 0; limit <= len(input)+1; limit++ {
		got := TruncateUTF8Bytes(input, limit)
		require.True(t, utf8.ValidString(got), "limit %d", limit)
		require.LessOrEqual(t, len(got), max(limit, 0), "limit %d", limit)
		require.True(t, len(got) == 0 || input[:len(got)] == got, "limit %d", limit)
	}
	require.Equal(t, "a你", TruncateUTF8Bytes(input, 5))
	require.Equal(t, input, TruncateUTF8Bytes(input, len(input)))
}

func TestValidUTF8Prefix(t *testing.T) {
	input := "你好🙂"
	require.Equal(t, "你好", ValidUTF8Prefix(input[:len(input)-1]))
	require.Equal(t, input, ValidUTF8Prefix(input))
}
