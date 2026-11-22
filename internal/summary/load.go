package summary

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/store"
)

// Source is the part of the store the summary reads.
type Source interface {
	History(appID int64, country string) ([]store.Snapshot, error)
	Releases(appID int64, country string) ([]store.Release, error)
}

// Load reads every market's history and releases for an app.
func Load(src Source, app config.App) ([]Market, error) {
	out := make([]Market, 0, len(app.Markets))
	for _, m := range app.Markets {
		history, err := src.History(app.ID, m.Country)
		if err != nil {
			return nil, err
		}
		releases, err := src.Releases(app.ID, m.Country)
		if err != nil {
			return nil, err
		}
		out = append(out, Market{Country: m.Country, Terms: m.Terms(), History: history, Releases: releases})
	}
	return out, nil
}

// ParseBase builds a Base from user input: a date (YYYY-MM-DD), a release
// version, or neither for the previous run.
func ParseBase(since, release string) (Base, error) {
	switch {
	case since != "" && release != "":
		return Base{}, errors.New("use either a date or a release, not both")
	case since != "":
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			return Base{}, fmt.Errorf("invalid date %q, want YYYY-MM-DD", since)
		}
		return Base{Kind: BaseDate, Date: t}, nil
	case release != "":
		return Base{Kind: BaseRelease, Version: release}, nil
	default:
		return Base{Kind: BasePrevious}, nil
	}
}

// Versions lists the app versions seen in any market, newest first.
func Versions(markets []Market) []string {
	first := map[string]time.Time{}
	for _, m := range markets {
		for _, r := range m.Releases {
			if t, ok := first[r.Version]; !ok || r.ReleasedAt.Before(t) {
				first[r.Version] = r.ReleasedAt
			}
		}
	}
	out := make([]string, 0, len(first))
	for v := range first {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return first[out[i]].After(first[out[j]]) })
	return out
}
