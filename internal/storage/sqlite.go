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
func NewSQLite(path string) (Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Set reasonable timeouts & pool sizes; SQLite ignores many but keeps API consistent.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sqliteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	const stmt = `CREATE TABLE IF NOT EXISTS processed (
        id TEXT PRIMARY KEY,
        created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
    );`
	_, err := db.Exec(stmt)
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
