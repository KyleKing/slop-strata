// Package cache stores per-blob analysis in SQLite so a run only ever scores
// the blobs it has not seen, keyed by blob id plus the grammar and rule
// versions that produced the score.
package cache

import (
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
func Open(path string) (*Store, error) {
	if path == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening cache: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close() //nolint:errcheck // an open that half-fails leaves nothing to lose

		return nil, fmt.Errorf("migrating cache: %w", err)
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
func (s *Store) Get(sha string, grammar, rules int) ([]byte, bool) {
	if s.db == nil {
		return nil, false
	}
	var payload []byte
	err := s.db.QueryRow(
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
func (s *Store) Put(sha string, grammar, rules int, payload []byte) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec( //nolint:errcheck // a failed write loses only a memo; the answer was already computed
		"INSERT OR REPLACE INTO blobs (sha, grammar, rules, payload) VALUES (?, ?, ?, ?)",
		sha, grammar, rules, payload)
}

// Close releases the database.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}

	return s.db.Close()
}
