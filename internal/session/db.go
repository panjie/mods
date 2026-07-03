package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"modernc.org/sqlite"
)

var (
	ErrNoMatches   = errors.New("no sessions found")
	ErrManyMatches = errors.New("multiple sessions matched the input")
)

func handleSqliteErr(err error) error {
	sqerr := &sqlite.Error{}
	if errors.As(err, &sqerr) {
		return fmt.Errorf(
			"%w: %s",
			sqerr,
			sqlite.ErrorCodeString[sqerr.Code()],
		)
	}
	return err
}

func MigrateDefaultStorage(sessionDir string) error {
	oldDir := filepath.Join(filepath.Dir(sessionDir), "conversations")
	oldDB := filepath.Join(oldDir, "mods.db")
	newDB := filepath.Join(sessionDir, "mods.db")

	if _, err := os.Stat(newDB); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat session db: %w", err)
	}
	if _, err := os.Stat(oldDB); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy session db: %w", err)
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil { //nolint:mnd
		return fmt.Errorf("create session dir: %w", err)
	}
	if err := os.Rename(oldDB, newDB); err != nil {
		return fmt.Errorf("move legacy session db: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		oldCompanion := oldDB + suffix
		newCompanion := newDB + suffix
		if err := os.Rename(oldCompanion, newCompanion); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("move legacy session db companion: %w", err)
		}
	}
	return nil
}

func Open(ds string) (*DB, error) {
	db, err := sqlx.Open("sqlite", ds)
	if err != nil {
		return nil, fmt.Errorf(
			"could not create db: %w",
			handleSqliteErr(err),
		)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf(
			"could not ping db: %w",
			handleSqliteErr(err),
		)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return nil, fmt.Errorf("could not enable foreign keys: %w", err)
	}
	if err := migrateLegacySessionSchema(db); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE
		  IF NOT EXISTS sessions (
		    id string NOT NULL PRIMARY KEY,
		    title string NOT NULL,
		    updated_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
		    CHECK (id <> ''),
		    CHECK (title <> '')
		  )
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_id ON sessions (id)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_title ON sessions (title)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_session_updated_at ON sessions (updated_at DESC)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}

	if !hasColumn(db, "sessions", "model") {
		if _, err := db.Exec(`
			ALTER TABLE sessions ADD COLUMN model string
		`); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if !hasColumn(db, "sessions", "api") {
		if _, err := db.Exec(`
			ALTER TABLE sessions ADD COLUMN api string
		`); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_messages (
			session_id string NOT NULL PRIMARY KEY,
			messages blob NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
		)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_rules (
			session_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			paths string NOT NULL DEFAULT '',
			mode string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode),
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
		)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if !hasColumn(db, "approval_rules", "scope_kind") || !hasColumn(db, "approval_rules", "scope_value") {
		if err := migrateApprovalRulesScope(db); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if !hasColumn(db, "approval_rules", "paths") {
		if err := migrateApprovalRulesPaths(db); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if !hasColumn(db, "approval_rules", "mode") {
		if err := migrateApprovalRulesMode(db); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}

	return &DB{db: db}, nil
}

func hasColumn(db *sqlx.DB, table, col string) bool {
	var count int
	if err := db.Get(&count, db.Rebind(`
		SELECT count(*)
		FROM pragma_table_info(?) c
		WHERE c.name = ?
	`), table, col); err != nil {
		return false
	}
	return count > 0
}

func tableExists(db *sqlx.DB, table string) bool {
	var count int
	if err := db.Get(&count, db.Rebind(`
		SELECT count(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`), table); err != nil {
		return false
	}
	return count > 0
}

func migrateLegacySessionSchema(db *sqlx.DB) error {
	if tableExists(db, "conversations") && !tableExists(db, "sessions") {
		if _, err := db.Exec(`ALTER TABLE conversations RENAME TO sessions`); err != nil {
			return err
		}
	}
	if tableExists(db, "conversation_messages") && !tableExists(db, "session_messages") {
		if _, err := db.Exec(`ALTER TABLE conversation_messages RENAME TO session_messages`); err != nil {
			return err
		}
	}
	if tableExists(db, "session_messages") &&
		hasColumn(db, "session_messages", "conversation_id") &&
		!hasColumn(db, "session_messages", "session_id") {
		if _, err := db.Exec(`ALTER TABLE session_messages RENAME COLUMN conversation_id TO session_id`); err != nil {
			return err
		}
	}
	if tableExists(db, "approval_rules") &&
		hasColumn(db, "approval_rules", "conversation_id") &&
		!hasColumn(db, "approval_rules", "session_id") {
		if _, err := db.Exec(`ALTER TABLE approval_rules RENAME COLUMN conversation_id TO session_id`); err != nil {
			return err
		}
	}
	return nil
}

func migrateApprovalRulesScope(db *sqlx.DB) error {
	return migrateApprovalRulesReplaceTable(db, `
		CREATE TABLE approval_rules_migration_tmp (
			session_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (session_id, scope_kind, scope_value, rule_type, tool_name, pattern),
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
		)
	`, `
		INSERT INTO approval_rules_migration_tmp
			(session_id, scope_kind, scope_value, rule_type, tool_name, pattern, created_at)
		SELECT session_id, '', '', rule_type, tool_name, pattern, created_at
		FROM approval_rules
	`)
}

func migrateApprovalRulesPaths(db *sqlx.DB) error {
	return migrateApprovalRulesReplaceTable(db, `
		CREATE TABLE approval_rules_migration_tmp (
			session_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			paths string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths),
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
		)
	`, `
		INSERT INTO approval_rules_migration_tmp
			(session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, created_at)
		SELECT session_id, scope_kind, scope_value, rule_type, tool_name, pattern, '', created_at
		FROM approval_rules
	`)
}

// migrateApprovalRulesMode adds the mode column (read/write) to
// approval_rules and folds it into the primary key so a read-only and a
// write-only approval for the same directory can coexist. Existing rows
// are copied with mode = ” which matches both read and write, preserving
// the behaviour of approvals saved before mode-splitting.
func migrateApprovalRulesMode(db *sqlx.DB) error {
	return migrateApprovalRulesReplaceTable(db, `
		CREATE TABLE approval_rules_migration_tmp (
			session_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			paths string NOT NULL DEFAULT '',
			mode string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode),
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
		)
	`, `
		INSERT INTO approval_rules_migration_tmp
			(session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode, created_at)
		SELECT session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, '', created_at
		FROM approval_rules
	`)
}

// migrateApprovalRulesReplaceTable rebuilds the approval_rules table by
// creating a temporary table from newTableDDL, copying rows via
// insertSelect, dropping the original, and renaming the temp table into
// its place. SQLite cannot ALTER the PRIMARY KEY in place, so any
// migration that changes the key shape must go through this path.
//
// The temporary table is always named approval_rules_migration_tmp so
// callers do not have to invent unique names per migration.
func migrateApprovalRulesReplaceTable(db *sqlx.DB, newTableDDL, insertSelect string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		newTableDDL,
		insertSelect,
		`DROP TABLE approval_rules`,
		`ALTER TABLE approval_rules_migration_tmp RENAME TO approval_rules`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type DB struct {
	db *sqlx.DB
}

// Session in the database.
type Session struct {
	ID        string    `db:"id"`
	Title     string    `db:"title"`
	UpdatedAt time.Time `db:"updated_at"`
	API       *string   `db:"api"`
	Model     *string   `db:"model"`
}

func (c *DB) Close() error {
	return c.db.Close() //nolint: wrapcheck
}

func (c *DB) Save(id, title, api, model string) error {
	return c.saveMetadata(c.db, id, title, api, model)
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Rebind(query string) string
}

func (c *DB) saveMetadata(exec sqlExecutor, id, title, api, model string) error {
	res, err := exec.Exec(exec.Rebind(`
		UPDATE sessions
		SET
		  title = ?,
		  api = ?,
		  model = ?,
		  updated_at = CURRENT_TIMESTAMP
		WHERE
		  id = ?
	`), title, api, model, id)
	if err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	if rows > 0 {
		return nil
	}

	if _, err := exec.Exec(exec.Rebind(`
		INSERT INTO
		  sessions (id, title, api, model)
		VALUES
		  (?, ?, ?, ?)
	`), id, title, api, model); err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	return nil
}

func (c *DB) SaveSession(
	id, title, api, model string,
	messages []proto.Message,
	rules []approval.Rule,
) error {
	encoded, err := encodeSession(messages)
	if err != nil {
		return err
	}
	tx, err := c.db.Beginx()
	if err != nil {
		return fmt.Errorf("SaveSession: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := c.saveMetadata(tx, id, title, api, model); err != nil {
		return err
	}
	if _, err := tx.Exec(tx.Rebind(`
		INSERT INTO session_messages (session_id, messages)
		VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET messages = excluded.messages
	`), id, encoded); err != nil {
		return fmt.Errorf("SaveSession messages: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM approval_rules WHERE session_id = ?
	`), id); err != nil {
		return fmt.Errorf("SaveSession rules: %w", err)
	}
	for _, rule := range approval.Dedupe(rules) {
		paths, err := json.Marshal(rule.Paths)
		if err != nil {
			return fmt.Errorf("SaveSession paths: %w", err)
		}
		if _, err := tx.Exec(tx.Rebind(`
			INSERT INTO approval_rules (session_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`), id, rule.ScopeKind, rule.ScopeValue, rule.Type, rule.Tool, rule.Pattern, string(paths), string(rule.Mode)); err != nil {
			return fmt.Errorf("SaveSession rule: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SaveSession commit: %w", err)
	}
	return nil
}

func (c *DB) ReadMessages(id string, messages *[]proto.Message) error {
	var encoded []byte
	if err := c.db.Get(&encoded, c.db.Rebind(`
		SELECT messages FROM session_messages WHERE session_id = ?
	`), id); err != nil {
		return fmt.Errorf("ReadMessages: %w", err)
	}
	if err := decodeSessionBytes(encoded, messages); err != nil {
		return fmt.Errorf("ReadMessages: %w", err)
	}
	return nil
}

type ruleRow struct {
	ScopeKind  string `db:"scope_kind"`
	ScopeValue string `db:"scope_value"`
	Type       string `db:"rule_type"`
	Tool       string `db:"tool_name"`
	Pattern    string `db:"pattern"`
	Paths      string `db:"paths"`
	Mode       string `db:"mode"`
}

func (c *DB) ApprovalRules(id string) ([]approval.Rule, error) {
	var rows []ruleRow
	if id == "" {
		return nil, nil
	}
	if err := c.db.Select(&rows, c.db.Rebind(`
		SELECT scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode
		FROM approval_rules
		WHERE session_id = ?
		ORDER BY created_at, scope_kind, scope_value, rule_type, tool_name, pattern, paths, mode
	`), id); err != nil {
		return nil, fmt.Errorf("ApprovalRules: %w", err)
	}
	rules := make([]approval.Rule, 0, len(rows))
	for _, row := range rows {
		rule := approval.Rule{
			ScopeKind:  approval.ScopeKind(row.ScopeKind),
			ScopeValue: row.ScopeValue,
			Type:       approval.RuleType(row.Type),
			Tool:       row.Tool,
			Pattern:    row.Pattern,
			Mode:       approval.AccessClass(row.Mode),
		}
		if row.Paths != "" && row.Paths != "null" {
			_ = json.Unmarshal([]byte(row.Paths), &rule.Paths)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func (c *DB) Delete(id string) error {
	tx, err := c.db.Beginx()
	if err != nil {
		return fmt.Errorf("Delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM approval_rules WHERE session_id = ?
	`), id); err != nil {
		return fmt.Errorf("Delete rules: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM session_messages WHERE session_id = ?
	`), id); err != nil {
		return fmt.Errorf("Delete messages: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM sessions
		WHERE
		  id = ?
	`), id); err != nil {
		return fmt.Errorf("Delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Delete commit: %w", err)
	}
	return nil
}

func (c *DB) MigrateLegacySessions(sessionDir string) error {
	dir := filepath.Join(filepath.Dir(sessionDir), "conversations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy sessions: %w", err)
	}
	var migrationErrors []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".gob" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".gob")
		path := filepath.Join(dir, entry.Name())
		if !IDPattern.MatchString(id) {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("legacy session %s has an invalid ID; file retained", path))
			continue
		}
		var exists int
		if err := c.db.Get(&exists, c.db.Rebind(`
			SELECT count(*) FROM sessions WHERE id = ?
		`), id); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("check legacy session %s: %w", id, err))
			continue
		}
		if exists == 0 {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("legacy session %s has no database metadata; file retained", id))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("read legacy session %s: %w", id, err))
			continue
		}
		var messages []proto.Message
		if err := decodeSessionBytes(data, &messages); err != nil {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("decode legacy session %s: %w; file retained", id, err))
			continue
		}
		encoded, err := encodeSession(messages)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("encode legacy session %s: %w", id, err))
			continue
		}
		var existing []byte
		existingErr := c.db.Get(&existing, c.db.Rebind(`
			SELECT messages FROM session_messages WHERE session_id = ?
		`), id)
		if existingErr == nil {
			var existingMessages []proto.Message
			if err := decodeSessionBytes(existing, &existingMessages); err == nil {
				if err := os.Remove(path); err != nil {
					migrationErrors = append(migrationErrors,
						fmt.Errorf("remove migrated legacy session %s: %w", id, err))
				}
				continue
			}
		} else if !errors.Is(existingErr, sql.ErrNoRows) {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("check migrated session %s: %w", id, existingErr))
			continue
		}
		if err := c.migrateOneLegacySession(id, encoded); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("migrate legacy session %s: %w", id, err))
			continue
		}
		if err := os.Remove(path); err != nil {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("remove migrated legacy session %s: %w", id, err))
		}
	}
	return errors.Join(migrationErrors...)
}

// migrateOneLegacySession persists a single legacy session in
// its own transaction. The defer-Rollback pattern mirrors the other
// migration helpers in this file (migrateApprovalRulesScope,
// migrateApprovalRulesPaths, SaveSession, Delete): a Rollback on an
// already-committed transaction is a documented no-op, so this is safe
// to call unconditionally and guarantees the transaction handle is
// released even if Commit fails.
func (c *DB) migrateOneLegacySession(id string, encoded []byte) error {
	tx, err := c.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin legacy migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(tx.Rebind(`
		INSERT INTO session_messages (session_id, messages)
		VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET messages = excluded.messages
	`), id, encoded); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy session: %w", err)
	}
	return nil
}

func (c *DB) ListOlderThan(t time.Duration) ([]Session, error) {
	var sessions []Session
	if err := c.db.Select(&sessions, c.db.Rebind(`
		SELECT
		  *
		FROM
		  sessions
		WHERE
		  updated_at < ?
		`), time.Now().Add(-t)); err != nil {
		return nil, fmt.Errorf("ListOlderThan: %w", err)
	}
	return sessions, nil
}

func (c *DB) FindHEAD() (*Session, error) {
	var session Session
	if err := c.db.Get(&session, `
		SELECT
		  *
		FROM
		  sessions
		ORDER BY
		  updated_at DESC
		LIMIT
		  1
	`); err != nil {
		return nil, fmt.Errorf("FindHead: %w", err)
	}
	return &session, nil
}

func (c *DB) findByExactTitle(result *[]Session, in string) error {
	if err := c.db.Select(result, c.db.Rebind(`
		SELECT
		  *
		FROM
		  sessions
		WHERE
		  title = ?
	`), in); err != nil {
		return fmt.Errorf("findByExactTitle: %w", err)
	}
	return nil
}

func (c *DB) findByIDOrTitle(result *[]Session, in string) error {
	if err := c.db.Select(result, c.db.Rebind(`
		SELECT
		  *
		FROM
		  sessions
		WHERE
		  id glob ?
		  OR title = ?
	`), in+"*", in); err != nil {
		return fmt.Errorf("findByIDOrTitle: %w", err)
	}
	return nil
}

func (c *DB) Completions(in string) ([]string, error) {
	var result []string
	if err := c.db.Select(&result, c.db.Rebind(`
		SELECT
		  printf (
		    '%s%c%s',
		    CASE
		      WHEN length (?) < ? THEN substr (id, 1, ?)
		      ELSE id
		    END,
		    char(9),
		    title
		  )
		FROM
		  sessions
		WHERE
		  id glob ?
		UNION
		SELECT
		  printf ("%s%c%s", title, char(9), substr (id, 1, ?))
		FROM
		  sessions
		WHERE
		  title glob ?
	`), in, ShortIDLength, ShortIDLength, in+"*", ShortIDLength, in+"*"); err != nil {
		return result, fmt.Errorf("Completions: %w", err)
	}
	return result, nil
}

func (c *DB) Find(in string) (*Session, error) {
	var sessions []Session
	var err error

	if len(in) < MinIDLength {
		err = c.findByExactTitle(&sessions, in)
	} else {
		err = c.findByIDOrTitle(&sessions, in)
	}
	if err != nil {
		return nil, fmt.Errorf("Find %q: %w", in, err)
	}

	if len(sessions) > 1 {
		return nil, fmt.Errorf("%w: %s", ErrManyMatches, in)
	}
	if len(sessions) == 1 {
		return &sessions[0], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNoMatches, in)
}

func (c *DB) List() ([]Session, error) {
	var sessions []Session
	if err := c.db.Select(&sessions, `
		SELECT
		  *
		FROM
		  sessions
		ORDER BY
		  updated_at DESC
	`); err != nil {
		return sessions, fmt.Errorf("List: %w", err)
	}
	return sessions, nil
}

func (c *DB) HasSessions() (bool, error) {
	var count int
	if err := c.db.Get(&count, `
		SELECT count(*)
		FROM sessions
	`); err != nil {
		return false, fmt.Errorf("HasSessions: %w", err)
	}
	return count > 0, nil
}
