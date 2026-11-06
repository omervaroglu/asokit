// Package web serves the asokit dashboard: a small, self-contained single-page
// UI (embedded in the binary) backed by a JSON endpoint over the local store.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/store"
)

//go:embed static
var staticFS embed.FS

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

// payload is the full response for the dashboard.
type payload struct {
	AppID       int64         `json:"app_id"`
	Country     string        `json:"country"`
	GeneratedAt string        `json:"generated_at"`
	Keywords    []keywordData `json:"keywords"`
}

// Handler returns the dashboard's HTTP handler: static assets at / and the
// data endpoint at /api/data.
func Handler(st *store.Store, cfg *config.Config) (http.Handler, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("embedding static assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		data, err := buildPayload(st, cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(data)
	})
	return mux, nil
}

// buildPayload reads the store and assembles one series per configured keyword,
// preserving the config's keyword order so the dashboard is stable.
func buildPayload(st *store.Store, cfg *config.Config) (payload, error) {
	history, err := st.History(cfg.AppID, cfg.Country)
	if err != nil {
		return payload{}, err
	}

	byKeyword := make(map[string][]point, len(cfg.Keywords))
	for _, s := range history {
		byKeyword[s.Keyword] = append(byKeyword[s.Keyword], point{
			TS:       s.Timestamp.Format(time.RFC3339),
			Position: s.Position,
			Found:    s.Found,
		})
	}

	out := payload{
		AppID:       cfg.AppID,
		Country:     cfg.Country,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Keywords:    make([]keywordData, 0, len(cfg.Keywords)),
	}
	for _, kw := range cfg.Keywords {
		series := byKeyword[kw]
		kd := keywordData{Keyword: kw, Series: series}
		if n := len(series); n > 0 {
			kd.Current = &series[n-1]
			if n > 1 {
				kd.Previous = &series[n-2]
			}
		}
		out.Keywords = append(out.Keywords, kd)
	}
	return out, nil
}
