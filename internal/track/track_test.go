package track

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omervaroglu/asokit/internal/config"
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

	cfg := &config.Config{AppID: 42, Country: "us", Keywords: []string{"a", "b"}, SearchLimit: 50}
	res, err := RunOnce(context.Background(), st, cfg, itunes.New(), 0, nil)
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
