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
	// Check if old table exists (without user_id)
	var oldTableCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='processed'`).Scan(&oldTableCount)
	oldTableExists := oldTableCount > 0
	if err == nil && oldTableExists {
		// Check if table has user_id column
		var hasUserID bool
		rows, err2 := db.Query(`PRAGMA table_info(processed)`)
		err = err2
		if err2 == nil {
			for rows.Next() {
				var cid int
				var name, dataType string
				var notnull, pk int
				var dfltValue interface{}
				rows.Scan(&cid, &name, &dataType, &notnull, &dfltValue, &pk)
				if name == "user_id" {
					hasUserID = true
					break
				}
			}
			rows.Close()
		}
		
		// Migrate old table if needed
		if !hasUserID {
			// Create new table with user_id
			const newTableStmt = `CREATE TABLE IF NOT EXISTS processed_new (
				user_id INTEGER NOT NULL,
				id TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (user_id, id)
			);`
			if _, err := db.Exec(newTableStmt); err != nil {
				return fmt.Errorf("failed to create new processed table: %w", err)
			}
			
			// Migrate old data with user_id = 0 (legacy data)
			const migrateStmt = `INSERT INTO processed_new (user_id, id, created_at) SELECT 0, id, created_at FROM processed;`
			if _, err := db.Exec(migrateStmt); err != nil {
				return fmt.Errorf("failed to migrate old data: %w", err)
			}
			
			// Drop old table and rename new
			if _, err := db.Exec(`DROP TABLE processed;`); err != nil {
				return fmt.Errorf("failed to drop old table: %w", err)
			}
			if _, err := db.Exec(`ALTER TABLE processed_new RENAME TO processed;`); err != nil {
				return fmt.Errorf("failed to rename new table: %w", err)
			}
		}
	} else {
		// Create new table
		const processedStmt = `CREATE TABLE IF NOT EXISTS processed (
			user_id INTEGER NOT NULL,
			id TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, id)
		);`
		if _, err := db.Exec(processedStmt); err != nil {
			return err
		}
	}
	
	// Create index for faster lookups
	const indexStmt = `CREATE INDEX IF NOT EXISTS idx_processed_user_id ON processed(user_id);`
	if _, err := db.Exec(indexStmt); err != nil {
		return err
	}

	// Table for user configurations
	const configStmt = `CREATE TABLE IF NOT EXISTS user_configs (
		user_id INTEGER PRIMARY KEY,
		wb_token TEXT NOT NULL DEFAULT '',
		template_good TEXT NOT NULL DEFAULT '',
		template_bad TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(configStmt); err != nil {
		return err
	}
	
	return nil
}

// Exists checks whether the given ID is already stored for the user.
func (s *sqliteStore) Exists(ctx context.Context, userID int64, id string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM processed WHERE user_id = ? AND id = ? LIMIT 1;`, userID, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return exists == 1, err
}

// Save inserts the ID for the user; duplicate IDs are ignored via INSERT OR IGNORE to keep idempotency.
func (s *sqliteStore) Save(ctx context.Context, userID int64, id string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO processed(user_id, id, created_at) VALUES(?, ?, ?);`, userID, id, time.Now())
	return err
}

// Close closes the underlying *sql.DB.
func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// SaveUserConfig saves or updates user configuration.
func (s *sqliteStore) SaveUserConfig(ctx context.Context, chatID int64, wbToken, tplGood, tplBad string) error {
	const stmt = `INSERT INTO user_configs (user_id, wb_token, template_good, template_bad, updated_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(user_id) DO UPDATE SET
            wb_token = excluded.wb_token,
            template_good = excluded.template_good,
            template_bad = excluded.template_bad,
            updated_at = excluded.updated_at;`
	_, err := s.db.ExecContext(ctx, stmt, chatID, wbToken, tplGood, tplBad, time.Now())
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
// Also deletes all processed feedback IDs for this user.
func (s *sqliteStore) DeleteUserConfig(ctx context.Context, chatID int64) error {
	// Delete processed feedbacks for this user
	const deleteProcessedStmt = `DELETE FROM processed WHERE user_id = ?;`
	if _, err := s.db.ExecContext(ctx, deleteProcessedStmt, chatID); err != nil {
		return fmt.Errorf("failed to delete processed feedbacks: %w", err)
	}
	
	// Delete user config
	const deleteConfigStmt = `DELETE FROM user_configs WHERE user_id = ?;`
	_, err := s.db.ExecContext(ctx, deleteConfigStmt, chatID)
	return err
}

// GetStats retrieves statistics about users.
func (s *sqliteStore) GetStats(ctx context.Context) (*Stats, error) {
	var totalUsers int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM user_configs`).Scan(&totalUsers)
	if err != nil {
		return nil, err
	}
	return &Stats{
		TotalUsers: totalUsers,
	}, nil
}
