package summary

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/omervaroglu/asokit/internal/store"
)

var (
	day1 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	day2 = day1.AddDate(0, 0, 1)
	day3 = day1.AddDate(0, 0, 2)
)

func f(v float64) *float64 { return &v }

// snap builds a snapshot; pos 0 means not in the top results.
func snap(t time.Time, kw string, pos int) store.Snapshot {
	return store.Snapshot{Timestamp: t, Keyword: kw, Position: pos, Found: pos > 0,
		Difficulty: f(30), Relevance: f(8)}
}

func keywords(r []Move) []string {
	out := make([]string, len(r))
	for i, m := range r {
		out[i] = m.Keyword
	}
	return out
}

func TestBuildPreviousRun(t *testing.T) {
	m := Market{
		Country: "us",
		Terms:   []string{"calm", "sleep", "focus", "rain", "noise", "breathe", "failed"},
		History: []store.Snapshot{
			snap(day1, "calm", 50), snap(day2, "calm", 10), // gained 40
			snap(day1, "sleep", 10), snap(day2, "sleep", 30), // dropped 20
			snap(day1, "focus", 0), snap(day2, "focus", 7), // entered
			snap(day1, "rain", 90), snap(day2, "rain", 0), // lost
			snap(day1, "noise", 20), snap(day2, "noise", 23), // within noise
			snap(day2, "breathe", 5), // new keyword, no base
			snap(day1, "failed", 3),  // not measured in latest run
		},
	}
	r := Build([]Market{m}, Base{Kind: BasePrevious}, Options{})

	check := func(name string, got []Move, want ...string) {
		t.Helper()
		if g := strings.Join(keywords(got), ","); g != strings.Join(want, ",") {
			t.Errorf("%s = %q, want %q", name, g, strings.Join(want, ","))
		}
	}
	check("entered", r.Entered, "focus")
	check("gained", r.Gained, "calm")
	check("dropped", r.Dropped, "sleep")
	check("lost", r.Lost, "rain")
	if r.Gained[0].Delta != 40 || r.Dropped[0].Delta != -20 {
		t.Errorf("deltas = %d, %d; want 40, -20", r.Gained[0].Delta, r.Dropped[0].Delta)
	}

	info := r.Markets[0]
	if !info.HasBase || !info.BaseAt.Equal(day1) || !info.LatestAt.Equal(day2) {
		t.Errorf("market info = %+v", info)
	}
	if strings.Join(info.Unmeasured, ",") != "failed" {
		t.Errorf("unmeasured = %v, want [failed]", info.Unmeasured)
	}
	if info.Keywords != 6 || info.Ranked != 5 {
		t.Errorf("keywords/ranked = %d/%d, want 6/5", info.Keywords, info.Ranked)
	}
	// Opportunities: ranked 11–40 with low difficulty, high relevance.
	check("opportunities", r.Opportunities, "noise", "sleep")
}

func TestBuildReleaseBase(t *testing.T) {
	rel := day2.Add(time.Hour) // released after the day-2 run
	m := Market{
		Country:  "de",
		Terms:    []string{"calm"},
		History:  []store.Snapshot{snap(day1, "calm", 80), snap(day2, "calm", 60), snap(day3, "calm", 20)},
		Releases: []store.Release{{Version: "2.0", ReleasedAt: rel}},
	}
	r := Build([]Market{m}, Base{Kind: BaseRelease, Version: "2.0"}, Options{})
	if got := r.Markets[0].BaseAt; !got.Equal(day2) {
		t.Errorf("base = %v, want day2 (last run before the release)", got)
	}
	if len(r.Gained) != 1 || r.Gained[0].Delta != 40 {
		t.Errorf("gained = %+v, want one move of 40", r.Gained)
	}

	r = Build([]Market{m}, Base{Kind: BaseRelease, Version: "3.0"}, Options{})
	if info := r.Markets[0]; info.HasBase || !strings.Contains(info.Note, "3.0") {
		t.Errorf("unknown version: info = %+v", info)
	}
}

func TestBuildDateBaseSkipsLatest(t *testing.T) {
	m := Market{Country: "tr", Terms: []string{"calm"},
		History: []store.Snapshot{snap(day1, "calm", 80), snap(day3, "calm", 20)}}
	// day3 is nearest, but it is the latest run, so day1 must be used.
	r := Build([]Market{m}, Base{Kind: BaseDate, Date: day3}, Options{})
	if got := r.Markets[0].BaseAt; !got.Equal(day1) {
		t.Errorf("base = %v, want day1", got)
	}
}

func TestDeadAndAlerts(t *testing.T) {
	var h []store.Snapshot
	for _, d := range []time.Time{day1, day2, day3} {
		h = append(h, snap(d, "never", 0))
	}
	h = append(h, snap(day1, "young", 0), snap(day3, "young", 0)) // only 2 runs: not judged
	for _, kw := range []string{"a", "b", "c"} {
		h = append(h, snap(day2, kw, 10), snap(day3, kw, 60))
	}
	m := Market{Country: "cn", Terms: []string{"never", "young", "a", "b", "c"}, History: h}
	r := Build([]Market{m}, Base{}, Options{})

	if len(r.Dead) != 1 || r.Dead[0].Keyword != "never" || r.Dead[0].Runs != 3 {
		t.Errorf("dead = %+v, want only 'never' over 3 runs", r.Dead)
	}
	if len(r.Alerts) != 1 || len(r.Alerts[0].Keywords) != 3 {
		t.Errorf("alerts = %+v, want one alert with 3 keywords", r.Alerts)
	}
}

func TestSingleRunHasNoBase(t *testing.T) {
	m := Market{Country: "jp", Terms: []string{"calm"}, History: []store.Snapshot{snap(day1, "calm", 3)}}
	r := Build([]Market{m}, Base{}, Options{})
	if r.Markets[0].HasBase || len(r.Gained)+len(r.Dropped)+len(r.Entered)+len(r.Lost) != 0 {
		t.Errorf("single run should have nothing to compare: %+v", r)
	}
}

func TestWriteFormats(t *testing.T) {
	m := Market{Country: "us", Terms: []string{"calm", "a|b"},
		History: []store.Snapshot{snap(day1, "calm", 50), snap(day2, "calm", 10),
			snap(day1, "a|b", 0), snap(day2, "a|b", 4)}}
	r := Build([]Market{m}, Base{}, Options{})

	var txt bytes.Buffer
	if err := Write(&txt, r, "Demo", FormatText); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Demo", "Gained more than 5 places (1)", "▲ 40", "Entered the top results (1)"} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("text output missing %q:\n%s", want, txt.String())
		}
	}
	if strings.Contains(txt.String(), "Dropped") {
		t.Errorf("empty sections should be omitted:\n%s", txt.String())
	}

	var md bytes.Buffer
	if err := Write(&md, r, "Demo", FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Demo", "### Gained", "| US | calm | #50 | #10 | ▲ 40 |", `a\|b`} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown output missing %q:\n%s", want, md.String())
		}
	}
}
