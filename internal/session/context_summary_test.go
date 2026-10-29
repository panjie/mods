package session

import (
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestContextSummaryPersistenceAndInvalidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.db")
	db, err := Open(path)
	require.NoError(t, err)
	id := NewID()
	history := []proto.Message{{Role: proto.RoleUser, Content: "original"}, {Role: proto.RoleAssistant, Content: "done"}}
	summary := NewContextSummary(history, "historical facts")
	require.NoError(t, db.SaveSession(id, "before", "google", "model", history, nil, summary))
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	loaded, err := db.ReadContextSummary(id, history)
	require.NoError(t, err)
	require.Equal(t, summary, loaded)
	appended := append(append([]proto.Message(nil), history...), proto.Message{Role: proto.RoleUser, Content: "next"})
	require.True(t, loaded.Matches(appended))
	changed := append([]proto.Message(nil), history...)
	changed[0].Content = "edited"
	stale, err := db.ReadContextSummary(id, changed)
	require.NoError(t, err)
	require.Nil(t, stale)
	require.Error(t, db.SaveSession(id, "after", "google", "model", changed, nil, summary))
	var saved []proto.Message
	require.NoError(t, db.ReadMessages(id, &saved))
	require.Equal(t, history, saved, "failed summary save must roll back the original transcript update")
	loaded, err = db.ReadContextSummary(id, history)
	require.NoError(t, err)
	require.Equal(t, summary, loaded, "failed save must also restore the previous summary")
	require.NoError(t, db.SaveSession(id, "edited", "google", "model", changed, nil))
	loaded, err = db.ReadContextSummary(id, changed)
	require.NoError(t, err)
	require.Nil(t, loaded)
}

func TestContextSummaryMigrationFromVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.db")
	db, err := Open(path)
	require.NoError(t, err)
	id := NewID()
	history := []proto.Message{{Role: proto.RoleUser, Content: "keep"}}
	require.NoError(t, db.SaveSession(id, "legacy", "openai", "model", history, nil))
	_, err = db.db.Exec(`DROP TABLE context_summaries`)
	require.NoError(t, err)
	_, err = db.db.Exec(`PRAGMA user_version = 1`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	var saved []proto.Message
	require.NoError(t, db.ReadMessages(id, &saved))
	require.Equal(t, history, saved)
	require.NoError(t, db.SaveSession(id, "legacy", "openai", "model", history, nil, NewContextSummary(history, "keep")))
}

func TestContextSummaryFingerprintIncludesMediaAndSignatures(t *testing.T) {
	history := []proto.Message{{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "a", Function: proto.Function{ThoughtSignature: "signature"}}}, Media: []proto.MediaAttachment{{Kind: "image", Source: "old.png", Data: []byte("bytes")}}}}
	summary := NewContextSummary(history, "reference only")
	require.True(t, summary.Matches(history))
	history[0].ToolCalls[0].Function.ThoughtSignature = "changed"
	require.False(t, summary.Matches(history))
	history[0].ToolCalls[0].Function.ThoughtSignature = "signature"
	history[0].Media[0].Data[0] = 'X'
	require.False(t, summary.Matches(history))
}
