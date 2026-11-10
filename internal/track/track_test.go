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

	res, err := RunOnce(context.Background(), st, 42, "us", []string{"a", "b"}, 50, itunes.New(), 0, nil)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
