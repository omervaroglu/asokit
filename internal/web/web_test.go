package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/store"
)

func newServer(t *testing.T, cfg *config.Config) (*Server, http.Handler) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfgPath := filepath.Join(t.TempDir(), "asokit.yaml")
	s := NewServer(st, cfg, "", cfgPath)
	h, err := s.Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return s, h
}

func do(h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

func cfgTR() *config.Config {
	c := &config.Config{AppID: 1, SearchLimit: 200, Markets: []config.Market{
		{Country: "tr", Keywords: []string{"sleep", "calm"}},
	}}
	return c
}

func TestDataWithHistory(t *testing.T) {
	s, h := newServer(t, cfgTR())

	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for _, snap := range []store.Snapshot{
		{Timestamp: t0, AppID: 1, Country: "tr", Keyword: "sleep", Position: 10, Found: true, ResultCount: 200},
		{Timestamp: t0.Add(24 * time.Hour), AppID: 1, Country: "tr", Keyword: "sleep", Position: 6, Found: true, ResultCount: 200},
	} {
		if err := s.st.Insert(snap); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	var p dataResponse
	decode(t, do(h, http.MethodGet, "/api/data", nil), &p)
	if !p.Initialized || p.Country != "tr" || len(p.Keywords) != 2 {
		t.Fatalf("response = %+v", p)
	}
	if p.Keywords[0].Current == nil || p.Keywords[0].Current.Position != 6 {
		t.Errorf("sleep current = %+v", p.Keywords[0].Current)
	}
}

// Switching markets must return that market's own series and never the other's.
func TestDataPerMarketIsolated(t *testing.T) {
	cfg := &config.Config{AppID: 1, SearchLimit: 200, Markets: []config.Market{
		{Country: "tr", Keywords: []string{"uyku"}},
		{Country: "de", Keywords: []string{"schlaf"}},
	}}
	s, h := newServer(t, cfg)
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	must(t, s.st.Insert(store.Snapshot{Timestamp: t0, AppID: 1, Country: "tr", Keyword: "uyku", Position: 3, Found: true}))
	must(t, s.st.Insert(store.Snapshot{Timestamp: t0, AppID: 1, Country: "de", Keyword: "schlaf", Position: 9, Found: true}))

	var de dataResponse
	decode(t, do(h, http.MethodGet, "/api/data?country=de", nil), &de)
	if de.Country != "de" || len(de.Keywords) != 1 || de.Keywords[0].Keyword != "schlaf" {
		t.Fatalf("de response = %+v", de)
	}
	if de.Keywords[0].Current == nil || de.Keywords[0].Current.Position != 9 {
		t.Errorf("de current = %+v, want #9", de.Keywords[0].Current)
	}
	if len(de.Markets) != 2 {
		t.Errorf("markets list = %v, want both", de.Markets)
	}
}

func TestUninitializedData(t *testing.T) {
	_, h := newServer(t, nil)
	var p dataResponse
	decode(t, do(h, http.MethodGet, "/api/data", nil), &p)
	if p.Initialized {
		t.Errorf("expected initialized=false before setup")
	}
}

func TestConfigPostThenGet(t *testing.T) {
	s, h := newServer(t, nil)

	body := []byte(`{"app_id":571800810,"markets":[{"country":"TR","keywords":["uyku","  ","bebek uyku,uyku"]}]}`)
	var saved configResponse
	rr := do(h, http.MethodPost, "/api/config", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rr.Code, rr.Body.String())
	}
	decode(t, rr, &saved)
	if !saved.Initialized || len(saved.Markets) != 1 || saved.Markets[0].Country != "tr" {
		t.Fatalf("saved = %+v", saved)
	}
	// "uyku" duplicate removed, empties dropped, comma split.
	if got := saved.Markets[0].Keywords; len(got) != 2 || got[0] != "uyku" || got[1] != "bebek uyku" {
		t.Fatalf("keywords = %v", got)
	}
	if _, err := os.Stat(s.cfgPath); err != nil {
		t.Errorf("config file not written: %v", err)
	}

	var got configResponse
	decode(t, do(h, http.MethodGet, "/api/config", nil), &got)
	if !got.Initialized || got.AppID != 571800810 {
		t.Errorf("GET config = %+v", got)
	}
}

// A browser "Save" only sends app_id/search_limit/markets. Fields the form does
// not carry (competitors, relevance, store_top) must survive the POST rather
// than being wiped — the config-preservation guarantee from the spec.
func TestConfigPostPreservesUneditedFields(t *testing.T) {
	cfg := &config.Config{
		AppID: 1, SearchLimit: 200, StoreTop: 25,
		Relevance:   config.Relevance{GenreIDs: []string{"6015"}},
		Competitors: []config.Competitor{{ID: 999, Name: "Rival"}},
		Markets:     []config.Market{{Country: "tr", Keywords: []string{"uyku"}}},
	}
	s, h := newServer(t, cfg)

	body := []byte(`{"app_id":1,"search_limit":200,"markets":[{"country":"tr","keywords":["uyku","kira"]}]}`)
	rr := do(h, http.MethodPost, "/api/config", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rr.Code, rr.Body.String())
	}

	// The edited field changed...
	if got := len(s.cfg.Markets[0].Keywords); got != 2 {
		t.Errorf("keywords not updated: %d", got)
	}
	// ...but the unedited fields are intact, both in memory and on disk.
	if s.cfg.StoreTop != 25 {
		t.Errorf("StoreTop wiped: %d", s.cfg.StoreTop)
	}
	if len(s.cfg.Competitors) != 1 || s.cfg.Competitors[0].ID != 999 {
		t.Errorf("competitors wiped: %+v", s.cfg.Competitors)
	}
	if len(s.cfg.Relevance.GenreIDs) != 1 {
		t.Errorf("relevance wiped: %+v", s.cfg.Relevance)
	}
	reloaded, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if len(reloaded.Competitors) != 1 || reloaded.Competitors[0].ID != 999 {
		t.Errorf("competitors not persisted to disk: %+v", reloaded.Competitors)
	}
}

func TestConfigPostInvalid(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodPost, "/api/config", []byte(`{"app_id":0,"markets":[]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	var resp configResponse
	decode(t, rr, &resp)
	if resp.Error == "" {
		t.Errorf("expected a validation error message for the form")
	}
}

func TestRunNotInitialized(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodPost, "/api/run", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("run before setup status = %d, want 400", rr.Code)
	}
}

func TestRunUnknownMarket(t *testing.T) {
	_, h := newServer(t, cfgTR())
	rr := do(h, http.MethodPost, "/api/run?country=xx", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("run unknown market status = %d, want 400", rr.Code)
	}
}

func TestServesIndex(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "asokit") {
		t.Errorf("index not served: status=%d", rr.Code)
	}
}

func decode(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rr.Body.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
