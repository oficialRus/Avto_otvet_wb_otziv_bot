package storage

import (
	"context"
	"time"
)

// Store abstracts persistence of processed feedback IDs.
// Implementations must be safe for concurrent use by multiple goroutines.
//
// Exists returns true iff the ID is already present in storage for the given user.
// Save must persist the ID atomically; duplicate inserts should be ignored to simplify caller logic.
// Close frees resources; after Close, the Store should not be used.
type Store interface {
	Exists(ctx context.Context, userID int64, id string) (bool, error)
	Save(ctx context.Context, userID int64, id string) error
	Close() error
}

// UserConfig represents user configuration stored in database.
type UserConfig struct {
	UserID       int64
	WBToken      string // For Wildberries (Bearer token)
	TemplateGood string
	TemplateBad  string
	UpdatedAt    time.Time
}

// Stats represents statistics about users and system.
type Stats struct {
	TotalUsers int64 // Total number of users in the system
}

// ConfigStore abstracts persistence of user configurations.
type ConfigStore interface {
	SaveUserConfig(ctx context.Context, chatID int64, wbToken, tplGood, tplBad string) error
	GetUserConfig(ctx context.Context, chatID int64) (*UserConfig, error)
	DeleteUserConfig(ctx context.Context, chatID int64) error
	GetStats(ctx context.Context) (*Stats, error) // Get statistics about users
}