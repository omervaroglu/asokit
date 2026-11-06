package store

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
