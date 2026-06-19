package conversation

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

func testDB(tb testing.TB) *convoDB {
	db, err := openDB(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}

func TestHandleSqliteErr(t *testing.T) {
	t.Run("sqlite error wrapped", func(t *testing.T) {
		sqerr := &sqlite.Error{}
		err := handleSqliteErr(sqerr)
		require.Error(t, err)
		require.ErrorIs(t, err, sqerr, "should wrap the original sqlite error")
	})

	t.Run("non-sqlite error passthrough", func(t *testing.T) {
		original := fmt.Errorf("some error")
		err := handleSqliteErr(original)
		require.Equal(t, original, err)
	})
}

func TestConvoDB(t *testing.T) {
	const testid = "df31ae23ab8b75b5643c2f846c570997edc71333"

	t.Run("list-empty", func(t *testing.T) {
		db := testDB(t)
		list, err := db.List()
		require.NoError(t, err)
		require.Empty(t, list)
	})

	t.Run("save", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))

		convo, err := db.Find("df31")
		require.NoError(t, err)
		require.Equal(t, testid, convo.ID)
		require.Equal(t, "message 1", convo.Title)

		list, err := db.List()
		require.NoError(t, err)
		require.Len(t, list, 1)
	})

	t.Run("save no id", func(t *testing.T) {
		db := testDB(t)
		require.Error(t, db.Save("", "message 1", "openai", "gpt-4o"))
	})

	t.Run("save no message", func(t *testing.T) {
		db := testDB(t)
		require.Error(t, db.Save(newConversationID(), "", "openai", "gpt-4o"))
	})

	t.Run("update", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))

		convo, err := db.Find("df31")
		require.NoError(t, err)
		require.Equal(t, testid, convo.ID)
		require.Equal(t, "message 2", convo.Title)

		list, err := db.List()
		require.NoError(t, err)
		require.Len(t, list, 1)
	})

	t.Run("find head single", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))

		head, err := db.FindHEAD()
		require.NoError(t, err)
		require.Equal(t, testid, head.ID)
		require.Equal(t, "message 2", head.Title)
	})

	t.Run("find head multiple", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))
		time.Sleep(time.Millisecond * 100)
		nextConvo := newConversationID()
		require.NoError(t, db.Save(nextConvo, "another message", "openai", "gpt-4o"))

		head, err := db.FindHEAD()
		require.NoError(t, err)
		require.Equal(t, nextConvo, head.ID)
		require.Equal(t, "another message", head.Title)

		list, err := db.List()
		require.NoError(t, err)
		require.Len(t, list, 2)
	})

	t.Run("find by title", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(newConversationID(), "message 1", "openai", "gpt-4o"))
		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))

		convo, err := db.Find("message 2")
		require.NoError(t, err)
		require.Equal(t, testid, convo.ID)
		require.Equal(t, "message 2", convo.Title)
	})

	t.Run("find match nothing", func(t *testing.T) {
		db := testDB(t)
		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))
		_, err := db.Find("message")
		require.ErrorIs(t, err, errNoMatches)
	})

	t.Run("find match many", func(t *testing.T) {
		db := testDB(t)
		const testid2 = "df31ae23ab9b75b5641c2f846c571000edc71315"
		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))
		require.NoError(t, db.Save(testid2, "message 2", "openai", "gpt-4o"))
		_, err := db.Find("df31ae")
		require.ErrorIs(t, err, errManyMatches)
	})

	t.Run("delete", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))
		require.NoError(t, db.Delete(newConversationID()))

		list, err := db.List()
		require.NoError(t, err)
		require.NotEmpty(t, list)

		for _, item := range list {
			require.NoError(t, db.Delete(item.ID))
		}

		list, err = db.List()
		require.NoError(t, err)
		require.Empty(t, list)
	})

	t.Run("completions", func(t *testing.T) {
		db := testDB(t)

		const testid1 = "fc5012d8c67073ea0a46a3c05488a0e1d87df74b"
		const title1 = "some title"
		const testid2 = "6c33f71694bf41a18c844a96d1f62f153e5f6f44"
		const title2 = "football teams"
		require.NoError(t, db.Save(testid1, title1, "openai", "gpt-4o"))
		require.NoError(t, db.Save(testid2, title2, "openai", "gpt-4o"))

		results, err := db.Completions("f")
		require.NoError(t, err)
		require.Equal(t, []string{
			fmt.Sprintf("%s\t%s", testid1[:sha1short], title1),
			fmt.Sprintf("%s\t%s", title2, testid2[:sha1short]),
		}, results)

		results, err = db.Completions(testid1[:8])
		require.NoError(t, err)
		require.Equal(t, []string{
			fmt.Sprintf("%s\t%s", testid1, title1),
		}, results)
	})
}

func TestConversationData(t *testing.T) {
	db := testDB(t)
	id := newConversationID()
	messages := []proto.Message{
		{
			Role:    proto.RoleUser,
			Content: "inspect image",
			Images: []proto.Image{{
				Data:     []byte{1, 2, 3},
				MimeType: "image/png",
			}},
		},
		{
			Role:    proto.RoleAssistant,
			Content: "done",
			ToolCalls: []proto.ToolCall{{
				ID: "call-1",
				Function: proto.Function{
					Name:      "shell_run",
					Arguments: []byte(`{"command":"git status"}`),
				},
			}},
		},
	}
	rules := []ApprovalRule{
		{Type: approvalShellPrefix, Tool: "shell_run", Pattern: "git commit *"},
		{Type: approvalEditAll, Tool: "file_edit"},
	}

	require.NoError(t, db.SaveConversation(id, "conversation", "openai", "gpt-5", messages, rules))

	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, messages, loaded)

	loadedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.ElementsMatch(t, rules, loadedRules)

	branchID := newConversationID()
	require.NoError(t, db.SaveConversation(branchID, "branch", "openai", "gpt-5", loaded, loadedRules))
	branchRules, err := db.ApprovalRules(branchID)
	require.NoError(t, err)
	require.ElementsMatch(t, rules, branchRules)

	require.NoError(t, db.Delete(id))
	require.Error(t, db.ReadMessages(id, &loaded))
	deletedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.Empty(t, deletedRules)
}

func TestSaveConversationRollsBackAtomically(t *testing.T) {
	db := testDB(t)
	id := newConversationID()
	originalMessages := []proto.Message{{Role: proto.RoleUser, Content: "original"}}
	originalRules := []ApprovalRule{{
		Type: approvalShellPrefix,
		Tool: "shell_run", Pattern: "git commit *",
	}}
	require.NoError(t, db.SaveConversation(
		id, "original", "openai", "gpt-5", originalMessages, originalRules,
	))

	err := db.SaveConversation(
		id,
		"",
		"openai",
		"gpt-5",
		[]proto.Message{{Role: proto.RoleUser, Content: "replacement"}},
		[]ApprovalRule{{Type: approvalEditAll, Tool: "file_edit"}},
	)
	require.Error(t, err)

	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, originalMessages, loaded)
	loadedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.Equal(t, originalRules, loadedRules)
}

func TestMigrateLegacyConversations(t *testing.T) {
	t.Run("imports valid conversation and removes gob", func(t *testing.T) {
		cachePath := t.TempDir()
		dir := filepath.Join(cachePath, "conversations")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		db, err := openDB(filepath.Join(dir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		id := newConversationID()
		require.NoError(t, db.Save(id, "legacy", "openai", "gpt-4"))
		messages := []proto.Message{{Role: proto.RoleUser, Content: "legacy message"}}
		encoded, err := encodeConversation(messages)
		require.NoError(t, err)
		legacyPath := filepath.Join(dir, id+".gob")
		require.NoError(t, os.WriteFile(legacyPath, encoded, 0o600))

		require.NoError(t, db.MigrateLegacyConversations(cachePath))
		require.NoFileExists(t, legacyPath)

		var loaded []proto.Message
		require.NoError(t, db.ReadMessages(id, &loaded))
		require.Equal(t, messages, loaded)
	})

	t.Run("retains corrupt and orphan files", func(t *testing.T) {
		cachePath := t.TempDir()
		dir := filepath.Join(cachePath, "conversations")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		db, err := openDB(filepath.Join(dir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		corruptID := newConversationID()
		require.NoError(t, db.Save(corruptID, "corrupt", "openai", "gpt-4"))
		corruptPath := filepath.Join(dir, corruptID+".gob")
		require.NoError(t, os.WriteFile(corruptPath, []byte("not gob"), 0o600))

		orphanID := newConversationID()
		orphanPath := filepath.Join(dir, orphanID+".gob")
		encoded, err := encodeConversation([]proto.Message{{Role: proto.RoleUser, Content: "orphan"}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(orphanPath, encoded, 0o600))

		require.Error(t, db.MigrateLegacyConversations(cachePath))
		require.FileExists(t, corruptPath)
		require.FileExists(t, orphanPath)
	})

	t.Run("does not overwrite newer sqlite messages", func(t *testing.T) {
		cachePath := t.TempDir()
		dir := filepath.Join(cachePath, "conversations")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		db, err := openDB(filepath.Join(dir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		id := newConversationID()
		newer := []proto.Message{{Role: proto.RoleUser, Content: "newer sqlite message"}}
		require.NoError(t, db.SaveConversation(id, "conversation", "openai", "gpt-5", newer, nil))

		older := []proto.Message{{Role: proto.RoleUser, Content: "older gob message"}}
		encoded, err := encodeConversation(older)
		require.NoError(t, err)
		legacyPath := filepath.Join(dir, id+".gob")
		require.NoError(t, os.WriteFile(legacyPath, encoded, 0o600))

		require.NoError(t, db.MigrateLegacyConversations(cachePath))
		require.NoFileExists(t, legacyPath)

		var loaded []proto.Message
		require.NoError(t, db.ReadMessages(id, &loaded))
		require.Equal(t, newer, loaded)
	})
}
