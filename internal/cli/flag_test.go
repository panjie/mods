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
		"flag needs an argument: --delete",
		"--delete",
		"Flag %s needs an argument.",
	},
	{
		// Multi-hyphen long flag: the previous strings.Split(s, "-") approach
		// produced >3 parts and left `flag` empty, yielding "Flag  is missing."
		// in the UI. TrimPrefix handles arbitrary hyphens.
		"flag needs an argument: --delete-older-than",
		"--delete-older-than",
		"Flag %s needs an argument.",
	},
	{
		"flag needs an argument: --format-as",
		"--format-as",
		"Flag %s needs an argument.",
	},
	{
		"flag needs an argument: 'd' in -d",
		"-d",
		"Flag %s needs an argument.",
	},
	{
		`invalid argument "20dd" for "--delete-older-than" flag: time: unknown unit "dd" in duration "20dd"`,
		"--delete-older-than",
		"Flag %s have an invalid argument.",
	},
	{
		`invalid argument "sdfjasdl" for "--max-tokens" flag: strconv.ParseInt: parsing "sdfjasdl": invalid syntax`,
		"--max-tokens",
		"Flag %s have an invalid argument.",
	},
	{
		`invalid argument "nope" for "-r, --raw" flag: strconv.ParseBool: parsing "nope": invalid syntax`,
		"-r, --raw",
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

func TestReasoningFlagRejectsAuto(t *testing.T) {
	var mode ReasoningMode
	flag := newReasoningFlag(ReasoningOff, &mode)

	require.NoError(t, flag.Set("on"))
	require.Equal(t, ReasoningOn, mode)

	err := flag.Set("auto")
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid reasoning mode "auto"`)
	require.Contains(t, err.Error(), "must be off or on")
}
