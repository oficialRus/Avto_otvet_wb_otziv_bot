package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// postgresStore is a PostgreSQL implementation of Store and ConfigStore.
// It supports multiple concurrent connections and is optimized for high load.
type postgresStore struct {
	db *sql.DB
}

// NewPostgreSQL opens a PostgreSQL connection and ensures the schema exists.
// dsn should be in format: "host=localhost port=5432 user=postgres password=postgres dbname=feedbacks sslmode=disable"
// Returns both Store and ConfigStore interfaces.
func NewPostgreSQL(dsn string) (Store, ConfigStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Set reasonable pool sizes for PostgreSQL
	db.SetMaxOpenConns(25)        // Maximum open connections
	db.SetMaxIdleConns(10)        // Maximum idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Connection lifetime

	// Test connection
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	if err := migratePostgres(db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("failed to migrate postgres schema: %w", err)
	}

	store := &postgresStore{db: db}
	return store, store, nil
}

func migratePostgres(db *sql.DB) error {
	// Create processed table with user_id support
	const processedTable = `
	CREATE TABLE IF NOT EXISTS processed (
		user_id BIGINT NOT NULL,
		id TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, id)
	);
	CREATE INDEX IF NOT EXISTS idx_processed_user_id ON processed(user_id);
	CREATE INDEX IF NOT EXISTS idx_processed_created_at ON processed(created_at);
	`
	if _, err := db.Exec(processedTable); err != nil {
		return fmt.Errorf("failed to create processed table: %w", err)
	}

	// Create user_configs table
	const configTable = `
	CREATE TABLE IF NOT EXISTS user_configs (
		user_id BIGINT PRIMARY KEY,
		wb_token TEXT NOT NULL DEFAULT '',
		template_good TEXT NOT NULL DEFAULT '',
		template_bad TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_user_configs_updated_at ON user_configs(updated_at);
	`
	if _, err := db.Exec(configTable); err != nil {
		return fmt.Errorf("failed to create user_configs table: %w", err)
	}

	return nil
}

// Exists checks whether the given ID is already stored for the user.
func (s *postgresStore) Exists(ctx context.Context, userID int64, id string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM processed WHERE user_id = $1 AND id = $2 LIMIT 1`,
		userID, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return exists == 1, err
}

// Save inserts the ID for the user; duplicate IDs are ignored via ON CONFLICT.
func (s *postgresStore) Save(ctx context.Context, userID int64, id string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed (user_id, id, created_at) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, id) DO NOTHING`,
		userID, id, time.Now())
	return err
}

// Close closes the underlying *sql.DB.
func (s *postgresStore) Close() error {
	return s.db.Close()
}

// SaveUserConfig saves or updates user configuration.
func (s *postgresStore) SaveUserConfig(ctx context.Context, chatID int64, wbToken, tplGood, tplBad string) error {
	const stmt = `
		INSERT INTO user_configs (user_id, wb_token, template_good, template_bad, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			wb_token = EXCLUDED.wb_token,
			template_good = EXCLUDED.template_good,
			template_bad = EXCLUDED.template_bad,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, stmt, chatID, wbToken, tplGood, tplBad, time.Now())
	return err
}

// GetUserConfig retrieves user configuration by chat ID.
func (s *postgresStore) GetUserConfig(ctx context.Context, chatID int64) (*UserConfig, error) {
	const stmt = `
		SELECT user_id, wb_token, template_good, template_bad, updated_at
		FROM user_configs WHERE user_id = $1 LIMIT 1
	`
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
// Also deletes all processed feedback IDs for this user.
func (s *postgresStore) DeleteUserConfig(ctx context.Context, chatID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete processed feedbacks for this user
	if _, err := tx.ExecContext(ctx, `DELETE FROM processed WHERE user_id = $1`, chatID); err != nil {
		return fmt.Errorf("failed to delete processed feedbacks: %w", err)
	}

	// Delete user config
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_configs WHERE user_id = $1`, chatID); err != nil {
		return fmt.Errorf("failed to delete user config: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetStats retrieves statistics about users.
func (s *postgresStore) GetStats(ctx context.Context) (*Stats, error) {
	var totalUsers int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM user_configs`).Scan(&totalUsers)
	if err != nil {
		return nil, err
	}
	return &Stats{
		TotalUsers: totalUsers,
	}, nil
}

