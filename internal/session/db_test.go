package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

func testDB(tb testing.TB) *sessionDB {
	db, err := openDB(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}

func testScopedRule(rule ApprovalRule) ApprovalRule {
	scope := workspaceScope("/workspace")
	rule.ScopeKind = scope.Kind
	rule.ScopeValue = scope.Value
	return rule
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

func TestHasSessions(t *testing.T) {
	db := testDB(t)

	has, err := db.HasSessions()
	require.NoError(t, err)
	require.False(t, has)

	require.NoError(t, db.Save("df31ae23ab8b75b5643c2f846c570997edc71333", "message", "openai", "gpt-4"))

	has, err = db.HasSessions()
	require.NoError(t, err)
	require.True(t, has)
}

func TestSessionDB(t *testing.T) {
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

		session, err := db.Find("df31")
		require.NoError(t, err)
		require.Equal(t, testid, session.ID)
		require.Equal(t, "message 1", session.Title)

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
		require.Error(t, db.Save(newSessionID(), "", "openai", "gpt-4o"))
	})

	t.Run("update", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(testid, "message 1", "openai", "gpt-4o"))
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))

		session, err := db.Find("df31")
		require.NoError(t, err)
		require.Equal(t, testid, session.ID)
		require.Equal(t, "message 2", session.Title)

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
		nextSession := newSessionID()
		require.NoError(t, db.Save(nextSession, "another message", "openai", "gpt-4o"))

		head, err := db.FindHEAD()
		require.NoError(t, err)
		require.Equal(t, nextSession, head.ID)
		require.Equal(t, "another message", head.Title)

		list, err := db.List()
		require.NoError(t, err)
		require.Len(t, list, 2)
	})

	t.Run("find by title", func(t *testing.T) {
		db := testDB(t)

		require.NoError(t, db.Save(newSessionID(), "message 1", "openai", "gpt-4o"))
		require.NoError(t, db.Save(testid, "message 2", "openai", "gpt-4o"))

		session, err := db.Find("message 2")
		require.NoError(t, err)
		require.Equal(t, testid, session.ID)
		require.Equal(t, "message 2", session.Title)
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
		require.NoError(t, db.Delete(newSessionID()))

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

func TestUpdatedAtIndexExists(t *testing.T) {
	db := testDB(t)
	var count int
	require.NoError(t, db.db.Get(&count, `
		SELECT count(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_session_updated_at'
	`))
	require.Equal(t, 1, count)
}

func TestSessionData(t *testing.T) {
	db := testDB(t)
	id := newSessionID()
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
		testScopedRule(ApprovalRule{Type: approvalShellPrefix, Tool: "shell_run", Pattern: "git commit *"}),
		testScopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"}),
		testScopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache"}, Mode: accessRead}),
		testScopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache"}, Mode: accessWrite}),
	}

	require.NoError(t, db.SaveSession(id, "session", "openai", "gpt-5", messages, rules))

	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, messages, loaded)

	loadedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.ElementsMatch(t, rules, loadedRules)

	branchID := newSessionID()
	require.NoError(t, db.SaveSession(branchID, "branch", "openai", "gpt-5", loaded, loadedRules))
	branchRules, err := db.ApprovalRules(branchID)
	require.NoError(t, err)
	require.ElementsMatch(t, rules, branchRules)

	require.NoError(t, db.Delete(id))
	require.Error(t, db.ReadMessages(id, &loaded))
	deletedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.Empty(t, deletedRules)
}

func TestSaveSessionRollsBackAtomically(t *testing.T) {
	db := testDB(t)
	id := newSessionID()
	originalMessages := []proto.Message{{Role: proto.RoleUser, Content: "original"}}
	originalRules := []ApprovalRule{testScopedRule(ApprovalRule{
		Type: approvalShellPrefix,
		Tool: "shell_run", Pattern: "git commit *",
	})}
	require.NoError(t, db.SaveSession(
		id, "original", "openai", "gpt-5", originalMessages, originalRules,
	))

	err := db.SaveSession(
		id,
		"",
		"openai",
		"gpt-5",
		[]proto.Message{{Role: proto.RoleUser, Content: "replacement"}},
		[]ApprovalRule{testScopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})},
	)
	require.Error(t, err)

	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, originalMessages, loaded)
	loadedRules, err := db.ApprovalRules(id)
	require.NoError(t, err)
	require.Equal(t, originalRules, loadedRules)
}

func TestMigratesLegacyApprovalRulesWithoutGrantingScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.db")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`
		CREATE TABLE conversations (
			id string NOT NULL PRIMARY KEY,
			title string NOT NULL,
			updated_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			CHECK (id <> ''),
			CHECK (title <> '')
		);
		CREATE TABLE approval_rules (
			conversation_id string NOT NULL,
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (conversation_id, rule_type, tool_name, pattern),
			FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
		);
		INSERT INTO conversations (id, title) VALUES ('abc', 'legacy');
		INSERT INTO approval_rules (conversation_id, rule_type, tool_name, pattern)
		VALUES ('abc', 'edit_all', 'file_edit', '');
	`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := openDB(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	rules, err := db.ApprovalRules("abc")
	require.NoError(t, err)
	require.Equal(t, []ApprovalRule{{Type: approvalEditAll, Tool: "file_edit"}}, rules)

	var ruleSet approvalRuleSet
	ruleSet.Replace(rules)
	require.False(t, ruleSet.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), workspaceScope("/workspace")))
}

// TestMigratesApprovalRulesToAddMode verifies that a DB persisted just
// before mode-splitting (scope + paths columns, no mode column) migrates
// cleanly: existing DirAllow rows survive with an empty Mode so they keep
// matching both read and write, and new mode-scoped rules round-trip.
func TestMigratesApprovalRulesToAddMode(t *testing.T) {
	const (
		legacyWorkspace = `C:\mods-workspace`
		legacyCacheDir  = `C:\mods-cache`
	)
	path := filepath.Join(t.TempDir(), "mods.db")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`
		CREATE TABLE conversations (
			id string NOT NULL PRIMARY KEY,
			title string NOT NULL,
			updated_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			CHECK (id <> ''),
			CHECK (title <> '')
		);
		CREATE TABLE approval_rules (
			conversation_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			paths string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths),
			FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
		);
		INSERT INTO conversations (id, title) VALUES ('abc', 'pre-mode');
		INSERT INTO approval_rules (conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths)
		VALUES ('abc', 'workspace', 'C:\mods-workspace', 'dir_allow', '', '', '["C:\\mods-cache"]');
	`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := openDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	rules, err := db.ApprovalRules("abc")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, approvalDirAllow, rules[0].Type)
	require.Equal(t, []string{legacyCacheDir}, rules[0].Paths)
	require.Equal(t, AccessClass(""), rules[0].Mode) // legacy: empty mode matches both

	// A legacy (empty-mode) rule still satisfies both read and write ops.
	legacyScope := workspaceScope(legacyWorkspace)
	require.True(t, rulesAllowDirs(rules, []string{legacyCacheDir}, legacyScope, accessRead))
	require.True(t, rulesAllowDirs(rules, []string{legacyCacheDir}, legacyScope, accessWrite))

	// New mode-scoped rules round-trip and coexist with the legacy row.
	newRules := append(rules, ApprovalRule{
		ScopeKind: "workspace", ScopeValue: legacyScope.Value,
		Type: approvalDirAllow, Paths: []string{legacyCacheDir}, Mode: accessWrite,
	})
	require.NoError(t, db.SaveSession("abc", "pre-mode", "openai", "gpt-5", nil, newRules))
	loaded, err := db.ApprovalRules("abc")
	require.NoError(t, err)
	require.ElementsMatch(t, newRules, loaded)
}

func TestMigrateDefaultStorageMovesLegacyDB(t *testing.T) {
	dataHome := t.TempDir()
	sessionDir := filepath.Join(dataHome, "sessions")
	legacyDir := filepath.Join(dataHome, "conversations")
	require.NoError(t, os.MkdirAll(legacyDir, 0o700))

	legacyDB := filepath.Join(legacyDir, "mods.db")
	db, err := openDB(legacyDB)
	require.NoError(t, err)
	const id = "df31ae23ab8b75b5643c2f846c570997edc71333"
	require.NoError(t, db.Save(id, "legacy default", "openai", "gpt-4o"))
	require.NoError(t, db.Close())

	require.NoError(t, MigrateDefaultStorage(sessionDir))

	require.NoFileExists(t, legacyDB)
	require.FileExists(t, filepath.Join(sessionDir, "mods.db"))

	migrated, err := openDB(filepath.Join(sessionDir, "mods.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, migrated.Close()) })
	found, err := migrated.Find(id)
	require.NoError(t, err)
	require.Equal(t, "legacy default", found.Title)
}

func TestMigrateLegacySessions(t *testing.T) {
	t.Run("imports valid session and removes gob", func(t *testing.T) {
		dataHome := t.TempDir()
		sessionDir := filepath.Join(dataHome, "sessions")
		legacyDir := filepath.Join(dataHome, "conversations")
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		require.NoError(t, os.MkdirAll(legacyDir, 0o700))
		db, err := openDB(filepath.Join(sessionDir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		id := newSessionID()
		require.NoError(t, db.Save(id, "legacy", "openai", "gpt-4"))
		messages := []proto.Message{{Role: proto.RoleUser, Content: "legacy message"}}
		encoded, err := encodeSession(messages)
		require.NoError(t, err)
		legacyPath := filepath.Join(legacyDir, id+".gob")
		require.NoError(t, os.WriteFile(legacyPath, encoded, 0o600))

		require.NoError(t, db.MigrateLegacySessions(sessionDir))
		require.NoFileExists(t, legacyPath)

		var loaded []proto.Message
		require.NoError(t, db.ReadMessages(id, &loaded))
		require.Equal(t, messages, loaded)
	})

	t.Run("retains corrupt and orphan files", func(t *testing.T) {
		dataHome := t.TempDir()
		sessionDir := filepath.Join(dataHome, "sessions")
		legacyDir := filepath.Join(dataHome, "conversations")
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		require.NoError(t, os.MkdirAll(legacyDir, 0o700))
		db, err := openDB(filepath.Join(sessionDir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		corruptID := newSessionID()
		require.NoError(t, db.Save(corruptID, "corrupt", "openai", "gpt-4"))
		corruptPath := filepath.Join(legacyDir, corruptID+".gob")
		require.NoError(t, os.WriteFile(corruptPath, []byte("not gob"), 0o600))

		orphanID := newSessionID()
		orphanPath := filepath.Join(legacyDir, orphanID+".gob")
		encoded, err := encodeSession([]proto.Message{{Role: proto.RoleUser, Content: "orphan"}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(orphanPath, encoded, 0o600))

		require.Error(t, db.MigrateLegacySessions(sessionDir))
		require.FileExists(t, corruptPath)
		require.FileExists(t, orphanPath)
	})

	t.Run("does not overwrite newer sqlite messages", func(t *testing.T) {
		dataHome := t.TempDir()
		sessionDir := filepath.Join(dataHome, "sessions")
		legacyDir := filepath.Join(dataHome, "conversations")
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		require.NoError(t, os.MkdirAll(legacyDir, 0o700))
		db, err := openDB(filepath.Join(sessionDir, "mods.db"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })

		id := newSessionID()
		newer := []proto.Message{{Role: proto.RoleUser, Content: "newer sqlite message"}}
		require.NoError(t, db.SaveSession(id, "session", "openai", "gpt-5", newer, nil))

		older := []proto.Message{{Role: proto.RoleUser, Content: "older gob message"}}
		encoded, err := encodeSession(older)
		require.NoError(t, err)
		legacyPath := filepath.Join(legacyDir, id+".gob")
		require.NoError(t, os.WriteFile(legacyPath, encoded, 0o600))

		require.NoError(t, db.MigrateLegacySessions(sessionDir))
		require.NoFileExists(t, legacyPath)

		var loaded []proto.Message
		require.NoError(t, db.ReadMessages(id, &loaded))
		require.Equal(t, newer, loaded)
	})
}

func TestMigrateLegacySchemaResumesAfterPartialMigration(t *testing.T) {
	// Simulate a DB left half-migrated by a crash mid-migration (or by an
	// older, non-atomic build): the table renames already completed
	// (sessions exists, conversations gone) but the conversation_id columns
	// in session_messages and approval_rules were not yet renamed to
	// session_id. The migration must resume and finish atomically without
	// error and without losing approval rules. This locks the resumability
	// property that the transactional migrateLegacySessionSchema relies on,
	// which was previously load-bearing and unverified.
	path := filepath.Join(t.TempDir(), "mods.db")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`
		CREATE TABLE sessions (
			id string NOT NULL PRIMARY KEY,
			title string NOT NULL,
			updated_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			CHECK (id <> ''),
			CHECK (title <> '')
		);
		CREATE TABLE session_messages (
			conversation_id string NOT NULL PRIMARY KEY,
			messages blob NOT NULL,
			FOREIGN KEY (conversation_id) REFERENCES sessions (id) ON DELETE CASCADE
		);
		CREATE TABLE approval_rules (
			conversation_id string NOT NULL,
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (conversation_id, rule_type, tool_name, pattern),
			FOREIGN KEY (conversation_id) REFERENCES sessions (id) ON DELETE CASCADE
		);
		INSERT INTO sessions (id, title) VALUES ('abc', 'half-migrated');
		INSERT INTO approval_rules (conversation_id, rule_type, tool_name, pattern)
		VALUES ('abc', 'edit_all', 'file_edit', '');
	`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	db, err := openDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// The approval rule must survive the conversation_id -> session_id column
	// rename (the FK follows the renamed column under legacy_alter_table=OFF).
	rules, err := db.ApprovalRules("abc")
	require.NoError(t, err)
	require.Equal(t, []ApprovalRule{{Type: approvalEditAll, Tool: "file_edit"}}, rules)

	// Re-opening a fully-migrated DB is a no-op, locking idempotency.
	require.NoError(t, db.Close())
	db2, err := openDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db2.Close()) })
	rules2, err := db2.ApprovalRules("abc")
	require.NoError(t, err)
	require.Equal(t, rules, rules2)
}

func TestMigrateDefaultStorageMovesCompanions(t *testing.T) {
	// WAL/-shm companions must move alongside the main DB. An orphaned -wal
	// left at the old path would discard uncheckpointed transactions, which
	// can include recently-saved approval rules. This locks companion
	// preservation (and the companions-first move ordering) against regressions.
	dataHome := t.TempDir()
	sessionDir := filepath.Join(dataHome, "sessions")
	legacyDir := filepath.Join(dataHome, "conversations")
	require.NoError(t, os.MkdirAll(legacyDir, 0o700))

	legacyDB := filepath.Join(legacyDir, "mods.db")
	db, err := openDB(legacyDB)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Simulate WAL/-shm companions left by a prior run.
	require.NoError(t, os.WriteFile(legacyDB+"-wal", []byte("wal-contents"), 0o600))
	require.NoError(t, os.WriteFile(legacyDB+"-shm", []byte("shm-contents"), 0o600))

	require.NoError(t, MigrateDefaultStorage(sessionDir))

	require.NoFileExists(t, legacyDB)
	require.NoFileExists(t, legacyDB+"-wal")
	require.NoFileExists(t, legacyDB+"-shm")
	require.FileExists(t, filepath.Join(sessionDir, "mods.db"))
	require.FileExists(t, filepath.Join(sessionDir, "mods.db-wal"))
	require.FileExists(t, filepath.Join(sessionDir, "mods.db-shm"))
}
