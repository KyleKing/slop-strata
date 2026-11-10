// Package cache stores per-blob analysis in SQLite so a run only ever scores
// the blobs it has not seen. An entry is keyed by blob id, the path class the
// blob was read under, and the digest of the code that produced it.
package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store is a SQLite-backed blob cache. A nil Store is valid and answers every
// Get as a miss, which lets runs without a cache directory proceed.
type Store struct {
	db *sql.DB
}

// dirPerm is the mode for the cache directory: owner-only.
const dirPerm = 0o750

const schema = `
CREATE TABLE IF NOT EXISTS analyses (
	sha TEXT NOT NULL,
	class TEXT NOT NULL,
	analysis TEXT NOT NULL,
	payload BLOB NOT NULL,
	PRIMARY KEY (sha, class, analysis)
)`

// openAttempts bounds the busy retry: parallel slop-strata invocations race
// the WAL transition and first schema write, and the journal-mode pragma
// answers BUSY without honoring the busy timeout, so opening retries at a
// fixed pace instead.
const openAttempts = 50

// openRetryWait paces the busy retry.
const openRetryWait = 100 * time.Millisecond

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
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	var err error
	for range openAttempts {
		db, opened := openStore(ctx, dsn)
		if opened == nil {
			return &Store{db: db}, nil
		}
		err = opened
		if !isBusy(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("opening cache: %w", ctx.Err())
		case <-time.After(openRetryWait):
		}
	}

	return nil, err
}

// openStore opens the database and writes the schema once.
func openStore(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening cache: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("migrating cache: %w", errors.Join(err, db.Close()))
	}

	return db, nil
}

// isBusy reports whether err is a SQLite BUSY result, the failure the open
// path retries. Extended busy codes share the low byte.
func isBusy(err error) bool {
	var sqlErr *sqlite.Error
	if errors.As(err, &sqlErr) {
		return sqlErr.Code()&0xFF == sqlite3.SQLITE_BUSY
	}

	return false
}

// Path answers where the cache file lives: one database per tool version
// under the user cache directory, shared across repositories because the key
// is the blob's content address, not anything about where the blob came
// from. The version in the filename is what a CI cache restores on.
func Path(toolVersion string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache directory: %w", err)
	}
	if toolVersion == "" {
		toolVersion = "dev"
	}

	return filepath.Join(base, "slop-strata", "blobs-"+toolVersion+".db"), nil
}

// Get returns the payload stored for a blob under a path class and analysis key.
func (s *Store) Get(ctx context.Context, sha, class, analysis string) ([]byte, bool) {
	if s.db == nil {
		return nil, false
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT payload FROM analyses WHERE sha = ? AND class = ? AND analysis = ?",
		sha, class, analysis).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		return nil, false
	}

	return payload, true
}

// Put stores a blob's payload under a path class and analysis key.
func (s *Store) Put(ctx context.Context, sha, class, analysis string, payload []byte) {
	if s.db == nil {
		return
	}
	// A failed write loses only a memo; the answer was already computed.
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO analyses (sha, class, analysis, payload) VALUES (?, ?, ?, ?)",
		sha, class, analysis, payload); err != nil {
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
