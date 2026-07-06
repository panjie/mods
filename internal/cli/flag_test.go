package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

var flagParseErrorTests = []struct {
	in     string
	flag   string
	reason string
}{
	{
		"unknown flag: --nope",
		"--nope",
		"Unknown flag %s.",
	},
	{
		// Shorthand cluster: pflag reports the unknown character quoted. The
		// previous regex `(-\w)` captured the first cluster char (-x), misreporting
		// the wrong flag. We now capture the quoted character.
		`unknown shorthand flag: 'z' in -xz`,
		"-z",
		"Short flag %s is missing.",
	},
	{
		`unknown shorthand flag: 'q' in -q`,
		"-q",
		"Short flag %s is missing.",
	},
	{
		"flag needs an argument: 'a' in -a",
		"-a",
		"Flag %s needs an argument.",
	},
	{
		`invalid argument "sdfjasdl" for "--max-tokens" flag: strconv.ParseInt: parsing "sdfjasdl": invalid syntax`,
		"--max-tokens",
		"Flag %s have an invalid argument.",
	},
	{
		`invalid argument "nope" for "-t, --think" flag: invalid think mode "nope", must be off or on`,
		"-t, --think",
		"Flag %s have an invalid argument.",
	},
}

func TestFlagParseError(t *testing.T) {
	for _, tf := range flagParseErrorTests {
		t.Run(tf.in, func(t *testing.T) {
			err := newFlagParseError(errors.New(tf.in))
			require.Equal(t, tf.flag, err.Flag())
			require.Equal(t, tf.reason, err.ReasonFormat())
			require.Equal(t, tf.in, err.Error())
		})
	}
}

func TestThinkFlagRejectsAuto(t *testing.T) {
	var mode ThinkMode
	flag := newThinkFlag(ThinkOff, &mode)

	require.NoError(t, flag.Set("on"))
	require.Equal(t, ThinkOn, mode)

	err := flag.Set("auto")
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid think mode "auto"`)
	require.Contains(t, err.Error(), "must be off or on")
}

func TestReviewFlagUsesAutoMode(t *testing.T) {
	var mode ReviewMode
	flag := newReviewFlag(ReviewAuto, &mode)

	require.Equal(t, "review-mode", flag.Type())
	require.NoError(t, flag.Set("auto"))
	require.Equal(t, ReviewAuto, mode)
	require.NoError(t, flag.Set("always"))
	require.Equal(t, ReviewAlways, mode)
	require.NoError(t, flag.Set("never"))
	require.Equal(t, ReviewNever, mode)

	err := flag.Set("mutable")
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid review mode "mutable"`)
	require.Contains(t, err.Error(), "must be auto, always, or never")
}
