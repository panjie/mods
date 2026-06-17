package openai

import "testing"

func TestPendingToolCallsEmptyChoices(t *testing.T) {
	var s Stream
	if calls := s.pendingToolCalls(); len(calls) != 0 {
		t.Fatalf("expected no calls, got %#v", calls)
	}
	if statuses := s.CallTools(); len(statuses) != 0 {
		t.Fatalf("expected no statuses, got %#v", statuses)
	}
}
