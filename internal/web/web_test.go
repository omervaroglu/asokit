package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/store"
)

func TestHandlerData(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for _, s := range []store.Snapshot{
		{Timestamp: t0, AppID: 1, Country: "us", Keyword: "sleep", Position: 10, Found: true, ResultCount: 200},
		{Timestamp: t0.Add(24 * time.Hour), AppID: 1, Country: "us", Keyword: "sleep", Position: 6, Found: true, ResultCount: 200},
	} {
		if err := st.Insert(s); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	cfg := &config.Config{AppID: 1, Country: "us", Keywords: []string{"sleep", "calm"}, SearchLimit: 200}
	h, err := Handler(st, cfg)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	// /api/data
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/data", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var p payload
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if p.AppID != 1 || p.Country != "us" {
		t.Errorf("unexpected header: %+v", p)
	}
	if len(p.Keywords) != 2 {
		t.Fatalf("got %d keywords, want 2 (config order preserved)", len(p.Keywords))
	}

	sleep := p.Keywords[0]
	if sleep.Keyword != "sleep" || len(sleep.Series) != 2 {
		t.Fatalf("sleep series = %+v", sleep)
	}
	if sleep.Current == nil || sleep.Current.Position != 6 {
		t.Errorf("sleep current = %+v, want position 6", sleep.Current)
	}
	if sleep.Previous == nil || sleep.Previous.Position != 10 {
		t.Errorf("sleep previous = %+v, want position 10", sleep.Previous)
	}

	calm := p.Keywords[1]
	if calm.Keyword != "calm" || len(calm.Series) != 0 || calm.Current != nil {
		t.Errorf("calm should have no data yet, got %+v", calm)
	}
}

func TestHandlerServesIndex(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{AppID: 1, Country: "us", Keywords: []string{"x"}, SearchLimit: 200}
	h, err := Handler(st, cfg)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "asokit") {
		t.Errorf("index.html did not render the app shell")
	}
}
