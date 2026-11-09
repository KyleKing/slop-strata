// Package cache stores per-blob analysis in SQLite so a run only ever scores
// the blobs it has not seen, keyed by blob id plus the grammar and rule
// versions that produced the score.
package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // the database/sql driver
)

// Store is a SQLite-backed blob cache. A nil Store is valid and answers every
// Get as a miss, which lets runs without a cache directory proceed.
type Store struct {
	db *sql.DB
}

// dirPerm is the mode for the cache directory: owner-only.
const dirPerm = 0o750

const schema = `
CREATE TABLE IF NOT EXISTS blobs (
	sha TEXT NOT NULL,
	grammar INT NOT NULL,
	rules INT NOT NULL,
	payload BLOB NOT NULL,
	PRIMARY KEY (sha, grammar, rules)
)`

// Open opens (creating, if needed) the cache file at path. A nil path or an
// unwritable directory yields a nil store and no error: the cache is an
// accelerator, not a requirement.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening cache: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("migrating cache: %w", errors.Join(err, db.Close()))
	}

	return &Store{db: db}, nil
}

// Path answers where the cache file lives: one database under the user cache
// directory, shared across repositories because the key is the blob's content
// address, not anything about where the blob came from.
func Path() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache directory: %w", err)
	}

	return filepath.Join(base, "slop-strata", "blobs.db"), nil
}

// Get returns the payload stored for a blob under the given versions.
func (s *Store) Get(ctx context.Context, sha string, grammar, rules int) ([]byte, bool) {
	if s.db == nil {
		return nil, false
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT payload FROM blobs WHERE sha = ? AND grammar = ? AND rules = ?",
		sha, grammar, rules).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		return nil, false
	}

	return payload, true
}

// Put stores a blob's payload under the given versions.
func (s *Store) Put(ctx context.Context, sha string, grammar, rules int, payload []byte) {
	if s.db == nil {
		return
	}
	// A failed write loses only a memo; the answer was already computed.
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO blobs (sha, grammar, rules, payload) VALUES (?, ?, ?, ?)",
		sha, grammar, rules, payload); err != nil {
		return
	}
}

// Close releases the database.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing cache: %w", err)
	}

	return nil
}
