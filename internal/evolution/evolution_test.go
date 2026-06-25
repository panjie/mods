package evolution

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusValidation(t *testing.T) {
	require.True(t, ValidEvaluationStatus(EvaluationRecorded))
	require.True(t, ValidEvaluationStatus(EvaluationImproving))
	require.True(t, ValidEvaluationStatus(EvaluationVerified))
	require.True(t, ValidEvaluationStatus(EvaluationFailed))
	require.False(t, ValidEvaluationStatus(EvaluationStatus("other")))

	require.True(t, errors.Is(ErrInvalidEvaluationStatus, ErrInvalidEvaluationStatus))
}
