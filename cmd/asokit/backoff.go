package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/omervaroglu/asokit/internal/itunes"
)

// maxRetries is how many times a rate-limited request is retried before giving
// up. Delays grow exponentially: 2s, 4s, 8s.
const maxRetries = 3

// searchWithBackoff calls the iTunes client, retrying with exponential backoff
// when the API signals rate limiting.
func searchWithBackoff(ctx context.Context, c *itunes.Client, term, country string, limit int) ([]itunes.App, error) {
	backoff := 2 * time.Second
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		results, err := c.Search(ctx, term, country, limit)
		if err == nil {
			return results, nil
		}

		var rl *itunes.RateLimitError
		if !errors.As(err, &rl) {
			return nil, err // Non-throttle errors are not retried.
		}
		lastErr = err

		if attempt == maxRetries {
			break
		}
		fmt.Fprintf(os.Stderr, "  %-30s rate limited, retrying in %s\n", term, backoff)
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
