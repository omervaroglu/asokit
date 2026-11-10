// Package web serves the asokit dashboard: a self-contained single-page UI
// (embedded in the binary) that can both read the local store and, when the
// config has not been set up yet, collect it from the browser. It supports
// multiple markets (storefronts) under one app id, each with its own keywords.
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
	"github.com/omervaroglu/asokit/internal/metrics"
	"github.com/omervaroglu/asokit/internal/store"
	"github.com/omervaroglu/asokit/internal/track"
)

//go:embed static
var staticFS embed.FS

// runDelay is the pause between keyword requests during a browser-triggered run.
const runDelay = 1500 * time.Millisecond

type point struct {
	TS       string `json:"ts"`
	Position int    `json:"position"`
	Found    bool   `json:"found"`
}

type keywordData struct {
	Keyword  string  `json:"keyword"`
	Current  *point  `json:"current"`
	Previous *point  `json:"previous"`
	Series   []point `json:"series"`

	// Latest-snapshot metrics for the table columns (nil when not computed).
	Difficulty  *float64 `json:"difficulty"`
	Relevance   *float64 `json:"relevance"`
	Narrow      *bool    `json:"narrow"`
	ResultCount int      `json:"result_count"`
}

// resultRow is one stored competitor row in a keyword's detail view.
type resultRow struct {
	Position    int     `json:"position"`
	TrackID     int64   `json:"track_id"`
	Name        string  `json:"name"`
	RatingCount int     `json:"rating_count"`
	RatingAvg   float64 `json:"rating_avg"`
	Genre       string  `json:"genre"`
}

// competitorSeries is one tracked competitor's rank-over-time for a keyword.
type competitorSeries struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Series []point `json:"series"`
}

// keywordDetail is the on-demand payload behind a keyword card: the latest
// snapshot's top results and each competitor's series for that keyword.
type keywordDetail struct {
	Keyword     string             `json:"keyword"`
	Top         []resultRow        `json:"top"`
	Competitors []competitorSeries `json:"competitors"`
}

type releaseMarker struct {
	Version    string `json:"version"`
	ReleasedAt string `json:"released_at"`
}

type dataResponse struct {
	Initialized bool            `json:"initialized"`
	AppID       int64           `json:"app_id"`
	Country     string          `json:"country"` // the market this response is for
	Markets     []string        `json:"markets"` // all configured storefronts
	GeneratedAt string          `json:"generated_at"`
	Keywords    []keywordData   `json:"keywords"`
	Releases    []releaseMarker `json:"releases"`
}

type configResponse struct {
	Initialized bool            `json:"initialized"`
	Error       string          `json:"error,omitempty"`
	AppID       int64           `json:"app_id"`
	SearchLimit int             `json:"search_limit"`
	Markets     []config.Market `json:"markets"`
}

type configRequest struct {
	AppID       int64           `json:"app_id"`
	SearchLimit int             `json:"search_limit"`
	Markets     []config.Market `json:"markets"`
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
	mux.HandleFunc("/api/keyword", s.handleKeyword)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/run", s.handleRun)
	return mux, nil
}

func (s *Server) snapshot() (*config.Config, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.cfgErr
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.snapshot()
	resp := dataResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Keywords:    []keywordData{},
		Markets:     []string{},
		Releases:    []releaseMarker{},
	}
	if cfg == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	market, ok := cfg.Market(r.URL.Query().Get("country"))
	if !ok {
		market = cfg.Markets[0] // default to the first storefront
	}
	kws, err := s.buildKeywords(cfg.AppID, market)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	releases, err := s.st.Releases(cfg.AppID, market.Country)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, rel := range releases {
		resp.Releases = append(resp.Releases, releaseMarker{
			Version: rel.Version, ReleasedAt: rel.ReleasedAt.Format(time.RFC3339),
		})
	}

	resp.Initialized = true
	resp.AppID = cfg.AppID
	resp.Country = market.Country
	resp.Markets = cfg.Countries()
	resp.Keywords = kws
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, cfgErr := s.snapshot()
		resp := configResponse{SearchLimit: config.DefaultSearchLimit, Markets: []config.Market{}}
		if cfg != nil {
			resp = configResponse{
				Initialized: true, AppID: cfg.AppID,
				SearchLimit: cfg.SearchLimit, Markets: cfg.Markets,
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
		// Merge onto the existing config so fields the setup form does not send
		// (store_top, relevance, competitors) are preserved rather than wiped.
		cfg := &config.Config{}
		if cur, _ := s.snapshot(); cur != nil {
			clone := *cur
			cfg = &clone
		}
		cfg.AppID = req.AppID
		cfg.SearchLimit = req.SearchLimit
		cfg.Markets = req.Markets
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
			Initialized: true, AppID: cfg.AppID,
			SearchLimit: cfg.SearchLimit, Markets: cfg.Markets,
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

	// Which markets to run: one (?country=xx) or all (omitted).
	markets := cfg.Markets
	if c := r.URL.Query().Get("country"); c != "" {
		m, ok := cfg.Market(c)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown market " + c})
			return
		}
		markets = []config.Market{m}
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	crit := track.ResolveRelevance(r.Context(), s.client, cfg.AppID, cfg.Markets[0].Country, cfg.Relevance)

	total := track.Result{At: time.Now().UTC()}
	for _, m := range markets {
		// Version marker (non-fatal, mirrors the CLI run path).
		_ = track.CaptureRelease(r.Context(), s.client, s.st, cfg.AppID, m.Country)
		rc := track.RunConfig{
			AppID:       cfg.AppID,
			Country:     m.Country,
			Keywords:    m.Keywords,
			SearchLimit: cfg.SearchLimit,
			StoreTop:    cfg.StoreTop,
			Weights:     metrics.DefaultWeights,
			Relevance:   crit,
			Client:      s.client,
			Delay:       runDelay,
		}
		res, err := track.RunOnce(r.Context(), s.st, rc, nil)
		total.Stored += res.Stored
		total.Failures += res.Failures
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": err.Error(), "stored": total.Stored, "failures": total.Failures,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, total)
}

// buildKeywords assembles one series per keyword of a market, preserving the
// market's keyword order so the dashboard is stable.
func (s *Server) buildKeywords(appID int64, m config.Market) ([]keywordData, error) {
	history, err := s.st.History(appID, m.Country)
	if err != nil {
		return nil, err
	}

	byKeyword := make(map[string][]point, len(m.Keywords))
	latest := make(map[string]store.Snapshot, len(m.Keywords))
	for _, snap := range history {
		byKeyword[snap.Keyword] = append(byKeyword[snap.Keyword], point{
			TS:       snap.Timestamp.Format(time.RFC3339),
			Position: snap.Position,
			Found:    snap.Found,
		})
		latest[snap.Keyword] = snap // history is ts-ascending, so the last wins
	}

	out := make([]keywordData, 0, len(m.Keywords))
	for _, kw := range m.Keywords {
		series := byKeyword[kw]
		kd := keywordData{Keyword: kw, Series: series}
		if n := len(series); n > 0 {
			kd.Current = &series[n-1]
			if n > 1 {
				kd.Previous = &series[n-2]
			}
		}
		if snap, ok := latest[kw]; ok {
			kd.Difficulty = snap.Difficulty
			kd.Relevance = snap.Relevance
			kd.Narrow = snap.Narrow
			kd.ResultCount = snap.ResultCount
		}
		out = append(out, kd)
	}
	return out, nil
}

// handleKeyword serves the on-demand detail for one keyword: the latest
// snapshot's top results and each competitor's rank-over-time series.
func (s *Server) handleKeyword(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.snapshot()
	keyword := r.URL.Query().Get("keyword")
	resp := keywordDetail{Keyword: keyword, Top: []resultRow{}, Competitors: []competitorSeries{}}
	if cfg == nil || keyword == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	market, ok := cfg.Market(r.URL.Query().Get("country"))
	if !ok {
		market = cfg.Markets[0]
	}

	snap, found, err := s.st.LatestForKeyword(cfg.AppID, market.Country, keyword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	results, err := s.st.Results(snap.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	const topN = 10
	for i, res := range results {
		if i >= topN {
			break
		}
		resp.Top = append(resp.Top, resultRow{
			Position: res.Position, TrackID: res.TrackID, Name: res.Name,
			RatingCount: res.RatingCount, RatingAvg: res.RatingAvg, Genre: res.Genre,
		})
	}

	ids := make([]int64, 0, len(cfg.Competitors))
	for _, c := range cfg.Competitors {
		ids = append(ids, c.ID)
	}
	seriesByID, err := s.st.CompetitorSeries(cfg.AppID, market.Country, keyword, ids)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, c := range cfg.Competitors {
		cs := competitorSeries{ID: c.ID, Name: c.Name, Series: []point{}}
		for _, pp := range seriesByID[c.ID] {
			cs.Series = append(cs.Series, point{
				TS: pp.Timestamp.Format(time.RFC3339), Position: pp.Position, Found: true,
			})
		}
		resp.Competitors = append(resp.Competitors, cs)
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
