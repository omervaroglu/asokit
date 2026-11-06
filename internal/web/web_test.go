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

func TestDataWithHistory(t *testing.T) {
	cfg := &config.Config{AppID: 1, Country: "us", Keywords: []string{"sleep", "calm"}, SearchLimit: 200}
	s, h := newServer(t, cfg)

	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for _, snap := range []store.Snapshot{
		{Timestamp: t0, AppID: 1, Country: "us", Keyword: "sleep", Position: 10, Found: true, ResultCount: 200},
		{Timestamp: t0.Add(24 * time.Hour), AppID: 1, Country: "us", Keyword: "sleep", Position: 6, Found: true, ResultCount: 200},
	} {
		if err := s.st.Insert(snap); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	rr := do(h, http.MethodGet, "/api/data", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var p dataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.Initialized || len(p.Keywords) != 2 {
		t.Fatalf("response = %+v", p)
	}
	if p.Keywords[0].Current == nil || p.Keywords[0].Current.Position != 6 {
		t.Errorf("sleep current = %+v", p.Keywords[0].Current)
	}
	if p.Keywords[1].Current != nil {
		t.Errorf("calm should have no data, got %+v", p.Keywords[1].Current)
	}
}

func TestUninitializedData(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodGet, "/api/data", nil)
	var p dataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Initialized {
		t.Errorf("expected initialized=false before setup")
	}
}

func TestConfigPostThenGet(t *testing.T) {
	s, h := newServer(t, nil)

	body := []byte(`{"app_id":571800810,"country":"TR","keywords":["uyku","  ","bebek uyku"]}`)
	rr := do(h, http.MethodPost, "/api/config", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var saved configResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !saved.Initialized || saved.Country != "tr" || len(saved.Keywords) != 2 {
		t.Fatalf("saved = %+v (country lowercased, empty keyword dropped)", saved)
	}

	// Config file was written to disk.
	if _, err := os.Stat(s.cfgPath); err != nil {
		t.Errorf("config file not written: %v", err)
	}
	// In-memory config now live.
	if cfg, _ := s.snapshot(); cfg == nil || cfg.AppID != 571800810 {
		t.Errorf("in-memory config not updated: %+v", cfg)
	}

	// GET reflects it.
	rr = do(h, http.MethodGet, "/api/config", nil)
	var got configResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Initialized || got.AppID != 571800810 {
		t.Errorf("GET config = %+v", got)
	}
}

func TestConfigPostInvalid(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodPost, "/api/config", []byte(`{"app_id":0,"keywords":[]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	var resp configResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
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

func TestServesIndex(t *testing.T) {
	_, h := newServer(t, nil)
	rr := do(h, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "asokit") {
		t.Errorf("index not served: status=%d", rr.Code)
	}
}
