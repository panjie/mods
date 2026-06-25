package conversation

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
	"github.com/panjie/mods/internal/evolution"
	"github.com/panjie/mods/internal/proto"
	"modernc.org/sqlite"
)

var (
	ErrNoMatches   = errors.New("no conversations found")
	ErrManyMatches = errors.New("multiple conversations matched the input")
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
	if _, err := db.Exec(`
		CREATE TABLE
		  IF NOT EXISTS conversations (
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
		CREATE INDEX IF NOT EXISTS idx_conv_id ON conversations (id)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_conv_title ON conversations (title)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_conv_updated_at ON conversations (updated_at DESC)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}

	if !hasColumn(db, "conversations", "model") {
		if _, err := db.Exec(`
			ALTER TABLE conversations ADD COLUMN model string
		`); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if !hasColumn(db, "conversations", "api") {
		if _, err := db.Exec(`
			ALTER TABLE conversations ADD COLUMN api string
		`); err != nil {
			return nil, fmt.Errorf("could not migrate db: %w", err)
		}
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS conversation_messages (
			conversation_id string NOT NULL PRIMARY KEY,
			messages blob NOT NULL,
			FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
		)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_rules (
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
	if _, err := db.Exec(`DROP TABLE IF EXISTS evolution_feedback`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS evolution_proposals`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS evolution_evaluations (
			id string NOT NULL PRIMARY KEY,
			workspace string NOT NULL,
			conversation_id string NOT NULL,
			rating integer NOT NULL,
			feedback string NOT NULL DEFAULT '',
			triggered boolean NOT NULL DEFAULT false,
			status string NOT NULL,
			failure_reason string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			updated_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			CHECK (id <> ''),
			CHECK (workspace <> ''),
			CHECK (conversation_id <> ''),
			CHECK (rating >= 1 AND rating <= 5),
			CHECK (status IN ('recorded', 'improving', 'verified', 'failed'))
		)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_evolution_evaluations_workspace_updated_at
		ON evolution_evaluations (workspace, updated_at DESC)
	`); err != nil {
		return nil, fmt.Errorf("could not migrate db: %w", err)
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

func migrateApprovalRulesScope(db *sqlx.DB) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		CREATE TABLE approval_rules_scoped_migration (
			conversation_id string NOT NULL,
			scope_kind string NOT NULL DEFAULT '',
			scope_value string NOT NULL DEFAULT '',
			rule_type string NOT NULL,
			tool_name string NOT NULL,
			pattern string NOT NULL DEFAULT '',
			created_at datetime NOT NULL DEFAULT (strftime ('%Y-%m-%d %H:%M:%f', 'now')),
			PRIMARY KEY (conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern),
			FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO approval_rules_scoped_migration
			(conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, created_at)
		SELECT conversation_id, '', '', rule_type, tool_name, pattern, created_at
		FROM approval_rules
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE approval_rules`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE approval_rules_scoped_migration RENAME TO approval_rules`); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateApprovalRulesPaths(db *sqlx.DB) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		CREATE TABLE approval_rules_paths_migration (
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
		)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO approval_rules_paths_migration
			(conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths, created_at)
		SELECT conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, '', created_at
		FROM approval_rules
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE approval_rules`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE approval_rules_paths_migration RENAME TO approval_rules`); err != nil {
		return err
	}
	return tx.Commit()
}

type DB struct {
	db *sqlx.DB
}

// Conversation in the database.
type Conversation struct {
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
		UPDATE conversations
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
		  conversations (id, title, api, model)
		VALUES
		  (?, ?, ?, ?)
	`), id, title, api, model); err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	return nil
}

func (c *DB) SaveConversation(
	id, title, api, model string,
	messages []proto.Message,
	rules []approval.Rule,
) error {
	encoded, err := encodeConversation(messages)
	if err != nil {
		return err
	}
	tx, err := c.db.Beginx()
	if err != nil {
		return fmt.Errorf("SaveConversation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := c.saveMetadata(tx, id, title, api, model); err != nil {
		return err
	}
	if _, err := tx.Exec(tx.Rebind(`
		INSERT INTO conversation_messages (conversation_id, messages)
		VALUES (?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET messages = excluded.messages
	`), id, encoded); err != nil {
		return fmt.Errorf("SaveConversation messages: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM approval_rules WHERE conversation_id = ?
	`), id); err != nil {
		return fmt.Errorf("SaveConversation rules: %w", err)
	}
	for _, rule := range approval.Dedupe(rules) {
		paths, err := json.Marshal(rule.Paths)
		if err != nil {
			return fmt.Errorf("SaveConversation paths: %w", err)
		}
		if _, err := tx.Exec(tx.Rebind(`
			INSERT INTO approval_rules (conversation_id, scope_kind, scope_value, rule_type, tool_name, pattern, paths)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`), id, rule.ScopeKind, rule.ScopeValue, rule.Type, rule.Tool, rule.Pattern, string(paths)); err != nil {
			return fmt.Errorf("SaveConversation rule: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SaveConversation commit: %w", err)
	}
	return nil
}

func (c *DB) ReadMessages(id string, messages *[]proto.Message) error {
	var encoded []byte
	if err := c.db.Get(&encoded, c.db.Rebind(`
		SELECT messages FROM conversation_messages WHERE conversation_id = ?
	`), id); err != nil {
		return fmt.Errorf("ReadMessages: %w", err)
	}
	if err := decodeConversationBytes(encoded, messages); err != nil {
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
}

func (c *DB) ApprovalRules(id string) ([]approval.Rule, error) {
	var rows []ruleRow
	if id == "" {
		return nil, nil
	}
	if err := c.db.Select(&rows, c.db.Rebind(`
		SELECT scope_kind, scope_value, rule_type, tool_name, pattern, paths
		FROM approval_rules
		WHERE conversation_id = ?
		ORDER BY created_at, scope_kind, scope_value, rule_type, tool_name, pattern
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
		}
		if row.Paths != "" && row.Paths != "null" {
			_ = json.Unmarshal([]byte(row.Paths), &rule.Paths)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func (c *DB) SaveEvolutionEvaluation(evaluation evolution.Evaluation) (evolution.Evaluation, error) {
	evaluation.ID = strings.TrimSpace(evaluation.ID)
	if evaluation.ID == "" {
		evaluation.ID = NewID()
	}
	evaluation.Workspace = strings.TrimSpace(evaluation.Workspace)
	evaluation.ConversationID = strings.TrimSpace(evaluation.ConversationID)
	evaluation.Feedback = strings.TrimSpace(evaluation.Feedback)
	evaluation.FailureReason = strings.TrimSpace(evaluation.FailureReason)
	if evaluation.Status == "" {
		evaluation.Status = evolution.EvaluationRecorded
	}
	if evaluation.Workspace == "" {
		return evolution.Evaluation{}, fmt.Errorf("SaveEvolutionEvaluation: workspace is required")
	}
	if evaluation.ConversationID == "" {
		return evolution.Evaluation{}, fmt.Errorf("SaveEvolutionEvaluation: conversation id is required")
	}
	if evaluation.Rating < 1 || evaluation.Rating > 5 {
		return evolution.Evaluation{}, fmt.Errorf("SaveEvolutionEvaluation: rating must be between 1 and 5")
	}
	if !evolution.ValidEvaluationStatus(evaluation.Status) {
		return evolution.Evaluation{}, fmt.Errorf("%w: %q", evolution.ErrInvalidEvaluationStatus, evaluation.Status)
	}

	if _, err := c.db.Exec(c.db.Rebind(`
		INSERT INTO evolution_evaluations (
			id, workspace, conversation_id, rating, feedback, triggered, status, failure_reason
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`), evaluation.ID, evaluation.Workspace, evaluation.ConversationID, evaluation.Rating,
		evaluation.Feedback, evaluation.Triggered, evaluation.Status, evaluation.FailureReason); err != nil {
		return evolution.Evaluation{}, fmt.Errorf("SaveEvolutionEvaluation: %w", err)
	}
	saved, err := c.FindEvolutionEvaluation(evaluation.Workspace, evaluation.ID)
	if err != nil {
		return evolution.Evaluation{}, fmt.Errorf("SaveEvolutionEvaluation: %w", err)
	}
	return saved, nil
}

func (c *DB) FindEvolutionEvaluation(workspace, id string) (evolution.Evaluation, error) {
	workspace = strings.TrimSpace(workspace)
	id = strings.TrimSpace(id)
	if workspace == "" {
		return evolution.Evaluation{}, fmt.Errorf("FindEvolutionEvaluation: workspace is required")
	}
	if id == "" {
		return evolution.Evaluation{}, fmt.Errorf("FindEvolutionEvaluation: id is required")
	}
	var evaluations []evolution.Evaluation
	op := "="
	arg := id
	if len(id) >= MinIDLength {
		op = "glob"
		arg = id + "*"
	}
	if err := c.db.Select(&evaluations, c.db.Rebind(fmt.Sprintf(`
		SELECT
			id, workspace, conversation_id, rating, feedback, triggered,
			status, failure_reason, created_at, updated_at
		FROM evolution_evaluations
		WHERE workspace = ? AND id %s ?
		ORDER BY updated_at DESC, created_at DESC, id DESC
	`, op)), workspace, arg); err != nil {
		return evolution.Evaluation{}, fmt.Errorf("FindEvolutionEvaluation: %w", err)
	}
	if len(evaluations) > 1 {
		return evolution.Evaluation{}, fmt.Errorf("%w: %s", ErrManyMatches, id)
	}
	if len(evaluations) == 0 {
		return evolution.Evaluation{}, fmt.Errorf("%w: %s", ErrNoMatches, id)
	}
	return evaluations[0], nil
}

func (c *DB) ListEvolutionEvaluations(workspace string) ([]evolution.Evaluation, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, fmt.Errorf("ListEvolutionEvaluations: workspace is required")
	}
	var evaluations []evolution.Evaluation
	if err := c.db.Select(&evaluations, c.db.Rebind(`
		SELECT
			id, workspace, conversation_id, rating, feedback, triggered,
			status, failure_reason, created_at, updated_at
		FROM evolution_evaluations
		WHERE workspace = ?
		ORDER BY updated_at DESC, created_at DESC, id DESC
	`), workspace); err != nil {
		return nil, fmt.Errorf("ListEvolutionEvaluations: %w", err)
	}
	return evaluations, nil
}

func (c *DB) UpdateEvolutionEvaluationStatus(workspace, id string, status evolution.EvaluationStatus, failureReason string) (evolution.Evaluation, error) {
	if !evolution.ValidEvaluationStatus(status) {
		return evolution.Evaluation{}, fmt.Errorf("%w: %q", evolution.ErrInvalidEvaluationStatus, status)
	}
	evaluation, err := c.FindEvolutionEvaluation(workspace, id)
	if err != nil {
		return evolution.Evaluation{}, err
	}
	failureReason = strings.TrimSpace(failureReason)
	if status != evolution.EvaluationFailed {
		failureReason = ""
	} else if failureReason == "" {
		failureReason = "automatic improvement failed"
	}
	if _, err := c.db.Exec(c.db.Rebind(`
		UPDATE evolution_evaluations
		SET status = ?, failure_reason = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND workspace = ?
	`), status, failureReason, evaluation.ID, evaluation.Workspace); err != nil {
		return evolution.Evaluation{}, fmt.Errorf("UpdateEvolutionEvaluationStatus: %w", err)
	}
	return c.FindEvolutionEvaluation(evaluation.Workspace, evaluation.ID)
}

func (c *DB) Delete(id string) error {
	tx, err := c.db.Beginx()
	if err != nil {
		return fmt.Errorf("Delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM approval_rules WHERE conversation_id = ?
	`), id); err != nil {
		return fmt.Errorf("Delete rules: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM conversation_messages WHERE conversation_id = ?
	`), id); err != nil {
		return fmt.Errorf("Delete messages: %w", err)
	}
	if _, err := tx.Exec(tx.Rebind(`
		DELETE FROM conversations
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

func (c *DB) MigrateLegacyConversations(cachePath string) error {
	dir := filepath.Join(cachePath, "conversations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy conversations: %w", err)
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
				fmt.Errorf("legacy conversation %s has an invalid ID; file retained", path))
			continue
		}
		var exists int
		if err := c.db.Get(&exists, c.db.Rebind(`
			SELECT count(*) FROM conversations WHERE id = ?
		`), id); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("check legacy conversation %s: %w", id, err))
			continue
		}
		if exists == 0 {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("legacy conversation %s has no database metadata; file retained", id))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("read legacy conversation %s: %w", id, err))
			continue
		}
		var messages []proto.Message
		if err := decodeConversationBytes(data, &messages); err != nil {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("decode legacy conversation %s: %w; file retained", id, err))
			continue
		}
		encoded, err := encodeConversation(messages)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("encode legacy conversation %s: %w", id, err))
			continue
		}
		var existing []byte
		existingErr := c.db.Get(&existing, c.db.Rebind(`
			SELECT messages FROM conversation_messages WHERE conversation_id = ?
		`), id)
		if existingErr == nil {
			var existingMessages []proto.Message
			if err := decodeConversationBytes(existing, &existingMessages); err == nil {
				if err := os.Remove(path); err != nil {
					migrationErrors = append(migrationErrors,
						fmt.Errorf("remove migrated legacy conversation %s: %w", id, err))
				}
				continue
			}
		} else if !errors.Is(existingErr, sql.ErrNoRows) {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("check migrated conversation %s: %w", id, existingErr))
			continue
		}
		tx, err := c.db.Beginx()
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("begin legacy migration %s: %w", id, err))
			continue
		}
		if _, err := tx.Exec(tx.Rebind(`
			INSERT INTO conversation_messages (conversation_id, messages)
			VALUES (?, ?)
			ON CONFLICT(conversation_id) DO UPDATE SET messages = excluded.messages
		`), id, encoded); err != nil {
			_ = tx.Rollback()
			migrationErrors = append(migrationErrors, fmt.Errorf("migrate legacy conversation %s: %w", id, err))
			continue
		}
		if err := tx.Commit(); err != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("commit legacy conversation %s: %w", id, err))
			continue
		}
		if err := os.Remove(path); err != nil {
			migrationErrors = append(migrationErrors,
				fmt.Errorf("remove migrated legacy conversation %s: %w", id, err))
		}
	}
	return errors.Join(migrationErrors...)
}

func (c *DB) ListOlderThan(t time.Duration) ([]Conversation, error) {
	var convos []Conversation
	if err := c.db.Select(&convos, c.db.Rebind(`
		SELECT
		  *
		FROM
		  conversations
		WHERE
		  updated_at < ?
		`), time.Now().Add(-t)); err != nil {
		return nil, fmt.Errorf("ListOlderThan: %w", err)
	}
	return convos, nil
}

func (c *DB) FindHEAD() (*Conversation, error) {
	var convo Conversation
	if err := c.db.Get(&convo, `
		SELECT
		  *
		FROM
		  conversations
		ORDER BY
		  updated_at DESC
		LIMIT
		  1
	`); err != nil {
		return nil, fmt.Errorf("FindHead: %w", err)
	}
	return &convo, nil
}

func (c *DB) findByExactTitle(result *[]Conversation, in string) error {
	if err := c.db.Select(result, c.db.Rebind(`
		SELECT
		  *
		FROM
		  conversations
		WHERE
		  title = ?
	`), in); err != nil {
		return fmt.Errorf("findByExactTitle: %w", err)
	}
	return nil
}

func (c *DB) findByIDOrTitle(result *[]Conversation, in string) error {
	if err := c.db.Select(result, c.db.Rebind(`
		SELECT
		  *
		FROM
		  conversations
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
		  conversations
		WHERE
		  id glob ?
		UNION
		SELECT
		  printf ("%s%c%s", title, char(9), substr (id, 1, ?))
		FROM
		  conversations
		WHERE
		  title glob ?
	`), in, ShortIDLength, ShortIDLength, in+"*", ShortIDLength, in+"*"); err != nil {
		return result, fmt.Errorf("Completions: %w", err)
	}
	return result, nil
}

func (c *DB) Find(in string) (*Conversation, error) {
	var conversations []Conversation
	var err error

	if len(in) < MinIDLength {
		err = c.findByExactTitle(&conversations, in)
	} else {
		err = c.findByIDOrTitle(&conversations, in)
	}
	if err != nil {
		return nil, fmt.Errorf("Find %q: %w", in, err)
	}

	if len(conversations) > 1 {
		return nil, fmt.Errorf("%w: %s", ErrManyMatches, in)
	}
	if len(conversations) == 1 {
		return &conversations[0], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNoMatches, in)
}

func (c *DB) List() ([]Conversation, error) {
	var convos []Conversation
	if err := c.db.Select(&convos, `
		SELECT
		  *
		FROM
		  conversations
		ORDER BY
		  updated_at DESC
	`); err != nil {
		return convos, fmt.Errorf("List: %w", err)
	}
	return convos, nil
}
