// Package track contains the core "measure every keyword once" routine shared
// by the CLI (`asokit run`) and the web dashboard's "Run now" action.
package track

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/itunes"
	"github.com/omervaroglu/asokit/internal/metrics"
	"github.com/omervaroglu/asokit/internal/rank"
	"github.com/omervaroglu/asokit/internal/store"
)

// maxRetries is how many times a transient failure is retried before giving up.
const maxRetries = 3

// baseBackoff is the first retry delay; it doubles each attempt (2s, 4s, 8s).
// It is a var, not a const, so tests can shrink it and not sleep for seconds.
var baseBackoff = 2 * time.Second

// Result summarises one run.
type Result struct {
	Stored   int       `json:"stored"`
	Failures int       `json:"failures"`
	At       time.Time `json:"at"`
}

// RunConfig bundles everything one market's run needs. It keeps RunOnce's
// signature small as the feature set grew (search limit, stored results,
// metric weights and relevance criteria).
type RunConfig struct {
	AppID       int64
	Country     string
	Keywords    []string
	SearchLimit int
	StoreTop    int                       // how many top results to persist
	Weights     metrics.Weights           // difficulty weights
	Relevance   metrics.RelevanceCriteria // resolved once by the caller
	Client      *itunes.Client
	Delay       time.Duration
}

// RunOnce fetches the current results for every keyword of one market, computes
// the competition/relevance metrics, and stores a snapshot plus its top-N
// results under a single timestamp. progress, if non-nil, is called after each
// keyword (err set when it failed).
func RunOnce(
	ctx context.Context,
	st *store.Store,
	rc RunConfig,
	progress func(keyword string, res rank.Result, err error),
) (Result, error) {
	now := time.Now().UTC()
	out := Result{At: now}

	for i, keyword := range rc.Keywords {
		if i > 0 {
			if err := sleep(ctx, rc.Delay); err != nil {
				return out, err
			}
		}

		results, err := Search(ctx, rc.Client, keyword, rc.Country, rc.SearchLimit)
		if err != nil {
			out.Failures++
			if progress != nil {
				progress(keyword, rank.Result{}, err)
			}
			continue
		}

		res := rank.Find(rc.AppID, results)

		// Metrics are computed from the full fetched window; only storage is
		// truncated to StoreTop, so a small StoreTop never degrades the scores.
		scores := metrics.Compute(results, rc.AppID, keyword, res.ResultCount, now, rc.Weights, rc.Relevance)
		diff, rel, narrow := scores.Difficulty, scores.Relevance, scores.Narrow

		snap := store.Snapshot{
			Timestamp:   now,
			AppID:       rc.AppID,
			Country:     rc.Country,
			Keyword:     keyword,
			Position:    res.Position,
			Found:       res.Found,
			ResultCount: res.ResultCount,
			Difficulty:  &diff,
			Relevance:   &rel,
			Narrow:      &narrow,
		}
		if _, err := st.InsertSnapshot(snap, topResults(results, rc.StoreTop)); err != nil {
			return out, err
		}
		out.Stored++
		if progress != nil {
			progress(keyword, res, nil)
		}
	}
	return out, nil
}

// topResults converts the first n search results into stored-result rows,
// numbering positions from 1.
func topResults(apps []itunes.App, n int) []store.Result {
	if n > len(apps) {
		n = len(apps)
	}
	out := make([]store.Result, 0, n)
	for i := 0; i < n; i++ {
		a := apps[i]
		out = append(out, store.Result{
			Position:    i + 1,
			TrackID:     a.ID,
			Name:        a.Name,
			RatingCount: a.RatingCount,
			RatingAvg:   a.RatingAvg,
			Genre:       a.Genre,
			ReleaseDate: a.ReleaseDate,
		})
	}
	return out
}

// CaptureRelease looks up the app's current version for a storefront and
// records it (deduplicated), and refreshes the app's cached display metadata
// (name + icon) from the same lookup. It is non-fatal by design: callers log the
// error and continue, since a missing version marker must never fail a run.
func CaptureRelease(ctx context.Context, client *itunes.Client, st *store.Store, appID int64, country string) error {
	app, err := client.Lookup(ctx, appID, country)
	if err != nil {
		return err
	}
	// Cache the app's name + icon for the dashboard switcher (best-effort: a
	// metadata write must never abort tracking).
	_ = st.UpsertAppMeta(appID, app.Name, app.ArtworkURL)
	if app.Version == "" || app.CurrentVersionReleaseDate == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, app.CurrentVersionReleaseDate)
	if err != nil {
		if t, err = time.Parse("2006-01-02", app.CurrentVersionReleaseDate); err != nil {
			return nil // unparseable date — skip quietly rather than error
		}
	}
	return st.UpsertRelease(appID, country, app.Version, t)
}

// ResolveRelevance builds the relevance criteria for a run. When the config
// supplies no genre ids it resolves the tracked app's own category via a single
// Lookup. A lookup failure is non-fatal — relevance degrades to whatever title
// terms were configured rather than failing the run.
func ResolveRelevance(ctx context.Context, client *itunes.Client, appID int64, country string, rel config.Relevance) metrics.RelevanceCriteria {
	crit := metrics.RelevanceCriteria{GenreIDs: rel.GenreIDs, TitleTerms: rel.TitleTerms}
	if len(crit.GenreIDs) == 0 {
		if app, err := client.Lookup(ctx, appID, country); err == nil {
			crit.GenreIDs = app.GenreIDs
		}
	}
	return crit
}

// Search calls the iTunes client, retrying with exponential backoff on
// transient failures: rate limiting (429/403), server errors (5xx) and
// temporary network problems (timeouts, "context deadline exceeded" from the
// client's own request timeout). Other errors are returned immediately.
func Search(ctx context.Context, c *itunes.Client, term, country string, limit int) ([]itunes.App, error) {
	backoff := baseBackoff
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		results, err := c.Search(ctx, term, country, limit)
		if err == nil {
			return results, nil
		}

		if !retryable(err) {
			return nil, err
		}
		lastErr = err
		if attempt == maxRetries {
			break
		}
		// sleep returns early if the caller cancelled, so a cancelled parent
		// context stops the loop instead of being treated as a transient error.
		if err := sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
	}
	return nil, fmt.Errorf("giving up after %d retries: %w", maxRetries, lastErr)
}

// retryable reports whether err is a transient failure worth retrying. A
// caller-cancelled context surfaces as context.Canceled, which is not
// retryable; a per-request timeout surfaces as a net.Error timeout or
// context.DeadlineExceeded, which is.
func retryable(err error) bool {
	var rl *itunes.RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	var te *itunes.TransientError
	if errors.As(err, &te) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

// sleep waits for d or returns early if the context is cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
