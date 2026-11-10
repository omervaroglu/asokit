package main

import (
	"testing"

	"github.com/omervaroglu/asokit/internal/config"
)

func TestResultsLabel(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{199, "199"}, {200, "200+"}, {250, "200+"}, {0, "0"},
	}
	for _, c := range cases {
		if got := resultsLabel(c.count); got != c.want {
			t.Errorf("resultsLabel(%d) = %q, want %q", c.count, got, c.want)
		}
	}
}

func TestMetricLabelsNil(t *testing.T) {
	if got := difficultyLabel(nil); got != "—" {
		t.Errorf("difficultyLabel(nil) = %q, want —", got)
	}
	if got := relevanceLabel(nil); got != "—" {
		t.Errorf("relevanceLabel(nil) = %q, want —", got)
	}
	d := 72.4
	if got := difficultyLabel(&d); got != "72" {
		t.Errorf("difficultyLabel(72.4) = %q, want 72", got)
	}
}

func TestCompetitorHeaderAndRank(t *testing.T) {
	if got := competitorHeader(config.Competitor{ID: 42}); got != "42" {
		t.Errorf("unnamed competitor header = %q, want 42", got)
	}
	if got := competitorHeader(config.Competitor{ID: 42, Name: "Rival"}); got != "Rival" {
		t.Errorf("named competitor header = %q, want Rival", got)
	}
	positions := map[int64]int{42: 3}
	if got := competitorRank(positions, 42); got != "#3" {
		t.Errorf("competitorRank present = %q, want #3", got)
	}
	if got := competitorRank(positions, 99); got != "—" {
		t.Errorf("competitorRank absent = %q, want —", got)
	}
}

func TestCSVNullHelpers(t *testing.T) {
	if got := csvFloat(nil); got != "" {
		t.Errorf("csvFloat(nil) = %q, want empty", got)
	}
	if got := csvBool(nil); got != "" {
		t.Errorf("csvBool(nil) = %q, want empty", got)
	}
	v := 1.5
	b := true
	if got := csvFloat(&v); got != "1.50" {
		t.Errorf("csvFloat(1.5) = %q, want 1.50", got)
	}
	if got := csvBool(&b); got != "true" {
		t.Errorf("csvBool(true) = %q, want true", got)
	}
}
