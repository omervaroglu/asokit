// Package track contains the core "measure every keyword once" routine shared
// by the CLI (`asokit run`) and the web dashboard's "Run now" action.
package track

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/itunes"
	"github.com/omervaroglu/asokit/internal/rank"
	"github.com/omervaroglu/asokit/internal/store"
)

// maxRetries is how many times a rate-limited request is retried before giving
// up. Delays grow exponentially: 2s, 4s, 8s.
const maxRetries = 3

// Result summarises one run.
type Result struct {
	Stored   int       `json:"stored"`
	Failures int       `json:"failures"`
	At       time.Time `json:"at"`
}

// RunOnce fetches and stores the current position for every keyword in cfg,
// recording all snapshots under a single timestamp. progress, if non-nil, is
// called after each keyword (with err set when that keyword failed).
func RunOnce(
	ctx context.Context,
	st *store.Store,
	cfg *config.Config,
	client *itunes.Client,
	delay time.Duration,
	progress func(keyword string, res rank.Result, err error),
) (Result, error) {
	now := time.Now().UTC()
	out := Result{At: now}

	for i, keyword := range cfg.Keywords {
		if i > 0 {
			if err := sleep(ctx, delay); err != nil {
				return out, err
			}
		}

		results, err := Search(ctx, client, keyword, cfg.Country, cfg.SearchLimit)
		if err != nil {
			out.Failures++
			if progress != nil {
				progress(keyword, rank.Result{}, err)
			}
			continue
		}

		res := rank.Find(cfg.AppID, results)
		if err := st.Insert(store.Snapshot{
			Timestamp:   now,
			AppID:       cfg.AppID,
			Country:     cfg.Country,
			Keyword:     keyword,
			Position:    res.Position,
			Found:       res.Found,
			ResultCount: res.ResultCount,
		}); err != nil {
			return out, err
		}
		out.Stored++
		if progress != nil {
			progress(keyword, res, nil)
		}
	}
	return out, nil
}

// Search calls the iTunes client, retrying with exponential backoff when the
// API signals rate limiting. Non-throttle errors are returned immediately.
func Search(ctx context.Context, c *itunes.Client, term, country string, limit int) ([]itunes.App, error) {
	backoff := 2 * time.Second
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		results, err := c.Search(ctx, term, country, limit)
		if err == nil {
			return results, nil
		}

		var rl *itunes.RateLimitError
		if !errors.As(err, &rl) {
			return nil, err
		}
		lastErr = err
		if attempt == maxRetries {
			break
		}
		if err := sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
	}
	return nil, fmt.Errorf("giving up after %d retries: %w", maxRetries, lastErr)
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
