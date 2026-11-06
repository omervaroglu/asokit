// Package store persists keyword-ranking snapshots in SQLite.
//
// It uses the pure-Go modernc.org/sqlite driver so the binary builds without
// CGO and cross-compiles cleanly.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // register the pure-Go "sqlite" driver with database/sql
)

// Snapshot is one measurement of one keyword at one point in time.
type Snapshot struct {
	ID          int64
	Timestamp   time.Time
	AppID       int64
	Country     string
	Keyword     string
	Position    int // 0 when not found.
	Found       bool
	ResultCount int
}

// Store wraps a SQLite database connection.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and ensures the
// schema exists. Use ":memory:" for an ephemeral database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db %q: %w", path, err)
	}
	// SQLite tolerates only one writer; a single connection avoids
	// "database is locked" under the driver's connection pool.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS snapshots (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	ts           TEXT    NOT NULL,
	app_id       INTEGER NOT NULL,
	country      TEXT    NOT NULL,
	keyword      TEXT    NOT NULL,
	position     INTEGER NOT NULL,
	found        INTEGER NOT NULL,
	result_count INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_lookup
	ON snapshots (app_id, country, keyword, ts);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating schema: %w", err)
	}
	return nil
}

// Insert records a snapshot. Timestamp is set to now (UTC) if zero.
func (s *Store) Insert(snap Snapshot) error {
	if snap.Timestamp.IsZero() {
		snap.Timestamp = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO snapshots (ts, app_id, country, keyword, position, found, result_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snap.Timestamp.UTC().Format(time.RFC3339),
		snap.AppID, snap.Country, snap.Keyword,
		snap.Position, boolToInt(snap.Found), snap.ResultCount,
	)
	if err != nil {
		return fmt.Errorf("inserting snapshot: %w", err)
	}
	return nil
}

// Latest returns the most recent snapshot for each keyword of the given app and
// country, ordered by keyword.
func (s *Store) Latest(appID int64, country string) ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count
		 FROM snapshots
		 WHERE app_id = ? AND country = ?
		   AND ts = (SELECT MAX(ts) FROM snapshots s2
		             WHERE s2.app_id = snapshots.app_id
		               AND s2.country = snapshots.country
		               AND s2.keyword = snapshots.keyword)
		 GROUP BY keyword
		 ORDER BY keyword`,
		appID, country,
	)
	if err != nil {
		return nil, fmt.Errorf("querying latest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSnapshots(rows)
}

// PreviousPosition returns the position from the snapshot immediately before
// the latest one for a keyword, used to compute trend. found reports whether
// such a prior snapshot exists.
func (s *Store) PreviousPosition(appID int64, country, keyword string) (position int, found bool, err error) {
	row := s.db.QueryRow(
		`SELECT position, found FROM snapshots
		 WHERE app_id = ? AND country = ? AND keyword = ?
		 ORDER BY ts DESC
		 LIMIT 1 OFFSET 1`,
		appID, country, keyword,
	)
	var foundInt int
	switch err := row.Scan(&position, &foundInt); err {
	case nil:
		return position, foundInt == 1, nil
	case sql.ErrNoRows:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("querying previous position: %w", err)
	}
}

// All returns every snapshot ordered by timestamp then keyword, for export.
func (s *Store) All() ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count
		 FROM snapshots ORDER BY ts, keyword`)
	if err != nil {
		return nil, fmt.Errorf("querying all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSnapshots(rows)
}

func scanSnapshots(rows *sql.Rows) ([]Snapshot, error) {
	var out []Snapshot
	for rows.Next() {
		var (
			snap     Snapshot
			ts       string
			foundInt int
		)
		if err := rows.Scan(&snap.ID, &ts, &snap.AppID, &snap.Country,
			&snap.Keyword, &snap.Position, &foundInt, &snap.ResultCount); err != nil {
			return nil, fmt.Errorf("scanning snapshot: %w", err)
		}
		snap.Timestamp, _ = time.Parse(time.RFC3339, ts)
		snap.Found = foundInt == 1
		out = append(out, snap)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
