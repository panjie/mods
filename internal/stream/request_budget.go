package stream

import (
	"context"
	"fmt"
	"sync/atomic"
)

type requestBudgetKey struct{}
type requestBudget struct{ remaining atomic.Int64 }

// WithRequestBudget bounds provider rounds across conversations sharing ctx,
// including tool continuations and summary workers. Transport retries within a
// provider round are governed by the provider's existing retry limit.
func WithRequestBudget(ctx context.Context, limit int) context.Context {
	budget := &requestBudget{}
	budget.remaining.Store(int64(limit))
	return context.WithValue(ctx, requestBudgetKey{}, budget)
}
func reserveRequest(ctx context.Context) error {
	budget, _ := ctx.Value(requestBudgetKey{}).(*requestBudget)
	if budget == nil {
		return nil
	}
	if budget.remaining.Add(-1) < 0 {
		return fmt.Errorf("model request budget exhausted")
	}
	return nil
}
