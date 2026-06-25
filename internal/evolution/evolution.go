package evolution

import (
	"errors"
	"time"
)

type EvaluationStatus string

const (
	EvaluationRecorded  EvaluationStatus = "recorded"
	EvaluationImproving EvaluationStatus = "improving"
	EvaluationVerified  EvaluationStatus = "verified"
	EvaluationFailed    EvaluationStatus = "failed"
)

var (
	ErrInvalidEvaluationStatus = errors.New("invalid evaluation status")
)

type Evaluation struct {
	ID             string           `db:"id"`
	Workspace      string           `db:"workspace"`
	ConversationID string           `db:"conversation_id"`
	Rating         int              `db:"rating"`
	Feedback       string           `db:"feedback"`
	Triggered      bool             `db:"triggered"`
	Status         EvaluationStatus `db:"status"`
	FailureReason  string           `db:"failure_reason"`
	CreatedAt      time.Time        `db:"created_at"`
	UpdatedAt      time.Time        `db:"updated_at"`
}

func ValidEvaluationStatus(status EvaluationStatus) bool {
	switch status {
	case EvaluationRecorded, EvaluationImproving, EvaluationVerified, EvaluationFailed:
		return true
	default:
		return false
	}
}
