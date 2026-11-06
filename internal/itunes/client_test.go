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
