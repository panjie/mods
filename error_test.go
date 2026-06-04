package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModsError(t *testing.T) {
	err := errors.New("inner error")
	me := modsError{err: err, reason: "something went wrong"}

	t.Run("error method", func(t *testing.T) {
		require.Equal(t, "inner error", me.Error())
	})
	t.Run("reason method", func(t *testing.T) {
		require.Equal(t, "something went wrong", me.Reason())
	})
}

func TestNewFlagParseError(t *testing.T) {
	t.Run("flag needs argument", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("flag needs an argument: --model"))
		require.Equal(t, "Flag %s needs an argument.", fpe.reason)
		require.Equal(t, "--model", fpe.flag)
	})

	t.Run("unknown flag", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("unknown flag: --wut"))
		require.Equal(t, "Flag %s is missing.", fpe.reason)
		require.Equal(t, "--wut", fpe.flag)
	})

	t.Run("unknown shorthand", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("unknown shorthand flag: 'z' in -xz"))
		require.Equal(t, "Short flag %s is missing.", fpe.reason)
		require.Equal(t, "-x", fpe.flag)
	})

	t.Run("invalid argument", func(t *testing.T) {
		fpe := newFlagParseError(errors.New(`invalid argument "abc" for "--model" flag: strconv.ParseFloat: parsing "abc": invalid syntax`))
		require.Equal(t, "Flag %s have an invalid argument.", fpe.reason)
		require.Equal(t, "--model", fpe.flag)
	})

	t.Run("default fallthrough", func(t *testing.T) {
		fpe := newFlagParseError(errors.New("some unknown error"))
		require.Equal(t, "some unknown error", fpe.reason)
	})
}
