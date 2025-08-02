package storage

import "context"

// Store abstracts persistence of processed feedback IDs.
// Implementations must be safe for concurrent use by multiple goroutines.
//
// Exists returns true iff the ID is already present in storage.
// Save must persist the ID atomically; duplicate inserts should be ignored to simplify caller logic.
// Close frees resources; after Close, the Store should not be used.
type Store interface {
	Exists(ctx context.Context, id string) (bool, error)
	Save(ctx context.Context, id string) error
	Close() error
}
