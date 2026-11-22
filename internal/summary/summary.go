// Package summary builds a cross-market run report for one app: what moved
// between a base run and the latest run in every storefront, plus rule-based
// suggestions (opportunities, keywords worth dropping, markets to check).
//
// It is pure: callers pass in the stored snapshots and releases, so the same
// logic serves the CLI and the dashboard and is easy to test.
package summary

import (
	"fmt"
	"sort"
	"time"

	"github.com/omervaroglu/asokit/internal/store"
)

// Options holds the thresholds. Zero values are replaced by DefaultOptions.
type Options struct {
	// Noise: position changes of at most this many places are ignored.
	Noise int `json:"noise"`
	// TopTier: inside the top TopTier (before or after), every move counts,
	// since one place matters more there than five places at #120.
	TopTier int `json:"top_tier"`
	// Opportunity: a ranked keyword in [OppMinRank, OppMaxRank] with difficulty
	// at most OppMaxDifficulty and relevance at least OppMinRelevance.
	OppMinRank       int     `json:"opp_min_rank"`
	OppMaxRank       int     `json:"opp_max_rank"`
	OppMaxDifficulty float64 `json:"opp_max_difficulty"`
	OppMinRelevance  float64 `json:"opp_min_relevance"`
	// DeadRuns: a keyword missing from the top results in each of its last
	// DeadRuns measurements is suggested for removal.
	DeadRuns int `json:"dead_runs"`
	// A market is flagged when at least AlertCount keywords dropped by more than
	// AlertDrop places (or fell out of the top results).
	AlertDrop  int `json:"alert_drop"`
	AlertCount int `json:"alert_count"`
}

// DefaultOptions are the thresholds used when an Options field is zero.
var DefaultOptions = Options{
	Noise:            5,
	TopTier:          10,
	OppMinRank:       11,
	OppMaxRank:       40,
	OppMaxDifficulty: 45,
	OppMinRelevance:  6,
	DeadRuns:         3,
	AlertDrop:        15,
	AlertCount:       3,
}

func (o Options) withDefaults() Options {
	d := DefaultOptions
	if o.Noise > 0 {
		d.Noise = o.Noise
	}
	if o.TopTier > 0 {
		d.TopTier = o.TopTier
	}
	if o.OppMinRank > 0 {
		d.OppMinRank = o.OppMinRank
	}
	if o.OppMaxRank > 0 {
		d.OppMaxRank = o.OppMaxRank
	}
	if o.OppMaxDifficulty > 0 {
		d.OppMaxDifficulty = o.OppMaxDifficulty
	}
	if o.OppMinRelevance > 0 {
		d.OppMinRelevance = o.OppMinRelevance
	}
	if o.DeadRuns > 0 {
		d.DeadRuns = o.DeadRuns
	}
	if o.AlertDrop > 0 {
		d.AlertDrop = o.AlertDrop
	}
	if o.AlertCount > 0 {
		d.AlertCount = o.AlertCount
	}
	return d
}

// BaseKind selects which run each market's latest run is compared with.
type BaseKind string

const (
	// BasePrevious compares with the run before the latest one.
	BasePrevious BaseKind = "previous"
	// BaseDate compares with the run nearest a date.
	BaseDate BaseKind = "date"
	// BaseRelease compares with the last run before a version was released in
	// that storefront.
	BaseRelease BaseKind = "release"
)

// Base is the comparison point.
type Base struct {
	Kind    BaseKind  `json:"kind"`
	Date    time.Time `json:"date,omitzero"`     // for BaseDate
	Version string    `json:"version,omitempty"` // for BaseRelease
}

// Market is one storefront's input.
type Market struct {
	Country  string
	Terms    []string         // configured keywords and phrases
	History  []store.Snapshot // every stored snapshot for this market
	Releases []store.Release
}

// Move is one keyword's change between the base and latest runs.
type Move struct {
	Country    string   `json:"country"`
	Keyword    string   `json:"keyword"`
	Was        int      `json:"was"`   // 0 when not in the top results
	Now        int      `json:"now"`   // 0 when not in the top results
	Delta      int      `json:"delta"` // positive = improved
	Difficulty *float64 `json:"difficulty,omitempty"`
	Relevance  *float64 `json:"relevance,omitempty"`
}

// Dead is a keyword that has not ranked in any of its last Runs measurements.
type Dead struct {
	Country string `json:"country"`
	Keyword string `json:"keyword"`
	Runs    int    `json:"runs"`
}

// Alert flags a market where many keywords dropped at once.
type Alert struct {
	Country  string   `json:"country"`
	Keywords []string `json:"keywords"`
}

// MarketInfo describes what was compared in one market.
type MarketInfo struct {
	Country  string    `json:"country"`
	LatestAt time.Time `json:"latest_at"`
	BaseAt   time.Time `json:"base_at,omitzero"`
	HasBase  bool      `json:"has_base"`
	// Note explains a missing base (e.g. the version was never seen there).
	Note string `json:"note,omitempty"`
	// Unmeasured lists configured keywords that the base run measured but the
	// latest run did not — usually failed requests.
	Unmeasured []string `json:"unmeasured,omitempty"`
	Keywords   int      `json:"keywords"` // measured in the latest run
	Ranked     int      `json:"ranked"`   // of those, in the top results
}

// Report is the full summary.
type Report struct {
	Base          Base         `json:"base"`
	Options       Options      `json:"options"`
	Markets       []MarketInfo `json:"markets"`
	Entered       []Move       `json:"entered"`
	Gained        []Move       `json:"gained"`
	Dropped       []Move       `json:"dropped"`
	Lost          []Move       `json:"lost"`
	Opportunities []Move       `json:"opportunities"`
	Dead          []Dead       `json:"dead"`
	Alerts        []Alert      `json:"alerts"`
}

// Build compares each market's latest run with its base run and applies the
// suggestion rules. Only configured terms are reported.
func Build(markets []Market, base Base, opts Options) Report {
	opts = opts.withDefaults()
	if base.Kind == "" {
		base.Kind = BasePrevious
	}
	r := Report{
		Base: base, Options: opts,
		Entered: []Move{}, Gained: []Move{}, Dropped: []Move{}, Lost: []Move{},
		Opportunities: []Move{}, Dead: []Dead{}, Alerts: []Alert{},
		Markets: []MarketInfo{},
	}

	for _, m := range markets {
		runs, byRun := groupRuns(m.History)
		if len(runs) == 0 {
			continue
		}
		latest := runs[len(runs)-1]
		info := MarketInfo{Country: m.Country, LatestAt: latest}

		baseAt, note := pickBase(runs, m.Releases, base)
		info.Note = note
		if !baseAt.IsZero() {
			info.HasBase, info.BaseAt = true, baseAt
		}

		cur := byRun[latest]
		prev := byRun[baseAt] // nil map when there is no base
		var alertKws []string

		for _, kw := range m.Terms {
			c, hasCur := cur[kw]
			if hasCur {
				info.Keywords++
				if c.Found {
					info.Ranked++
					if isOpportunity(c, opts) {
						r.Opportunities = append(r.Opportunities, move(m.Country, store.Snapshot{}, c))
					}
				}
			}
			if !info.HasBase {
				continue
			}
			p, hasPrev := prev[kw]
			switch {
			case hasPrev && !hasCur:
				info.Unmeasured = append(info.Unmeasured, kw)
			case !hasPrev || !hasCur:
				// Newly configured keyword: nothing to compare yet.
			case !p.Found && c.Found:
				r.Entered = append(r.Entered, move(m.Country, p, c))
			case p.Found && !c.Found:
				r.Lost = append(r.Lost, move(m.Country, p, c))
				alertKws = append(alertKws, kw)
			case p.Found && c.Found:
				mv := move(m.Country, p, c)
				noise := opts.Noise
				if p.Position <= opts.TopTier || c.Position <= opts.TopTier {
					noise = 0
				}
				switch {
				case mv.Delta > noise:
					r.Gained = append(r.Gained, mv)
				case mv.Delta < -noise:
					r.Dropped = append(r.Dropped, mv)
				}
				if mv.Delta < -opts.AlertDrop {
					alertKws = append(alertKws, kw)
				}
			}
		}

		if len(alertKws) >= opts.AlertCount {
			r.Alerts = append(r.Alerts, Alert{Country: m.Country, Keywords: alertKws})
		}
		r.Dead = append(r.Dead, deadKeywords(m.Country, m.Terms, runs, byRun, opts.DeadRuns)...)
		r.Markets = append(r.Markets, info)
	}

	sort.SliceStable(r.Entered, func(i, j int) bool { return r.Entered[i].Now < r.Entered[j].Now })
	sort.SliceStable(r.Gained, func(i, j int) bool { return r.Gained[i].Delta > r.Gained[j].Delta })
	sort.SliceStable(r.Dropped, func(i, j int) bool { return r.Dropped[i].Delta < r.Dropped[j].Delta })
	sort.SliceStable(r.Lost, func(i, j int) bool { return r.Lost[i].Was < r.Lost[j].Was })
	sort.SliceStable(r.Opportunities, func(i, j int) bool { return r.Opportunities[i].Now < r.Opportunities[j].Now })
	return r
}

// groupRuns splits a market's snapshots into runs (one timestamp per run),
// returning the run times oldest first and each run's snapshots by keyword.
func groupRuns(history []store.Snapshot) ([]time.Time, map[time.Time]map[string]store.Snapshot) {
	byRun := map[time.Time]map[string]store.Snapshot{}
	for _, s := range history {
		t := s.Timestamp.UTC()
		if byRun[t] == nil {
			byRun[t] = map[string]store.Snapshot{}
		}
		byRun[t][s.Keyword] = s
	}
	runs := make([]time.Time, 0, len(byRun))
	for t := range byRun {
		runs = append(runs, t)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Before(runs[j]) })
	return runs, byRun
}

// pickBase returns the base run time (zero when there is none) and a note
// explaining why there is none.
func pickBase(runs []time.Time, releases []store.Release, base Base) (time.Time, string) {
	if len(runs) < 2 {
		return time.Time{}, "only one run so far"
	}
	latest := runs[len(runs)-1]
	earlier := runs[:len(runs)-1]

	switch base.Kind {
	case BaseDate:
		best, bestDiff := time.Time{}, time.Duration(1)<<62
		for _, t := range earlier {
			d := t.Sub(base.Date)
			if d < 0 {
				d = -d
			}
			if d < bestDiff {
				best, bestDiff = t, d
			}
		}
		return best, ""
	case BaseRelease:
		var releasedAt time.Time
		for _, rel := range releases {
			if rel.Version == base.Version {
				releasedAt = rel.ReleasedAt
			}
		}
		if releasedAt.IsZero() {
			return time.Time{}, fmt.Sprintf("version %s not seen in this storefront yet", base.Version)
		}
		var best time.Time
		for _, t := range earlier {
			if t.Before(releasedAt) {
				best = t
			}
		}
		if best.IsZero() {
			return time.Time{}, fmt.Sprintf("no run before %s was released", base.Version)
		}
		if !latest.After(releasedAt) {
			return time.Time{}, fmt.Sprintf("no run since %s was released", base.Version)
		}
		return best, ""
	default:
		return earlier[len(earlier)-1], ""
	}
}

func isOpportunity(s store.Snapshot, o Options) bool {
	return s.Found && s.Position >= o.OppMinRank && s.Position <= o.OppMaxRank &&
		s.Difficulty != nil && *s.Difficulty <= o.OppMaxDifficulty &&
		s.Relevance != nil && *s.Relevance >= o.OppMinRelevance
}

// deadKeywords returns configured terms whose last n measurements all missed
// the top results. Terms measured fewer than n times are not judged yet.
func deadKeywords(country string, terms []string, runs []time.Time, byRun map[time.Time]map[string]store.Snapshot, n int) []Dead {
	var out []Dead
	for _, kw := range terms {
		seen := 0
		dead := true
		for i := len(runs) - 1; i >= 0 && seen < n; i-- {
			s, ok := byRun[runs[i]][kw]
			if !ok {
				continue
			}
			seen++
			if s.Found {
				dead = false
				break
			}
		}
		if dead && seen >= n {
			out = append(out, Dead{Country: country, Keyword: kw, Runs: seen})
		}
	}
	return out
}

func move(country string, p, c store.Snapshot) Move {
	mv := Move{Country: country, Keyword: c.Keyword, Difficulty: c.Difficulty, Relevance: c.Relevance}
	if p.Found {
		mv.Was = p.Position
	}
	if c.Found {
		mv.Now = c.Position
	}
	if p.Found && c.Found {
		mv.Delta = p.Position - c.Position
	}
	return mv
}
