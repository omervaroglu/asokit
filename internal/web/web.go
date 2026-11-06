// Package web serves the asokit dashboard: a self-contained single-page UI
// (embedded in the binary) that can both read the local store and, when the
// config has not been set up yet, collect it from the browser.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/itunes"
	"github.com/omervaroglu/asokit/internal/store"
	"github.com/omervaroglu/asokit/internal/track"
)

//go:embed static
var staticFS embed.FS

// runDelay is the pause between keyword requests during a browser-triggered run.
const runDelay = 1500 * time.Millisecond

// point is one measurement in a keyword's time series.
type point struct {
	TS       string `json:"ts"`
	Position int    `json:"position"`
	Found    bool   `json:"found"`
}

// keywordData is the series and summary for a single keyword.
type keywordData struct {
	Keyword  string  `json:"keyword"`
	Current  *point  `json:"current"`
	Previous *point  `json:"previous"`
	Series   []point `json:"series"`
}

type dataResponse struct {
	Initialized bool          `json:"initialized"`
	AppID       int64         `json:"app_id"`
	Country     string        `json:"country"`
	GeneratedAt string        `json:"generated_at"`
	Keywords    []keywordData `json:"keywords"`
}

type configResponse struct {
	Initialized bool     `json:"initialized"`
	Error       string   `json:"error,omitempty"`
	AppID       int64    `json:"app_id"`
	Country     string   `json:"country"`
	Keywords    []string `json:"keywords"`
	SearchLimit int      `json:"search_limit"`
}

type configRequest struct {
	AppID       int64    `json:"app_id"`
	Country     string   `json:"country"`
	Keywords    []string `json:"keywords"`
	SearchLimit int      `json:"search_limit"`
}

// Server owns the mutable config state behind the dashboard.
type Server struct {
	st      *store.Store
	client  *itunes.Client
	cfgPath string

	mu     sync.RWMutex
	cfg    *config.Config // nil until initialized
	cfgErr string         // parse error when an existing file is malformed

	runMu sync.Mutex // serializes browser-triggered runs
}

// NewServer builds a dashboard server. cfg may be nil (not set up yet); cfgErr
// holds the parse error when an existing config file was malformed.
func NewServer(st *store.Store, cfg *config.Config, cfgErr, cfgPath string) *Server {
	return &Server{st: st, client: itunes.New(), cfgPath: cfgPath, cfg: cfg, cfgErr: cfgErr}
}

// Handler returns the HTTP handler: static assets at / and the JSON API.
func (s *Server) Handler() (http.Handler, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("embedding static assets: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/run", s.handleRun)
	return mux, nil
}

func (s *Server) snapshot() (*config.Config, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.cfgErr
}

func (s *Server) handleData(w http.ResponseWriter, _ *http.Request) {
	cfg, _ := s.snapshot()
	resp := dataResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Keywords:    []keywordData{},
	}
	if cfg == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	kws, err := s.buildKeywords(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Initialized = true
	resp.AppID = cfg.AppID
	resp.Country = cfg.Country
	resp.Keywords = kws
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, cfgErr := s.snapshot()
		resp := configResponse{Country: config.DefaultCountry, SearchLimit: config.DefaultSearchLimit}
		if cfg != nil {
			resp = configResponse{
				Initialized: true, AppID: cfg.AppID, Country: cfg.Country,
				Keywords: cfg.Keywords, SearchLimit: cfg.SearchLimit,
			}
		} else if cfgErr != "" {
			resp.Error = cfgErr
		}
		writeJSON(w, http.StatusOK, resp)

	case http.MethodPost:
		var req configRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, configResponse{Error: "invalid request body"})
			return
		}
		cfg := &config.Config{
			AppID: req.AppID, Country: req.Country,
			Keywords: req.Keywords, SearchLimit: req.SearchLimit,
		}
		cfg.Normalize()
		if err := cfg.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, configResponse{Error: err.Error()})
			return
		}
		if err := cfg.Save(s.cfgPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, configResponse{Error: err.Error()})
			return
		}
		s.mu.Lock()
		s.cfg = cfg
		s.cfgErr = ""
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, configResponse{
			Initialized: true, AppID: cfg.AppID, Country: cfg.Country,
			Keywords: cfg.Keywords, SearchLimit: cfg.SearchLimit,
		})

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _ := s.snapshot()
	if cfg == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not set up yet"})
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	res, err := track.RunOnce(r.Context(), s.st, cfg, s.client, runDelay, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(), "stored": res.Stored, "failures": res.Failures,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// buildKeywords assembles one series per configured keyword, preserving config
// order so the dashboard is stable.
func (s *Server) buildKeywords(cfg *config.Config) ([]keywordData, error) {
	history, err := s.st.History(cfg.AppID, cfg.Country)
	if err != nil {
		return nil, err
	}

	byKeyword := make(map[string][]point, len(cfg.Keywords))
	for _, snap := range history {
		byKeyword[snap.Keyword] = append(byKeyword[snap.Keyword], point{
			TS:       snap.Timestamp.Format(time.RFC3339),
			Position: snap.Position,
			Found:    snap.Found,
		})
	}

	out := make([]keywordData, 0, len(cfg.Keywords))
	for _, kw := range cfg.Keywords {
		series := byKeyword[kw]
		kd := keywordData{Keyword: kw, Series: series}
		if n := len(series); n > 0 {
			kd.Current = &series[n-1]
			if n > 1 {
				kd.Previous = &series[n-2]
			}
		}
		out = append(out, kd)
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
