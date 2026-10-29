package session

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/panjie/mods/internal/proto"
)

// ContextSummary is derived context, never a replacement for stored messages or
// approval state. Covered counts non-system messages in the original transcript.
type ContextSummary struct {
	Version int
	Covered int
	Digest  string
	Text    string
}

func NewContextSummary(prefix []proto.Message, text string) *ContextSummary {
	messages := contextMessages(prefix)
	return &ContextSummary{Version: 1, Covered: len(messages), Digest: contextDigest(messages), Text: text}
}

func (s *ContextSummary) Matches(messages []proto.Message) bool {
	if s == nil || s.Version != 1 || s.Covered <= 0 || len(s.Digest) != 64 || strings.TrimSpace(s.Text) == "" || len(s.Text) > 32*1024 {
		return false
	}
	history := contextMessages(messages)
	return s.Covered <= len(history) && s.Digest == contextDigest(history[:s.Covered])
}

func contextMessages(messages []proto.Message) []proto.Message {
	var history []proto.Message
	for _, message := range proto.NormalizeMedia(messages) {
		if message.Role != proto.RoleSystem {
			history = append(history, message)
		}
	}
	return history
}

func contextDigest(messages []proto.Message) string {
	// JSON orders provider-data map keys deterministically. Thought signatures
	// are excluded by Message's JSON contract, so hash them explicitly as well.
	type record struct {
		Message    proto.Message
		Signatures []string
	}
	rows := make([]record, len(messages))
	for i, message := range messages {
		rows[i].Message = message
		for _, call := range message.ToolCalls {
			rows[i].Signatures = append(rows[i].Signatures, call.Function.ThoughtSignature)
		}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (c *DB) ReadContextSummary(id string, messages []proto.Message) (*ContextSummary, error) {
	var data []byte
	if err := c.db.Get(&data, `SELECT snapshot FROM context_summaries WHERE session_id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var summary ContextSummary
	if json.Unmarshal(data, &summary) != nil || !summary.Matches(messages) {
		return nil, nil
	}
	return &summary, nil
}
