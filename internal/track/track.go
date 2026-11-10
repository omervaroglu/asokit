// Package track contains the core "measure every keyword once" routine shared
// by the CLI (`asokit run`) and the web dashboard's "Run now" action.
package track

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/omervaroglu/asokit/internal/itunes"
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

// RunOnce fetches and stores the current position for every keyword of one
// market (appID + country), recording all snapshots under a single timestamp.
// progress, if non-nil, is called after each keyword (err set when it failed).
func RunOnce(
	ctx context.Context,
	st *store.Store,
	appID int64,
	country string,
	keywords []string,
	searchLimit int,
	client *itunes.Client,
	delay time.Duration,
	progress func(keyword string, res rank.Result, err error),
) (Result, error) {
	now := time.Now().UTC()
	out := Result{At: now}

	for i, keyword := range keywords {
		if i > 0 {
			if err := sleep(ctx, delay); err != nil {
				return out, err
			}
		}

		results, err := Search(ctx, client, keyword, country, searchLimit)
		if err != nil {
			out.Failures++
			if progress != nil {
				progress(keyword, rank.Result{}, err)
			}
			continue
		}

		res := rank.Find(appID, results)
		if err := st.Insert(store.Snapshot{
			Timestamp:   now,
			AppID:       appID,
			Country:     country,
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
