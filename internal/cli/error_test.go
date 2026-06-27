package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModsError(t *testing.T) {
	err := errors.New("inner error")
	me := modsError{Err: err, ReasonText: "something went wrong"}

	t.Run("error method composes reason and inner detail", func(t *testing.T) {
		// Error() preserves both halves so any caller that only logs
		// err.Error() still sees the wrapping context, not just the
		// innermost message. Previously this returned "inner error".
		require.Equal(t, "something went wrong: inner error", me.Error())
	})
	t.Run("reason method", func(t *testing.T) {
		require.Equal(t, "something went wrong", me.Reason())
	})
	t.Run("error method without inner falls back to reason", func(t *testing.T) {
		require.Equal(t, "something went wrong", modsError{ReasonText: "something went wrong"}.Error())
	})
	t.Run("error method without reason falls back to inner", func(t *testing.T) {
		require.Equal(t, "inner error", modsError{Err: err}.Error())
	})
	t.Run("errors.As still matches the wrapper after nesting", func(t *testing.T) {
		// Nested wrapping must keep both ReasonText layers visible to
		// callers that walk the chain via errors.As.
		nested := modsError{Err: me, ReasonText: "outer reason"}
		var got modsError
		require.True(t, errors.As(nested, &got))
		require.Equal(t, "outer reason", got.ReasonText)

		// Unwrap once to reach the inner wrapper.
		require.True(t, errors.As(errors.Unwrap(nested), &got))
		require.Equal(t, "something went wrong", got.ReasonText)
	})
}

func TestNewFlagParseError(t *testing.T) {
	t.Run("flag needs argument", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("flag needs an argument: --model"))
		require.Equal(t, "Flag %s needs an argument.", fpe.ReasonText)
		require.Equal(t, "--model", fpe.flag)
	})

	t.Run("unknown flag", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("unknown flag: --wut"))
		require.Equal(t, "Unknown flag %s.", fpe.ReasonText)
		require.Equal(t, "--wut", fpe.flag)
	})

	t.Run("unknown shorthand", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("unknown shorthand flag: 'z' in -xz"))
		require.Equal(t, "Short flag %s is missing.", fpe.ReasonText)
		// The unknown character is 'z' (the quoted char), not 'x' (the cluster
		// start). Previously the regex captured -x, misreporting the flag.
		require.Equal(t, "-z", fpe.flag)
	})

	t.Run("invalid argument", func(t *testing.T) {
		fpe := newFlagParseError(errors.New(`invalid argument "abc" for "--model" flag: strconv.ParseFloat: parsing "abc": invalid syntax`))
		require.Equal(t, "Flag %s have an invalid argument.", fpe.ReasonText)
		require.Equal(t, "--model", fpe.flag)
	})

	t.Run("default fallthrough", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("some unknown error"))
		require.Equal(t, "some unknown error", fpe.ReasonText)
	})
}
