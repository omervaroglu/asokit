// Package web serves the asokit dashboard: a self-contained single-page UI
// (embedded in the binary) that can both read the local store and, when the
// config has not been set up yet, collect it from the browser. It supports
// multiple markets (storefronts) under one app id, each with its own keywords.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
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

// appInfo is the minimal per-app descriptor the dashboard's app switcher needs.
// IconURL is filled once app metadata is captured (populated in a later phase).
type appInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"icon_url,omitempty"`
}

type dataResponse struct {
	Initialized bool            `json:"initialized"`
	AppID       int64           `json:"app_id"`  // the app this response is for
	Apps        []appInfo       `json:"apps"`    // all configured apps (for the switcher)
	Country     string          `json:"country"` // the market this response is for
	Markets     []string        `json:"markets"` // the app's configured storefronts
	GeneratedAt string          `json:"generated_at"`
	Keywords    []keywordData   `json:"keywords"`
	Phrases     []keywordData   `json:"phrases"`
	Releases    []releaseMarker `json:"releases"`
}

type configResponse struct {
	Initialized bool            `json:"initialized"`
	Error       string          `json:"error,omitempty"`
	AppID       int64           `json:"app_id"` // the first/selected app (back-compat)
	SearchLimit int             `json:"search_limit"`
	Markets     []config.Market `json:"markets"` // the first/selected app's markets
	Apps        []config.App    `json:"apps"`    // all configured apps
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

// selectApp picks the app a request targets: the one named by ?app=<id>, or the
// first configured app when the parameter is missing or unknown. cfg must be
// initialized (Validate guarantees at least one app).
func selectApp(cfg *config.Config, q string) config.App {
	if q != "" {
		if id, err := strconv.ParseInt(q, 10, 64); err == nil {
			if a, ok := cfg.App(id); ok {
				return a
			}
		}
	}
	return cfg.Apps[0]
}

// EnsureAppMeta fills in the cached name + icon for any configured app that
// doesn't have them yet, via one lightweight App Store lookup each. It is
// best-effort and meant to run in the background at startup (and after a config
// change), so the switcher shows icons within seconds rather than only after a
// full run. A lookup failure is ignored; the next run will try again.
func (s *Server) EnsureAppMeta(ctx context.Context) {
	cfg, _ := s.snapshot()
	if cfg == nil {
		return
	}
	for _, a := range cfg.Apps {
		if _, ok, err := s.st.AppMeta(a.ID); err == nil && ok {
			continue // already cached
		}
		country := config.DefaultCountry
		if len(a.Markets) > 0 {
			country = a.Markets[0].Country
		}
		if app, err := s.client.Lookup(ctx, a.ID, country); err == nil {
			_ = s.st.UpsertAppMeta(a.ID, app.Name, app.ArtworkURL)
		}
	}
}

// appInfos lists the configured apps for the dashboard's switcher, enriched with
// each app's cached name and icon. A configured name overrides the cached one; a
// missing cache entry just yields no icon (the UI falls back to the id).
func (s *Server) appInfos(cfg *config.Config) []appInfo {
	out := make([]appInfo, 0, len(cfg.Apps))
	for _, a := range cfg.Apps {
		info := appInfo{ID: a.ID, Name: a.Name}
		if meta, ok, err := s.st.AppMeta(a.ID); err == nil && ok {
			if info.Name == "" {
				info.Name = meta.Name
			}
			info.IconURL = meta.IconURL
		}
		out = append(out, info)
	}
	return out
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.snapshot()
	resp := dataResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Keywords:    []keywordData{},
		Phrases:     []keywordData{},
		Apps:        []appInfo{},
		Markets:     []string{},
		Releases:    []releaseMarker{},
	}
	if cfg == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	app := selectApp(cfg, r.URL.Query().Get("app"))
	market, ok := app.Market(r.URL.Query().Get("country"))
	if !ok {
		market = app.Markets[0] // default to the first storefront
	}
	kws, phrases, err := s.buildMarket(app.ID, market)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	releases, err := s.st.Releases(app.ID, market.Country)
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
	resp.AppID = app.ID
	resp.Apps = s.appInfos(cfg)
	resp.Country = market.Country
	resp.Markets = app.Countries()
	resp.Keywords = kws
	resp.Phrases = phrases
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, cfgErr := s.snapshot()
		resp := configResponse{SearchLimit: config.DefaultSearchLimit, Markets: []config.Market{}, Apps: []config.App{}}
		if cfg != nil {
			first := cfg.Apps[0]
			resp = configResponse{
				Initialized: true, AppID: first.ID,
				SearchLimit: cfg.SearchLimit, Markets: first.Markets, Apps: cfg.Apps,
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
		// The setup form edits one app's markets. Merge onto the existing config
		// so other apps and the app's own unedited fields (competitors, relevance,
		// name, per-market phrases) are preserved rather than wiped.
		cfg := &config.Config{SearchLimit: req.SearchLimit}
		var apps []config.App
		if cur, _ := s.snapshot(); cur != nil {
			apps = append(apps, cur.Apps...)
			cfg.StoreTop = cur.StoreTop
			// The setup form doesn't send search_limit; keep the existing value
			// instead of letting Normalize reset it to the default.
			if req.SearchLimit <= 0 {
				cfg.SearchLimit = cur.SearchLimit
			}
		}
		// Target the app with this id; if none matches and there is exactly one
		// app, update it (an id change); otherwise add it as a new app.
		idx := -1
		for i := range apps {
			if apps[i].ID == req.AppID {
				idx = i
				break
			}
		}
		if idx == -1 && len(apps) == 1 {
			idx = 0
		}
		markets := req.Markets
		if idx >= 0 {
			prevPhrases := make(map[string][]string, len(apps[idx].Markets))
			for _, m := range apps[idx].Markets {
				prevPhrases[m.Country] = m.Phrases
			}
			for i, m := range markets {
				if len(m.Phrases) == 0 {
					if p, ok := prevPhrases[config.NormalizeCountry(m.Country)]; ok {
						markets[i].Phrases = p
					}
				}
			}
			apps[idx].ID = req.AppID
			apps[idx].Markets = markets
		} else {
			apps = append(apps, config.App{ID: req.AppID, Markets: markets})
		}
		cfg.Apps = apps
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
		// Fetch the icon/name for any newly added app, in the background.
		go s.EnsureAppMeta(context.Background())
		first := cfg.Apps[0]
		writeJSON(w, http.StatusOK, configResponse{
			Initialized: true, AppID: first.ID,
			SearchLimit: cfg.SearchLimit, Markets: first.Markets, Apps: cfg.Apps,
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

	app := selectApp(cfg, r.URL.Query().Get("app"))
	// Which markets to run: one (?country=xx) or all of the app's (omitted).
	markets := app.Markets
	if c := r.URL.Query().Get("country"); c != "" {
		m, ok := app.Market(c)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown market " + c})
			return
		}
		markets = []config.Market{m}
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	crit := track.ResolveRelevance(r.Context(), s.client, app.ID, app.Markets[0].Country, app.Relevance)

	total := track.Result{At: time.Now().UTC()}
	for _, m := range markets {
		// Version marker (non-fatal, mirrors the CLI run path).
		_ = track.CaptureRelease(r.Context(), s.client, s.st, app.ID, m.Country)
		rc := track.RunConfig{
			AppID:       app.ID,
			Country:     m.Country,
			Keywords:    m.Terms(), // keywords + phrases, tracked together
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

// buildMarket assembles the keyword and phrase sections of a market from a
// single history read, preserving each group's configured order.
func (s *Server) buildMarket(appID int64, m config.Market) (keywords, phrases []keywordData, err error) {
	history, err := s.st.History(appID, m.Country)
	if err != nil {
		return nil, nil, err
	}

	byKeyword := make(map[string][]point)
	latest := make(map[string]store.Snapshot)
	for _, snap := range history {
		byKeyword[snap.Keyword] = append(byKeyword[snap.Keyword], point{
			TS:       snap.Timestamp.Format(time.RFC3339),
			Position: snap.Position,
			Found:    snap.Found,
		})
		latest[snap.Keyword] = snap // history is ts-ascending, so the last wins
	}
	return section(m.Keywords, byKeyword, latest), section(m.Phrases, byKeyword, latest), nil
}

// section builds the per-term data for one group (keywords or phrases).
func section(terms []string, byKeyword map[string][]point, latest map[string]store.Snapshot) []keywordData {
	out := make([]keywordData, 0, len(terms))
	for _, kw := range terms {
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
	return out
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

	app := selectApp(cfg, r.URL.Query().Get("app"))
	market, ok := app.Market(r.URL.Query().Get("country"))
	if !ok {
		market = app.Markets[0]
	}

	snap, found, err := s.st.LatestForKeyword(app.ID, market.Country, keyword)
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

	ids := make([]int64, 0, len(app.Competitors))
	for _, c := range app.Competitors {
		ids = append(ids, c.ID)
	}
	seriesByID, err := s.st.CompetitorSeries(app.ID, market.Country, keyword, ids)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, c := range app.Competitors {
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
