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

	// Competition/relevance metrics, all nullable: nil means "not computed for
	// this snapshot" (e.g. snapshots stored before the metrics feature existed).
	// Difficulty is 0–100, Relevance is 0–10, Narrow marks a low-competition term
	// (fewer than the API's 200-result ceiling).
	Difficulty *float64
	Relevance  *float64
	Narrow     *bool
}

// Result is one stored search result (a competitor row) belonging to a snapshot.
// Only the top N results of each snapshot are kept, controlled by store_top.
type Result struct {
	Position    int
	TrackID     int64
	Name        string
	RatingCount int
	RatingAvg   float64
	Genre       string
	ReleaseDate string
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
	ON snapshots (app_id, country, keyword, ts);

CREATE TABLE IF NOT EXISTS results (
	snapshot_id  INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
	position     INTEGER NOT NULL,
	track_id     INTEGER NOT NULL,
	name         TEXT    NOT NULL,
	rating_count INTEGER NOT NULL,
	rating_avg   REAL    NOT NULL,
	genre        TEXT    NOT NULL,
	release_date TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_results_snapshot ON results (snapshot_id);

CREATE TABLE IF NOT EXISTS releases (
	app_id      INTEGER NOT NULL,
	country     TEXT    NOT NULL,
	version     TEXT    NOT NULL,
	released_at TEXT    NOT NULL,
	UNIQUE (app_id, country, version)
);
CREATE INDEX IF NOT EXISTS idx_releases_lookup ON releases (app_id, country, released_at);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrating schema: %w", err)
	}

	// Metric columns were added after the first release, so an existing database
	// may not have them yet. Add each only if missing, preserving all data.
	for _, c := range []struct{ name, decl string }{
		{"difficulty", "REAL"},
		{"relevance", "REAL"},
		{"narrow", "INTEGER"},
	} {
		if err := s.ensureColumn("snapshots", c.name, c.decl); err != nil {
			return err
		}
	}

	// Enforce the results→snapshots foreign key (SQLite leaves it off by
	// default). Best-effort: the app never deletes snapshots today, so this is
	// a safety net rather than a correctness requirement.
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}
	return nil
}

// ensureColumn adds a column to a table if it is not already present, making the
// migration idempotent and safe to run against an existing database.
func (s *Store) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspecting %s columns: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
			return fmt.Errorf("scanning %s columns: %w", table, err)
		}
		if name == column {
			return rows.Err() // already present, nothing to do
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// table/column/decl are package-internal constants (SQLite has no parameter
	// placeholders for identifiers), so the formatted statement is not injectable.
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)); err != nil {
		return fmt.Errorf("adding column %s.%s: %w", table, column, err)
	}
	return nil
}

// Insert records a snapshot without any stored results. Timestamp is set to now
// (UTC) if zero. It is kept for callers that only track position; use
// InsertSnapshot to also persist the top-N results.
func (s *Store) Insert(snap Snapshot) error {
	_, err := s.InsertSnapshot(snap, nil)
	return err
}

// InsertSnapshot records a snapshot together with its top-N results in a single
// transaction and returns the new snapshot id. Timestamp is set to now (UTC) if
// zero. Metric fields (Difficulty/Relevance/Narrow) are stored when non-nil.
func (s *Store) InsertSnapshot(snap Snapshot, results []Result) (int64, error) {
	if snap.Timestamp.IsZero() {
		snap.Timestamp = time.Now().UTC()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	res, err := tx.Exec(
		`INSERT INTO snapshots
		   (ts, app_id, country, keyword, position, found, result_count, difficulty, relevance, narrow)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.Timestamp.UTC().Format(time.RFC3339),
		snap.AppID, snap.Country, snap.Keyword,
		snap.Position, boolToInt(snap.Found), snap.ResultCount,
		nullFloat(snap.Difficulty), nullFloat(snap.Relevance), nullBool(snap.Narrow),
	)
	if err != nil {
		return 0, fmt.Errorf("inserting snapshot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading snapshot id: %w", err)
	}

	for _, r := range results {
		if _, err := tx.Exec(
			`INSERT INTO results
			   (snapshot_id, position, track_id, name, rating_count, rating_avg, genre, release_date)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.Position, r.TrackID, r.Name, r.RatingCount, r.RatingAvg, r.Genre, r.ReleaseDate,
		); err != nil {
			return 0, fmt.Errorf("inserting result: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing snapshot: %w", err)
	}
	return id, nil
}

// Release is one app version and when it shipped, for drawing version markers.
type Release struct {
	Version    string
	ReleasedAt time.Time
}

// UpsertRelease records an app version for a storefront, ignoring a version
// already stored (versions are deduplicated per app+country).
func (s *Store) UpsertRelease(appID int64, country, version string, releasedAt time.Time) error {
	if version == "" {
		return nil // nothing useful to record
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO releases (app_id, country, version, released_at)
		 VALUES (?, ?, ?, ?)`,
		appID, country, version, releasedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upserting release: %w", err)
	}
	return nil
}

// Releases returns the stored versions for an app and country, oldest first.
func (s *Store) Releases(appID int64, country string) ([]Release, error) {
	rows, err := s.db.Query(
		`SELECT version, released_at FROM releases
		 WHERE app_id = ? AND country = ? ORDER BY released_at`,
		appID, country,
	)
	if err != nil {
		return nil, fmt.Errorf("querying releases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Release
	for rows.Next() {
		var (
			version string
			ts      string
		)
		if err := rows.Scan(&version, &ts); err != nil {
			return nil, fmt.Errorf("scanning release: %w", err)
		}
		r := Release{Version: version}
		r.ReleasedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PositionPoint is one competitor rank at one time, for a per-keyword series.
type PositionPoint struct {
	Timestamp time.Time
	Position  int
}

// LatestForKeyword returns the most recent snapshot for one keyword of an app
// and country. found reports whether any snapshot exists.
func (s *Store) LatestForKeyword(appID int64, country, keyword string) (Snapshot, bool, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count, difficulty, relevance, narrow
		 FROM snapshots
		 WHERE app_id = ? AND country = ? AND keyword = ?
		 ORDER BY ts DESC LIMIT 1`,
		appID, country, keyword,
	)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("querying latest for keyword: %w", err)
	}
	defer func() { _ = rows.Close() }()
	snaps, err := scanSnapshots(rows)
	if err != nil {
		return Snapshot{}, false, err
	}
	if len(snaps) == 0 {
		return Snapshot{}, false, nil
	}
	return snaps[0], true, nil
}

// CompetitorSeries returns, per competitor track id, that competitor's stored
// position over time for one keyword. A competitor only appears at the
// snapshots where it was within the stored top-N.
func (s *Store) CompetitorSeries(appID int64, country, keyword string, ids []int64) (map[int64][]PositionPoint, error) {
	out := make(map[int64][]PositionPoint, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	args := []any{appID, country, keyword}
	placeholders := make([]byte, 0, len(ids)*2)
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}
	query := `SELECT s.ts, r.track_id, r.position
		 FROM snapshots s JOIN results r ON r.snapshot_id = s.id
		 WHERE s.app_id = ? AND s.country = ? AND s.keyword = ?
		   AND r.track_id IN (` + string(placeholders) + `)
		 ORDER BY s.ts`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying competitor series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			ts       string
			trackID  int64
			position int
		)
		if err := rows.Scan(&ts, &trackID, &position); err != nil {
			return nil, fmt.Errorf("scanning competitor series: %w", err)
		}
		t, _ := time.Parse(time.RFC3339, ts)
		out[trackID] = append(out[trackID], PositionPoint{Timestamp: t, Position: position})
	}
	return out, rows.Err()
}

// CompetitorPositions returns, for one snapshot, the stored position of each of
// the given track ids that appears among the snapshot's results. Ids outside the
// stored top-N are simply absent from the map.
func (s *Store) CompetitorPositions(snapshotID int64, trackIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(trackIDs))
	if len(trackIDs) == 0 {
		return out, nil
	}
	want := make(map[int64]bool, len(trackIDs))
	for _, id := range trackIDs {
		want[id] = true
	}

	rows, err := s.db.Query(`SELECT track_id, position FROM results WHERE snapshot_id = ?`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("querying competitor positions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var trackID int64
		var position int
		if err := rows.Scan(&trackID, &position); err != nil {
			return nil, fmt.Errorf("scanning competitor position: %w", err)
		}
		if want[trackID] {
			out[trackID] = position
		}
	}
	return out, rows.Err()
}

// Results returns the stored results for a snapshot, ordered by position.
func (s *Store) Results(snapshotID int64) ([]Result, error) {
	rows, err := s.db.Query(
		`SELECT position, track_id, name, rating_count, rating_avg, genre, release_date
		 FROM results WHERE snapshot_id = ? ORDER BY position`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("querying results: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Position, &r.TrackID, &r.Name,
			&r.RatingCount, &r.RatingAvg, &r.Genre, &r.ReleaseDate); err != nil {
			return nil, fmt.Errorf("scanning result: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Latest returns the most recent snapshot for each keyword of the given app and
// country, ordered by keyword.
func (s *Store) Latest(appID int64, country string) ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count, difficulty, relevance, narrow
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

// History returns every snapshot for the given app and country, ordered by
// keyword then timestamp — the shape the dashboard needs to draw one time
// series per keyword.
func (s *Store) History(appID int64, country string) ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count, difficulty, relevance, narrow
		 FROM snapshots
		 WHERE app_id = ? AND country = ?
		 ORDER BY keyword, ts`,
		appID, country,
	)
	if err != nil {
		return nil, fmt.Errorf("querying history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSnapshots(rows)
}

// All returns every snapshot ordered by timestamp then keyword, for export.
func (s *Store) All() ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, app_id, country, keyword, position, found, result_count, difficulty, relevance, narrow
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
			snap       Snapshot
			ts         string
			foundInt   int
			difficulty sql.NullFloat64
			relevance  sql.NullFloat64
			narrow     sql.NullInt64
		)
		if err := rows.Scan(&snap.ID, &ts, &snap.AppID, &snap.Country,
			&snap.Keyword, &snap.Position, &foundInt, &snap.ResultCount,
			&difficulty, &relevance, &narrow); err != nil {
			return nil, fmt.Errorf("scanning snapshot: %w", err)
		}
		snap.Timestamp, _ = time.Parse(time.RFC3339, ts)
		snap.Found = foundInt == 1
		if difficulty.Valid {
			snap.Difficulty = &difficulty.Float64
		}
		if relevance.Valid {
			snap.Relevance = &relevance.Float64
		}
		if narrow.Valid {
			b := narrow.Int64 == 1
			snap.Narrow = &b
		}
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

// nullFloat/nullBool turn optional metric values into Exec args: a nil pointer
// becomes a SQL NULL, a set pointer becomes its value.
func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullBool(v *bool) any {
	if v == nil {
		return nil
	}
	return boolToInt(*v)
}
