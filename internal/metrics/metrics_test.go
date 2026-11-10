package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/omervaroglu/asokit/internal/itunes"
)

var refNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// noOwnApp is an id no test fixture uses, so nothing is excluded as "ourselves".
const noOwnApp int64 = -1

func approx(a, b float64) bool { return math.Abs(a-b) < 0.1 }

// Difficulty is tested one sub-score at a time by zeroing the other weights, so
// each assertion pins down exactly one part of the formula.

func TestDifficultyRatingOnly(t *testing.T) {
	// log10(10)=1, log10(100)=2 -> median 1.5 -> /7 -> *100.
	apps := []itunes.App{{RatingCount: 9}, {RatingCount: 99}}
	got := Difficulty(apps, "x", refNow, Weights{Rating: 1})
	want := 100 * (1.5 / ratingLogCap)
	if !approx(got, want) {
		t.Errorf("rating-only difficulty = %.3f, want %.3f", got, want)
	}
}

func TestDifficultyTitleOnly(t *testing.T) {
	apps := []itunes.App{
		{Name: "Bütçe Takip"},   // contains term
		{Name: "İçin Bütçe"},    // İ-lowercasing must not break the match
		{Name: "Para Yönetimi"}, // no
		{Name: "money"},         // no
	}
	got := Difficulty(apps, "bütçe", refNow, Weights{Title: 1})
	if !approx(got, 50) { // 2 of 4 titles contain the term
		t.Errorf("title-only difficulty = %.3f, want 50", got)
	}
}

func TestDifficultyAgeOnly(t *testing.T) {
	apps := []itunes.App{
		{ReleaseDate: "2025-01-01T00:00:00Z"}, // ~1 year
		{ReleaseDate: "2024-01-01"},           // ~2 years (plain date form)
	}
	got := Difficulty(apps, "x", refNow, Weights{Age: 1})
	if !approx(got, 100*(1.5/ageYearCap)) { // median age ~1.5y, cap 5y
		t.Errorf("age-only difficulty = %.3f, want ~30", got)
	}
}

func TestDifficultyEmptyAndClamp(t *testing.T) {
	if got := Difficulty(nil, "x", refNow, DefaultWeights); got != 0 {
		t.Errorf("empty difficulty = %v, want 0", got)
	}
	// A single mega-app with the term in its title and old age saturates every
	// sub-score, so the blended score tops out near 100.
	apps := []itunes.App{{Name: "sleep", RatingCount: 50_000_000, ReleaseDate: "2010-01-01"}}
	got := Difficulty(apps, "sleep", refNow, DefaultWeights)
	if got < 95 || got > 100 {
		t.Errorf("saturated difficulty = %.3f, want ~100", got)
	}
}

func TestDifficultyUsesOnlyTopWindow(t *testing.T) {
	// 10 strongly-rated apps, then many zero-rated ones that must be ignored.
	apps := make([]itunes.App, 0, 30)
	for i := 0; i < resultWindow; i++ {
		apps = append(apps, itunes.App{RatingCount: 9_999_999})
	}
	for i := 0; i < 20; i++ {
		apps = append(apps, itunes.App{RatingCount: 0})
	}
	got := Difficulty(apps, "x", refNow, Weights{Rating: 1})
	if got < 95 { // median of the top 10 is still ~max
		t.Errorf("windowed difficulty = %.3f, want ~100 (tail ignored)", got)
	}
}

func TestRelevance(t *testing.T) {
	crit := RelevanceCriteria{GenreIDs: []string{"6015"}, TitleTerms: []string{"budget"}}
	apps := []itunes.App{
		{Name: "Alpha", GenreIDs: []string{"6015"}},          // genre match
		{Name: "Budget Planner", GenreIDs: []string{"6016"}}, // title match
		{Name: "Some Game", GenreIDs: []string{"6014"}},      // neither
		{Name: "Beta", GenreIDs: []string{"7000", "6015"}},   // genre match (second id)
	}
	if got := Relevance(apps, noOwnApp, crit); got != 3 {
		t.Errorf("Relevance = %v, want 3", got)
	}
}

func TestRelevanceExcludesOwnApp(t *testing.T) {
	crit := RelevanceCriteria{GenreIDs: []string{"6015"}}
	apps := []itunes.App{
		{ID: 42, GenreIDs: []string{"6015"}}, // us — must not be counted
		{ID: 7, GenreIDs: []string{"6015"}},  // competitor, same genre
		{ID: 8, GenreIDs: []string{"6015"}},  // competitor, same genre
	}
	if got := Relevance(apps, 42, crit); got != 2 {
		t.Errorf("Relevance = %v, want 2 (own app excluded)", got)
	}
}

func TestRelevanceCountsAtMostTopWindow(t *testing.T) {
	crit := RelevanceCriteria{GenreIDs: []string{"6015"}}
	apps := make([]itunes.App, 15)
	for i := range apps {
		apps[i] = itunes.App{ID: int64(i + 1), GenreIDs: []string{"6015"}}
	}
	if got := Relevance(apps, noOwnApp, crit); got != resultWindow {
		t.Errorf("Relevance = %v, want %d (capped at top window)", got, resultWindow)
	}
}

func TestNarrow(t *testing.T) {
	cases := []struct {
		count int
		want  bool
	}{
		{199, true}, {200, false}, {50, true}, {1000, false},
	}
	for _, c := range cases {
		if got := Narrow(c.count); got != c.want {
			t.Errorf("Narrow(%d) = %v, want %v", c.count, got, c.want)
		}
	}
}

func TestCompute(t *testing.T) {
	apps := []itunes.App{{Name: "sleep", RatingCount: 100, GenreIDs: []string{"6015"}, ReleaseDate: "2024-01-01"}}
	got := Compute(apps, noOwnApp, "sleep", 180, refNow, DefaultWeights, RelevanceCriteria{GenreIDs: []string{"6015"}})
	if got.Difficulty <= 0 || got.Difficulty > 100 {
		t.Errorf("Difficulty out of range: %v", got.Difficulty)
	}
	if got.Relevance != 1 {
		t.Errorf("Relevance = %v, want 1", got.Relevance)
	}
	if !got.Narrow {
		t.Errorf("Narrow = false, want true (180 < 200)")
	}
}

// foldForMatch is the Unicode-safe matcher the spec calls out explicitly.
func TestFoldForMatch(t *testing.T) {
	cases := []struct{ a, b string }{
		{"İstanbul", "istanbul"}, // Turkish dotted capital İ
		{"Işık", "ışık"},         // dotless/dotted i family
		{"IŞIK", "ışık"},         // all-caps Turkish
		{"Bütçe", "bütçe"},       // Turkish ü
		{"Müll", "müll"},         // German ü
		{"Schloß", "schloß"},     // German ß stays ß
		{"健康", "健康"},             // CJK unchanged
		{"FATURA", "fatura"},     // plain ASCII case
	}
	for _, c := range cases {
		if fa, fb := foldForMatch(c.a), foldForMatch(c.b); fa != fb {
			t.Errorf("foldForMatch(%q)=%q != foldForMatch(%q)=%q", c.a, fa, c.b, fb)
		}
	}
	// An empty term must not match everything.
	if foldForMatch("") != "" {
		t.Error("foldForMatch(\"\") should be empty")
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},      // odd, unsorted
		{[]float64{4, 1, 3, 2}, 2.5}, // even
	}
	for _, c := range cases {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
