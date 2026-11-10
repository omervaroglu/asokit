// Package itunes is a minimal client for Apple's public iTunes Search API.
//
// Note: the Search API's result ordering is a *relative relevance* ordering,
// not a reproduction of the personalized, ad-influenced ranking a user sees in
// the App Store app. asokit treats a result position as a ranking proxy, which
// is good enough to track movement over time but is not an absolute rank.
package itunes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// BaseURL is the iTunes Search endpoint. Exposed as a var so tests can point
// the client at an httptest server.
var BaseURL = "https://itunes.apple.com/search"

// MaxResults is the hard ceiling the Search API enforces on the number of
// results per term. A response at this count is a ceiling, not a true total.
const MaxResults = 200

// App is a single search result, trimmed to the fields asokit needs. Beyond
// identity (ID/Name), these fields feed the competition and relevance metrics;
// the Search API already returns them, so capturing them needs no extra request.
type App struct {
	ID   int64  `json:"trackId"`
	Name string `json:"trackName"`

	// RatingCount is the number of ratings for the current release line
	// (userRatingCount). A proxy for how established a competitor is.
	RatingCount int `json:"userRatingCount"`
	// RatingAvg is the average star rating (averageUserRating), 0–5.
	RatingAvg float64 `json:"averageUserRating"`
	// Genre is the primary App Store category name, e.g. "Finance".
	Genre string `json:"primaryGenreName"`
	// GenreIDs are the numeric category ids as strings, e.g. ["6015","6007"].
	// The API returns these as strings, so keep them as strings.
	GenreIDs []string `json:"genreIds"`
	// ReleaseDate is when the app first shipped (RFC3339 string from the API).
	ReleaseDate string `json:"releaseDate"`
	// CurrentVersionReleaseDate is when the current version shipped.
	CurrentVersionReleaseDate string `json:"currentVersionReleaseDate"`
	// SellerName is the developer/publisher name.
	SellerName string `json:"sellerName"`
}

type searchResponse struct {
	ResultCount int   `json:"resultCount"`
	Results     []App `json:"results"`
}

// Client queries the iTunes Search API.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// New returns a Client with sane defaults.
func New() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		UserAgent: "asokit (+https://github.com/omervaroglu/asokit)",
	}
}

// Search returns the software results for term in the given country storefront,
// in the API's relevance order. limit is clamped to the API maximum of 200.
func (c *Client) Search(ctx context.Context, term, country string, limit int) ([]App, error) {
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}

	q := url.Values{}
	q.Set("term", term)
	q.Set("country", country)
	q.Set("entity", "software")
	q.Set("limit", strconv.Itoa(limit))

	reqURL := BaseURL + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itunes search %q: %w", term, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Surface throttling clearly so callers can back off.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, &RateLimitError{StatusCode: resp.StatusCode, Term: term}
	}
	// 5xx is a transient server-side problem; surface it as a distinct type so
	// callers can retry it the same way they retry throttling.
	if resp.StatusCode >= 500 {
		return nil, &TransientError{StatusCode: resp.StatusCode, Term: term}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itunes search %q: unexpected status %d", term, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("itunes search %q: reading body: %w", term, err)
	}

	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("itunes search %q: decoding response: %w", term, err)
	}
	return sr.Results, nil
}

// RateLimitError indicates the API throttled the request.
type RateLimitError struct {
	StatusCode int
	Term       string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("itunes search %q: rate limited (status %d)", e.Term, e.StatusCode)
}

// TransientError indicates a temporary server-side failure (HTTP 5xx) that is
// worth retrying.
type TransientError struct {
	StatusCode int
	Term       string
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("itunes search %q: server error (status %d)", e.Term, e.StatusCode)
}
