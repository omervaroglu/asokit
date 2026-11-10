// Package metrics turns a keyword's top search results into two scores —
// difficulty and relevance — plus a "narrow" flag. It is pure: no database, no
// network, no clock of its own (the caller passes the reference time), so the
// formulas can be unit-tested exactly.
//
// # Difficulty (0–100)
//
// How hard it looks to rank for a term, from the top-10 competitive landscape.
// It is a weighted blend of three sub-scores, each normalised to 0–1:
//
//   - rating strength: the median of log10(ratingCount+1) across the top 10,
//     divided by log10(10,000,000) ≈ 7. Many heavily-rated incumbents push this
//     toward 1.
//   - title saturation: the fraction of the top 10 whose title contains the
//     term (case- and Unicode-insensitive). If everyone names the term in their
//     title, it is contested.
//   - maturity: the median age of the top 10, in years, divided by a 5-year cap.
//     Older incumbents are harder to displace.
//
// difficulty = 100 × (Wrating·rating + Wtitle·title + Wage·maturity).
// The weights are fixed defaults (see DefaultWeights) but a caller may override
// them; they should sum to 1 so the result stays within 0–100.
//
// # Relevance (0–10)
//
// How on-topic the term is for *our* app: the count of apps in the top 10 that
// are the "same type" as us. A result counts when its App Store category is one
// of our genre ids, OR its title contains one of a small list of title terms.
// The value is a plain count, so it ranges 0–10 (fewer when the API returned
// fewer than 10 results).
//
// # Narrow
//
// True when the API returned fewer than its 200-result ceiling, marking a
// low-competition term. (200 is a ceiling, not a real total, so it is shown as
// "200+" in the UI.)
package metrics

import (
	"math"
	"strings"
	"time"

	"github.com/omervaroglu/asokit/internal/itunes"
)

// resultWindow is how many top results the difficulty/relevance scores consider.
const resultWindow = 10

// Normalisation caps: a rating count at or above ten million, and an age at or
// above five years, each saturate their sub-score at 1.
const (
	ratingLogCap = 7.0 // log10(10_000_000)
	ageYearCap   = 5.0
)

// Weights blends the three difficulty sub-scores. They should sum to 1.
type Weights struct {
	Rating float64
	Title  float64
	Age    float64
}

// DefaultWeights favours rating strength (the most reliable signal), then title
// saturation, then maturity. Documented here so the choice is reviewable.
var DefaultWeights = Weights{Rating: 0.5, Title: 0.3, Age: 0.2}

// RelevanceCriteria defines what makes a result "the same type" as our app.
// GenreIDs are numeric App Store category ids (as strings, matching the API);
// TitleTerms are words whose presence in a result's title also counts.
type RelevanceCriteria struct {
	GenreIDs   []string
	TitleTerms []string
}

// Scores is the full metric set for one keyword snapshot.
type Scores struct {
	Difficulty float64
	Relevance  float64
	Narrow     bool
}

// Compute returns all three metrics for a keyword. results is the search result
// list in rank order; term is the searched keyword; resultCount is the API's
// reported total (for Narrow); now is the reference time for age; w are the
// difficulty weights (use DefaultWeights for the standard blend).
func Compute(results []itunes.App, term string, resultCount int, now time.Time, w Weights, crit RelevanceCriteria) Scores {
	return Scores{
		Difficulty: Difficulty(results, term, now, w),
		Relevance:  Relevance(results, crit),
		Narrow:     Narrow(resultCount),
	}
}

// Difficulty scores how contested a term is (0–100). See the package doc.
func Difficulty(results []itunes.App, term string, now time.Time, w Weights) float64 {
	top := results
	if len(top) > resultWindow {
		top = top[:resultWindow]
	}
	if len(top) == 0 {
		return 0
	}

	// Rating strength: median of log10(ratingCount+1), normalised by the cap.
	logs := make([]float64, 0, len(top))
	for _, a := range top {
		logs = append(logs, math.Log10(float64(a.RatingCount)+1))
	}
	rating := clamp01(median(logs) / ratingLogCap)

	// Title saturation: fraction of titles containing the term.
	foldedTerm := foldForMatch(term)
	hits := 0
	for _, a := range top {
		if foldedTerm != "" && strings.Contains(foldForMatch(a.Name), foldedTerm) {
			hits++
		}
	}
	title := float64(hits) / float64(len(top))

	// Maturity: median age (years) of results with a parseable release date.
	ages := make([]float64, 0, len(top))
	for _, a := range top {
		if t, ok := parseDate(a.ReleaseDate); ok {
			years := now.Sub(t).Hours() / (24 * 365.25)
			if years < 0 {
				years = 0
			}
			ages = append(ages, years)
		}
	}
	maturity := 0.0
	if len(ages) > 0 {
		maturity = clamp01(median(ages) / ageYearCap)
	}

	return 100 * (w.Rating*rating + w.Title*title + w.Age*maturity)
}

// Relevance counts how many of the top results are the same type as our app
// (0–10). See the package doc.
func Relevance(results []itunes.App, crit RelevanceCriteria) float64 {
	top := results
	if len(top) > resultWindow {
		top = top[:resultWindow]
	}

	genreSet := make(map[string]bool, len(crit.GenreIDs))
	for _, g := range crit.GenreIDs {
		if g = strings.TrimSpace(g); g != "" {
			genreSet[g] = true
		}
	}
	foldedTerms := make([]string, 0, len(crit.TitleTerms))
	for _, t := range crit.TitleTerms {
		if f := foldForMatch(t); f != "" {
			foldedTerms = append(foldedTerms, f)
		}
	}

	count := 0
	for _, a := range top {
		if sameType(a, genreSet, foldedTerms) {
			count++
		}
	}
	return float64(count)
}

// Narrow reports whether the term is low-competition: the API returned fewer
// results than its 200 ceiling.
func Narrow(resultCount int) bool { return resultCount < itunes.MaxResults }

func sameType(a itunes.App, genreSet map[string]bool, foldedTerms []string) bool {
	for _, g := range a.GenreIDs {
		if genreSet[g] {
			return true
		}
	}
	if len(foldedTerms) > 0 {
		name := foldForMatch(a.Name)
		for _, t := range foldedTerms {
			if strings.Contains(name, t) {
				return true
			}
		}
	}
	return false
}

// foldForMatch normalises a string for case- and Unicode-insensitive substring
// matching. Beyond Unicode lowercasing (which already handles German ü/ß and
// leaves CJK untouched), it folds the Turkish dotted/dotless i family together:
// İ, I, ı and i all become "i". İ lowercases to "i" + a combining dot above
// (U+0307), which this strips, and dotless ı is mapped to i — so a term and a
// title written with any of these still match.
func foldForMatch(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '̇': // combining dot above left over from lowercasing İ
			continue
		case 'ı': // dotless i → i
			r = 'i'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseDate accepts the RFC3339 timestamps the iTunes API uses, and the plain
// YYYY-MM-DD some fields carry.
func parseDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// median returns the median of xs; it mutates a copy, not the input. Empty → 0.
func median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	s := make([]float64, n)
	copy(s, xs)
	// Small slices (≤ top 10), so an insertion sort keeps it dependency-free.
	for i := 1; i < n; i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
