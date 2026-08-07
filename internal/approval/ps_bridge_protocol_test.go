package approval

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellBridgeSerializesEveryIRField(t *testing.T) {
	script := string(psBridgeScript)
	start := strings.Index(script, "function Write-IR")
	require.GreaterOrEqual(t, start, 0)
	end := strings.Index(script[start:], "[Console]::Out.WriteLine")
	require.Greater(t, end, 0)
	writeIR := script[start : start+end]

	typeOfIR := reflect.TypeOf(psBridgeIR{})
	for i := 0; i < typeOfIR.NumField(); i++ {
		field := strings.Split(typeOfIR.Field(i).Tag.Get("json"), ",")[0]
		require.NotEmpty(t, field)
		assignment := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `\s*=`)
		require.Regexpf(t, assignment, writeIR, "%s must be copied into the bridge JSON response", field)
	}
}
