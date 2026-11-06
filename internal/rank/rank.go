// Package rank computes an app's position within a list of search results.
package rank

import "github.com/omervaroglu/asokit/internal/itunes"

// Result is the outcome of looking for one app within one keyword's results.
type Result struct {
	// Position is the 1-based index of the app in the results, or 0 if the
	// app did not appear within the fetched window.
	Position int
	// Found reports whether the app appeared at all.
	Found bool
	// ResultCount is how many results the API returned for the term.
	ResultCount int
}

// Find locates appID within results and reports its 1-based position.
//
// A position of 0 with Found=false means the app was outside the fetched
// window (or absent). Because the fetch is capped (max 200), "not found" is
// "not in the top N", not a guarantee the app is unranked.
func Find(appID int64, results []itunes.App) Result {
	r := Result{ResultCount: len(results)}
	for i, app := range results {
		if app.ID == appID {
			r.Position = i + 1
			r.Found = true
			break
		}
	}
	return r
}
