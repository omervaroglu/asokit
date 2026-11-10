package itunes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("entity"); got != "software" {
			t.Errorf("entity = %q, want software", got)
		}
		if got := r.URL.Query().Get("term"); got != "sleep" {
			t.Errorf("term = %q, want sleep", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultCount":2,"results":[
			{"trackId":111,"trackName":"App One"},
			{"trackId":222,"trackName":"App Two"}]}`))
	}))
	defer srv.Close()

	old := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = old }()

	got, err := New().Search(context.Background(), "sleep", "us", 50)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].ID != 111 || got[0].Name != "App One" {
		t.Errorf("unexpected first result: %+v", got[0])
	}
}

func TestSearchParsesMetricFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{
			"trackId":111,"trackName":"App One",
			"userRatingCount":12345,"averageUserRating":4.6,
			"primaryGenreName":"Finance","genreIds":["6015","6007"],
			"releaseDate":"2020-01-02T00:00:00Z",
			"currentVersionReleaseDate":"2026-10-01T00:00:00Z",
			"sellerName":"Acme Inc."}]}`))
	}))
	defer srv.Close()

	old := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = old }()

	got, err := New().Search(context.Background(), "x", "us", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	a := got[0]
	switch {
	case a.RatingCount != 12345:
		t.Errorf("RatingCount = %d, want 12345", a.RatingCount)
	case a.RatingAvg != 4.6:
		t.Errorf("RatingAvg = %v, want 4.6", a.RatingAvg)
	case a.Genre != "Finance":
		t.Errorf("Genre = %q, want Finance", a.Genre)
	case len(a.GenreIDs) != 2 || a.GenreIDs[0] != "6015" || a.GenreIDs[1] != "6007":
		t.Errorf("GenreIDs = %v, want [6015 6007]", a.GenreIDs)
	case a.ReleaseDate != "2020-01-02T00:00:00Z":
		t.Errorf("ReleaseDate = %q", a.ReleaseDate)
	case a.CurrentVersionReleaseDate != "2026-10-01T00:00:00Z":
		t.Errorf("CurrentVersionReleaseDate = %q", a.CurrentVersionReleaseDate)
	case a.SellerName != "Acme Inc.":
		t.Errorf("SellerName = %q, want Acme Inc.", a.SellerName)
	}
}

func TestLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("id"); got != "42" {
			t.Errorf("id = %q, want 42", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultCount":1,"results":[{
			"trackId":42,"trackName":"Mine","primaryGenreName":"Finance",
			"genreIds":["6015"],"version":"1.1.8",
			"currentVersionReleaseDate":"2026-10-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	old := LookupURL
	LookupURL = srv.URL
	defer func() { LookupURL = old }()

	app, err := New().Lookup(context.Background(), 42, "us")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if app.ID != 42 || app.Version != "1.1.8" || len(app.GenreIDs) != 1 || app.GenreIDs[0] != "6015" {
		t.Errorf("unexpected lookup result: %+v", app)
	}
}

func TestLookupNoResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"resultCount":0,"results":[]}`))
	}))
	defer srv.Close()

	old := LookupURL
	LookupURL = srv.URL
	defer func() { LookupURL = old }()

	if _, err := New().Lookup(context.Background(), 999, "us"); err == nil {
		t.Fatal("expected an error for an empty lookup result")
	}
}

func TestSearchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	old := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = old }()

	_, err := New().Search(context.Background(), "x", "us", 10)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %v", err)
	}
}
