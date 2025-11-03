package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore is a lightweight implementation based on SQLite.
// It keeps a single table `processed(id TEXT PRIMARY KEY)`.
// We rely on SQLite's implicit WAL-mode concurrency. For write-heavy loads
// consider moving to Redis/Postgres, but for MVP it is sufficient and easy
// to embed.
//
// Uses modernc.org/sqlite driver — pure Go, so no CGO headaches in CI/CD.
// Tested with Go 1.22.
type sqliteStore struct {
	db *sql.DB
}

// NewSQLite opens (or creates) the database at the given path and ensures the
// schema exists. Caller is responsible for calling Close() when done.
// Returns both Store and ConfigStore interfaces.
func NewSQLite(path string) (Store, ConfigStore, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	// Set reasonable timeouts & pool sizes; SQLite ignores many but keeps API consistent.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	store := &sqliteStore{db: db}
	return store, store, nil
}

func migrate(db *sql.DB) error {
	// Table for processed feedback IDs
	const processedStmt = `CREATE TABLE IF NOT EXISTS processed (
        id TEXT PRIMARY KEY,
        created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
    );`
	if _, err := db.Exec(processedStmt); err != nil {
		return err
	}

	// Table for user configurations
	const configStmt = `CREATE TABLE IF NOT EXISTS user_configs (
        user_id INTEGER PRIMARY KEY,
        wb_token TEXT NOT NULL,
        template_good TEXT NOT NULL,
        template_bad TEXT NOT NULL,
        updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
    );`
	_, err := db.Exec(configStmt)
	return err
}

// Exists checks whether the given ID is already stored.
func (s *sqliteStore) Exists(ctx context.Context, id string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM processed WHERE id = ? LIMIT 1;`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return exists == 1, err
}

// Save inserts the ID; duplicate IDs are ignored via INSERT OR IGNORE to keep idempotency.
func (s *sqliteStore) Save(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO processed(id, created_at) VALUES(?, ?);`, id, time.Now())
	return err
}

// Close closes the underlying *sql.DB.
func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// SaveUserConfig saves or updates user configuration.
func (s *sqliteStore) SaveUserConfig(ctx context.Context, chatID int64, token, tplGood, tplBad string) error {
	const stmt = `INSERT INTO user_configs (user_id, wb_token, template_good, template_bad, updated_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(user_id) DO UPDATE SET
            wb_token = excluded.wb_token,
            template_good = excluded.template_good,
            template_bad = excluded.template_bad,
            updated_at = excluded.updated_at;`
	_, err := s.db.ExecContext(ctx, stmt, chatID, token, tplGood, tplBad, time.Now())
	return err
}

// GetUserConfig retrieves user configuration by chat ID.
func (s *sqliteStore) GetUserConfig(ctx context.Context, chatID int64) (*UserConfig, error) {
	const stmt = `SELECT user_id, wb_token, template_good, template_bad, updated_at
        FROM user_configs WHERE user_id = ? LIMIT 1;`
	var cfg UserConfig
	err := s.db.QueryRowContext(ctx, stmt, chatID).Scan(
		&cfg.UserID,
		&cfg.WBToken,
		&cfg.TemplateGood,
		&cfg.TemplateBad,
		&cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DeleteUserConfig removes user configuration from database.
func (s *sqliteStore) DeleteUserConfig(ctx context.Context, chatID int64) error {
	const stmt = `DELETE FROM user_configs WHERE user_id = ?;`
	_, err := s.db.ExecContext(ctx, stmt, chatID)
	return err
}
