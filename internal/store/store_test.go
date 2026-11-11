package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInsertAndLatest(t *testing.T) {
	s := newTestStore(t)

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)

	// Two snapshots for the same keyword at different times.
	must(t, s.Insert(Snapshot{Timestamp: t0, AppID: 1, Country: "us", Keyword: "sleep", Position: 10, Found: true, ResultCount: 200}))
	must(t, s.Insert(Snapshot{Timestamp: t1, AppID: 1, Country: "us", Keyword: "sleep", Position: 5, Found: true, ResultCount: 200}))
	must(t, s.Insert(Snapshot{Timestamp: t1, AppID: 1, Country: "us", Keyword: "calm", Position: 0, Found: false, ResultCount: 200}))

	latest, err := s.Latest(1, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("got %d keywords, want 2", len(latest))
	}

	byKeyword := map[string]Snapshot{}
	for _, snap := range latest {
		byKeyword[snap.Keyword] = snap
	}
	if got := byKeyword["sleep"].Position; got != 5 {
		t.Errorf("latest sleep position = %d, want 5 (newest)", got)
	}
	if byKeyword["calm"].Found {
		t.Errorf("calm should be not-found")
	}
}

func TestPreviousPosition(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	must(t, s.Insert(Snapshot{Timestamp: t0, AppID: 1, Country: "us", Keyword: "sleep", Position: 10, Found: true}))
	must(t, s.Insert(Snapshot{Timestamp: t0.Add(time.Hour), AppID: 1, Country: "us", Keyword: "sleep", Position: 7, Found: true}))

	pos, found, err := s.PreviousPosition(1, "us", "sleep")
	if err != nil {
		t.Fatalf("PreviousPosition: %v", err)
	}
	if !found || pos != 10 {
		t.Errorf("previous = (%d, %v), want (10, true)", pos, found)
	}

	// Only one snapshot -> no previous.
	if _, found, _ := s.PreviousPosition(1, "us", "nope"); found {
		t.Errorf("expected no previous for unknown keyword")
	}
}

func TestAll(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	must(t, s.Insert(Snapshot{Timestamp: t0, AppID: 1, Country: "us", Keyword: "a", Position: 1, Found: true}))
	must(t, s.Insert(Snapshot{Timestamp: t0, AppID: 1, Country: "us", Keyword: "b", Position: 2, Found: true}))

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d, want 2", len(all))
	}
}

func TestInsertSnapshotWithResultsAndMetrics(t *testing.T) {
	s := newTestStore(t)

	diff, rel := 72.5, 6.0
	narrow := true
	id, err := s.InsertSnapshot(
		Snapshot{
			AppID: 1, Country: "us", Keyword: "sleep", Position: 3, Found: true,
			ResultCount: 180, Difficulty: &diff, Relevance: &rel, Narrow: &narrow,
		},
		[]Result{
			{Position: 1, TrackID: 10, Name: "Alpha", RatingCount: 1000, RatingAvg: 4.5, Genre: "Health", ReleaseDate: "2019-01-01T00:00:00Z"},
			{Position: 2, TrackID: 20, Name: "Beta", RatingCount: 50, RatingAvg: 3.9, Genre: "Health", ReleaseDate: "2022-06-01T00:00:00Z"},
		},
	)
	if err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}
	if id <= 0 {
		t.Fatalf("snapshot id = %d, want positive", id)
	}

	// Metrics round-trip through Latest.
	latest, err := s.Latest(1, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(latest))
	}
	got := latest[0]
	if got.Difficulty == nil || *got.Difficulty != diff {
		t.Errorf("Difficulty = %v, want %v", got.Difficulty, diff)
	}
	if got.Relevance == nil || *got.Relevance != rel {
		t.Errorf("Relevance = %v, want %v", got.Relevance, rel)
	}
	if got.Narrow == nil || *got.Narrow != true {
		t.Errorf("Narrow = %v, want true", got.Narrow)
	}

	// Results round-trip, ordered by position.
	results, err := s.Results(id)
	if err != nil {
		t.Fatalf("Results: %v", err)
	}
	if len(results) != 2 || results[0].TrackID != 10 || results[1].TrackID != 20 {
		t.Fatalf("results = %+v, want track ids [10 20] in order", results)
	}
	if results[0].RatingCount != 1000 || results[0].RatingAvg != 4.5 || results[0].Genre != "Health" {
		t.Errorf("result[0] = %+v, unexpected fields", results[0])
	}
}

func TestCompetitorPositions(t *testing.T) {
	s := newTestStore(t)
	id, err := s.InsertSnapshot(
		Snapshot{AppID: 1, Country: "us", Keyword: "k", Position: 5, Found: true, ResultCount: 200},
		[]Result{
			{Position: 1, TrackID: 100, Name: "A"},
			{Position: 2, TrackID: 200, Name: "B"},
			{Position: 3, TrackID: 300, Name: "C"},
		},
	)
	if err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}

	// 200 is stored (→ #2), 999 is not among the results (→ absent).
	got, err := s.CompetitorPositions(id, []int64{200, 999})
	if err != nil {
		t.Fatalf("CompetitorPositions: %v", err)
	}
	if got[200] != 2 {
		t.Errorf("competitor 200 position = %d, want 2", got[200])
	}
	if _, ok := got[999]; ok {
		t.Errorf("competitor 999 should be absent, got %v", got[999])
	}

	// No ids → empty map, no query.
	if m, err := s.CompetitorPositions(id, nil); err != nil || len(m) != 0 {
		t.Errorf("empty ids = (%v, %v), want ({}, nil)", m, err)
	}
}

func TestReleasesDedupAndOrder(t *testing.T) {
	s := newTestStore(t)
	d := func(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }

	must(t, s.UpsertRelease(1, "us", "1.1.0", d(3)))
	must(t, s.UpsertRelease(1, "us", "1.0.0", d(1)))
	must(t, s.UpsertRelease(1, "us", "1.1.0", d(9))) // duplicate version — ignored
	must(t, s.UpsertRelease(1, "de", "1.0.0", d(1))) // other storefront
	must(t, s.UpsertRelease(1, "us", "", d(5)))      // empty version — skipped

	rels, err := s.Releases(1, "us")
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("got %d releases, want 2 (dedup + skip empty): %+v", len(rels), rels)
	}
	// Oldest first, and the duplicate kept its first date (day 3, not 9).
	if rels[0].Version != "1.0.0" || rels[1].Version != "1.1.0" {
		t.Errorf("order = %+v, want 1.0.0 then 1.1.0", rels)
	}
	if !rels[1].ReleasedAt.Equal(d(3)) {
		t.Errorf("duplicate release date = %v, want %v (first write kept)", rels[1].ReleasedAt, d(3))
	}
}

func TestLatestForKeywordAndCompetitorSeries(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Two snapshots of "loan"; competitor 7 moves #3 → #2, competitor 8 only
	// appears in the second snapshot.
	id1, err := s.InsertSnapshot(
		Snapshot{Timestamp: t0, AppID: 1, Country: "us", Keyword: "loan", Position: 5, Found: true, ResultCount: 200},
		[]Result{{Position: 3, TrackID: 7, Name: "Seven"}},
	)
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := s.InsertSnapshot(
		Snapshot{Timestamp: t0.Add(time.Hour), AppID: 1, Country: "us", Keyword: "loan", Position: 4, Found: true, ResultCount: 200},
		[]Result{{Position: 2, TrackID: 7, Name: "Seven"}, {Position: 4, TrackID: 8, Name: "Eight"}},
	); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	_ = id1

	latest, ok, err := s.LatestForKeyword(1, "us", "loan")
	if err != nil || !ok {
		t.Fatalf("LatestForKeyword: ok=%v err=%v", ok, err)
	}
	if latest.Position != 4 {
		t.Errorf("latest position = %d, want 4 (newest)", latest.Position)
	}

	if _, ok, _ := s.LatestForKeyword(1, "us", "absent"); ok {
		t.Errorf("expected no snapshot for unknown keyword")
	}

	series, err := s.CompetitorSeries(1, "us", "loan", []int64{7, 8})
	if err != nil {
		t.Fatalf("CompetitorSeries: %v", err)
	}
	if len(series[7]) != 2 || series[7][0].Position != 3 || series[7][1].Position != 2 {
		t.Errorf("competitor 7 series = %+v, want #3 then #2", series[7])
	}
	if len(series[8]) != 1 || series[8][0].Position != 4 {
		t.Errorf("competitor 8 series = %+v, want one point #4", series[8])
	}
}

func TestPlainInsertLeavesMetricsNull(t *testing.T) {
	s := newTestStore(t)
	must(t, s.Insert(Snapshot{AppID: 1, Country: "us", Keyword: "k", Position: 1, Found: true, ResultCount: 200}))
	latest, err := s.Latest(1, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	got := latest[0]
	if got.Difficulty != nil || got.Relevance != nil || got.Narrow != nil {
		t.Errorf("metrics should be nil for a plain Insert, got %v %v %v", got.Difficulty, got.Relevance, got.Narrow)
	}
}

// TestMigrationUpgradesExistingDatabase simulates a database created before the
// metrics feature: a snapshots table without the metric columns and no results
// table. Opening it must add the columns and keep existing rows intact.
func TestMigrationUpgradesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// Build the pre-metrics schema directly and seed a row.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL, app_id INTEGER NOT NULL,
		country TEXT NOT NULL, keyword TEXT NOT NULL, position INTEGER NOT NULL,
		found INTEGER NOT NULL, result_count INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO snapshots (ts, app_id, country, keyword, position, found, result_count)
		VALUES ('2026-01-01T00:00:00Z', 1, 'us', 'legacy', 4, 1, 200)`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	_ = raw.Close()

	// Opening through the store runs the migration.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migrate): %v", err)
	}
	defer func() { _ = s.Close() }()

	// Old row survives and new columns read back as NULL (nil).
	latest, err := s.Latest(1, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 1 || latest[0].Keyword != "legacy" || latest[0].Position != 4 {
		t.Fatalf("legacy row not preserved: %+v", latest)
	}
	if latest[0].Difficulty != nil || latest[0].Narrow != nil {
		t.Errorf("legacy metrics should be nil, got %v %v", latest[0].Difficulty, latest[0].Narrow)
	}

	// Migration is idempotent: a second Open must not fail or duplicate columns.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = s2.Close()

	// The new column accepts a value now.
	diff := 10.0
	if _, err := s.InsertSnapshot(Snapshot{AppID: 1, Country: "us", Keyword: "new", Position: 1, Found: true, ResultCount: 200, Difficulty: &diff}, nil); err != nil {
		t.Fatalf("InsertSnapshot after migrate: %v", err)
	}
}

// TestAppMeta: upsert caches an app's name/icon, a second upsert refreshes it,
// and a blank name is ignored so good metadata is never wiped.
func TestAppMeta(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if _, ok, _ := st.AppMeta(42); ok {
		t.Fatal("expected no metadata before any upsert")
	}
	must(t, st.UpsertAppMeta(42, "Calm", "https://cdn/icon1.png"))
	m, ok, err := st.AppMeta(42)
	if err != nil || !ok || m.Name != "Calm" || m.IconURL != "https://cdn/icon1.png" {
		t.Fatalf("after upsert: %+v ok=%v err=%v", m, ok, err)
	}

	// Refresh replaces name + icon.
	must(t, st.UpsertAppMeta(42, "Calm — Sleep", "https://cdn/icon2.png"))
	m, _, _ = st.AppMeta(42)
	if m.Name != "Calm — Sleep" || m.IconURL != "https://cdn/icon2.png" {
		t.Errorf("refresh didn't apply: %+v", m)
	}

	// Blank name is a no-op (never wipes existing metadata).
	must(t, st.UpsertAppMeta(42, "", ""))
	m, _, _ = st.AppMeta(42)
	if m.Name != "Calm — Sleep" {
		t.Errorf("blank upsert wiped metadata: %+v", m)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
