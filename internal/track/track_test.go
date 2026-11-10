package track

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omervaroglu/asokit/internal/itunes"
	"github.com/omervaroglu/asokit/internal/metrics"
	"github.com/omervaroglu/asokit/internal/store"
)

func TestRunOnce(t *testing.T) {
	// Fake iTunes API: app id 42 ranks #2 for "a", is absent for "b".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("term") {
		case "a":
			_, _ = w.Write([]byte(`{"resultCount":2,"results":[{"trackId":1},{"trackId":42}]}`))
		default:
			_, _ = w.Write([]byte(`{"resultCount":1,"results":[{"trackId":1}]}`))
		}
	}))
	defer srv.Close()

	old := itunes.BaseURL
	itunes.BaseURL = srv.URL
	defer func() { itunes.BaseURL = old }()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	rc := RunConfig{
		AppID: 42, Country: "us", Keywords: []string{"a", "b"},
		SearchLimit: 50, StoreTop: 50, Weights: metrics.DefaultWeights,
		Client: itunes.New(), Delay: 0,
	}
	res, err := RunOnce(context.Background(), st, rc, nil)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.Stored != 2 || res.Failures != 0 {
		t.Fatalf("Result = %+v, want Stored=2 Failures=0", res)
	}

	latest, err := st.Latest(42, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	byKeyword := map[string]store.Snapshot{}
	for _, s := range latest {
		byKeyword[s.Keyword] = s
	}
	if got := byKeyword["a"]; !got.Found || got.Position != 2 {
		t.Errorf("keyword a = %+v, want found at #2", got)
	}
	if got := byKeyword["b"]; got.Found {
		t.Errorf("keyword b should be not-found, got %+v", got)
	}
}

// TestRunOnceStoresResultsAndMetrics checks the A-side wiring end to end: the
// top-N results are persisted (truncated to StoreTop) and the snapshot carries
// computed metrics.
func TestRunOnceStoresResultsAndMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Three results; our app (id 42) is #2. Rich fields so metrics compute.
		_, _ = w.Write([]byte(`{"resultCount":3,"results":[
			{"trackId":1,"trackName":"Rival","userRatingCount":1000,"averageUserRating":4.5,"primaryGenreName":"Finance","genreIds":["6015"],"releaseDate":"2019-01-01T00:00:00Z"},
			{"trackId":42,"trackName":"Mine","userRatingCount":10,"primaryGenreName":"Finance","genreIds":["6015"],"releaseDate":"2024-01-01T00:00:00Z"},
			{"trackId":2,"trackName":"Other","userRatingCount":5,"primaryGenreName":"Games","genreIds":["6014"],"releaseDate":"2025-01-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	old := itunes.BaseURL
	itunes.BaseURL = srv.URL
	defer func() { itunes.BaseURL = old }()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	rc := RunConfig{
		AppID: 42, Country: "us", Keywords: []string{"loan"},
		SearchLimit: 200, StoreTop: 2, // store only the top 2 of 3
		Weights:   metrics.DefaultWeights,
		Relevance: metrics.RelevanceCriteria{GenreIDs: []string{"6015"}},
		Client:    itunes.New(),
	}
	if _, err := RunOnce(context.Background(), st, rc, nil); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	latest, err := st.Latest(42, "us")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(latest))
	}
	snap := latest[0]
	if snap.Position != 2 || !snap.Found {
		t.Errorf("position = %d found = %v, want #2 found", snap.Position, snap.Found)
	}
	if snap.Difficulty == nil || snap.Relevance == nil || snap.Narrow == nil {
		t.Fatalf("metrics should be set: %+v", snap)
	}
	if *snap.Narrow != true { // 3 < 200
		t.Errorf("Narrow = %v, want true", *snap.Narrow)
	}
	if *snap.Relevance != 1 { // one Finance competitor (id 1); our app excluded, Games not counted
		t.Errorf("Relevance = %v, want 1", *snap.Relevance)
	}

	results, err := st.Results(snap.ID)
	if err != nil {
		t.Fatalf("Results: %v", err)
	}
	if len(results) != 2 { // truncated to StoreTop=2
		t.Fatalf("stored %d results, want 2 (StoreTop)", len(results))
	}
	if results[0].TrackID != 1 || results[0].Position != 1 || results[1].TrackID != 42 {
		t.Errorf("unexpected stored results: %+v", results)
	}
	if results[0].RatingCount != 1000 || results[0].Genre != "Finance" {
		t.Errorf("result[0] fields not captured: %+v", results[0])
	}
}

func TestCaptureRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{"trackId":42,"version":"1.1.8","currentVersionReleaseDate":"2026-10-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()
	old := itunes.LookupURL
	itunes.LookupURL = srv.URL
	defer func() { itunes.LookupURL = old }()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := CaptureRelease(context.Background(), itunes.New(), st, 42, "us"); err != nil {
		t.Fatalf("CaptureRelease: %v", err)
	}
	rels, err := st.Releases(42, "us")
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(rels) != 1 || rels[0].Version != "1.1.8" {
		t.Fatalf("releases = %+v, want one 1.1.8", rels)
	}
}

// fastBackoff shrinks the retry delay for the duration of a test so it does not
// sleep for real seconds, and restores it afterwards.
func fastBackoff(t *testing.T) {
	t.Helper()
	old := baseBackoff
	baseBackoff = time.Millisecond
	t.Cleanup(func() { baseBackoff = old })
}

// TestSearchRetriesServerError: a 5xx is transient, so Search retries and
// succeeds once the server recovers.
func TestSearchRetriesServerError(t *testing.T) {
	fastBackoff(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= 2 { // fail the first two attempts, then recover
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{"trackId":42}]}`))
	}))
	defer srv.Close()

	old := itunes.BaseURL
	itunes.BaseURL = srv.URL
	defer func() { itunes.BaseURL = old }()

	results, err := Search(context.Background(), itunes.New(), "x", "us", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != 42 {
		t.Fatalf("results = %+v, want one app with id 42", results)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("server hit %d times, want 3 (two failures + one success)", got)
	}
}

// TestSearchRetriesTimeout: a per-request timeout is transient, so Search keeps
// retrying (rather than returning on the first attempt) and eventually gives up
// after exhausting its retries.
func TestSearchRetriesTimeout(t *testing.T) {
	fastBackoff(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond) // always slower than the client timeout
	}))
	defer srv.Close()

	old := itunes.BaseURL
	itunes.BaseURL = srv.URL
	defer func() { itunes.BaseURL = old }()

	client := &itunes.Client{HTTP: &http.Client{Timeout: 20 * time.Millisecond}}
	_, err := Search(context.Background(), client, "x", "us", 50)
	if err == nil {
		t.Fatal("Search: want error, got nil")
	}
	if !strings.Contains(err.Error(), "giving up after") {
		t.Fatalf("error = %v, want a 'giving up after' retry-exhausted error", err)
	}
	if got := hits.Load(); got != maxRetries+1 {
		t.Fatalf("server hit %d times, want %d (initial + %d retries)", got, maxRetries+1, maxRetries)
	}
}
