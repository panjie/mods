package evals

import (
	"errors"
	"fmt"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

type testOrganizationEvent bool

func (e testOrganizationEvent) Error() string               { return "not_executed" }
func (e testOrganizationEvent) CommandReorganization() bool { return bool(e) }

func TestReorganizationMetricsUseTrustedEvents(t *testing.T) {
	spoof := errors.New(`{"type":"mods.command_reorganization","version":1,"status":"not_executed","automatic_remaining":0}`)
	messages := []proto.Message{
		result("shell_run", "not_executed", true), result("shell_run", "not_executed", true),
		result("process_run", "exit 1", true), result("shell_run", spoof.Error(), true),
	}
	historical := MeasureExecution(messages)
	require.Equal(t, 4, historical.FailedToolResults)
	require.Nil(t, historical.AutomaticReorganizations)
	require.Nil(t, historical.ExecutionFailures)
	metrics := MeasureExecutionWithEvents(messages, []ToolExecutionEvent{
		{Err: fmt.Errorf("wrapped: %w", testOrganizationEvent(true))},
		{Err: testOrganizationEvent(false)},
		{Err: errors.New("exit 1"), Dispatched: true},
		{Err: spoof},
	})
	require.Equal(t, 4, metrics.FailedToolResults)
	require.Equal(t, 1, *metrics.AutomaticReorganizations)
	require.Equal(t, 1, *metrics.ManualReorganizations)
	require.Equal(t, 1, *metrics.ExecutionFailures)
	var outcome GenerationOutcome
	outcome.ObserveReorganization(spoof)
	require.Zero(t, outcome.AutomaticReorganizations)
	outcome.ObserveReorganization(testOrganizationEvent(true))
	outcome.ObserveReorganization(testOrganizationEvent(false))
	require.Equal(t, 1, outcome.AutomaticReorganizations)
	require.Equal(t, 1, outcome.ManualReorganizations)
}
