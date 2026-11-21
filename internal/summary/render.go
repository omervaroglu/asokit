package summary

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Format is an output format for Write.
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "md"
)

// Write renders the report. title is the heading line (e.g. the app name).
func Write(w io.Writer, r Report, title string, format Format) error {
	var out sink
	if format == FormatMarkdown {
		out = &mdSink{w: w}
	} else {
		out = &textSink{w: w}
	}

	out.title(title)
	out.line(baseLabel(r.Base))
	out.line(totals(r))

	out.table(fmt.Sprintf("Entered the top results (%d)", len(r.Entered)),
		[]string{"MARKET", "KEYWORD", "NOW"}, rows(r.Entered, func(m Move) []string {
			return []string{up(m.Country), m.Keyword, rank(m.Now)}
		}))
	out.table(fmt.Sprintf("Gained more than %d places (%d)", r.Options.Noise, len(r.Gained)),
		[]string{"MARKET", "KEYWORD", "WAS", "NOW", "MOVE"}, rows(r.Gained, moveRow))
	out.table(fmt.Sprintf("Dropped more than %d places (%d)", r.Options.Noise, len(r.Dropped)),
		[]string{"MARKET", "KEYWORD", "WAS", "NOW", "MOVE"}, rows(r.Dropped, moveRow))
	out.table(fmt.Sprintf("Fell out of the top results (%d)", len(r.Lost)),
		[]string{"MARKET", "KEYWORD", "WAS"}, rows(r.Lost, func(m Move) []string {
			return []string{up(m.Country), m.Keyword, rank(m.Was)}
		}))

	var unmeasured [][]string
	for _, m := range r.Markets {
		if len(m.Unmeasured) > 0 {
			unmeasured = append(unmeasured, []string{up(m.Country), strings.Join(m.Unmeasured, ", ")})
		}
	}
	out.table("Not measured in the latest run (failed requests?)", []string{"MARKET", "KEYWORDS"}, unmeasured)

	out.heading("Suggestions")
	o := r.Options
	out.table(fmt.Sprintf("Opportunities — rank %d–%d, difficulty ≤ %s, relevance ≥ %s (%d)",
		o.OppMinRank, o.OppMaxRank, num(o.OppMaxDifficulty), num(o.OppMinRelevance), len(r.Opportunities)),
		[]string{"MARKET", "KEYWORD", "NOW", "DIFF", "REL"}, rows(r.Opportunities, func(m Move) []string {
			return []string{up(m.Country), m.Keyword, rank(m.Now), ptr(m.Difficulty), ptr(m.Relevance)}
		}))

	var alerts [][]string
	for _, a := range r.Alerts {
		alerts = append(alerts, []string{up(a.Country), strconv.Itoa(len(a.Keywords)), strings.Join(a.Keywords, ", ")})
	}
	out.table(fmt.Sprintf("Check — markets where %d+ keywords dropped more than %d places or fell out", o.AlertCount, o.AlertDrop),
		[]string{"MARKET", "COUNT", "KEYWORDS"}, alerts)

	out.table(fmt.Sprintf("Consider removing — not in the top results in the last %d runs (%d)", o.DeadRuns, len(r.Dead)),
		[]string{"MARKET", "COUNT", "KEYWORDS"}, deadRows(r.Dead))

	var notes [][]string
	for _, m := range r.Markets {
		if !m.HasBase {
			notes = append(notes, []string{up(m.Country), m.Note})
		}
	}
	out.table("Markets without a comparison", []string{"MARKET", "REASON"}, notes)
	return out.err()
}

func baseLabel(b Base) string {
	switch b.Kind {
	case BaseDate:
		return "Compared with: the run nearest " + b.Date.Format("2006-01-02") + " in each market"
	case BaseRelease:
		return "Compared with: the last run before " + b.Version + " was released in each market"
	default:
		return "Compared with: the previous run in each market"
	}
}

func totals(r Report) string {
	kw, ranked, compared := 0, 0, 0
	for _, m := range r.Markets {
		kw += m.Keywords
		ranked += m.Ranked
		if m.HasBase {
			compared++
		}
	}
	return fmt.Sprintf("%d markets (%d compared) · %d keywords measured · %d in the top results",
		len(r.Markets), compared, kw, ranked)
}

func moveRow(m Move) []string {
	arrow := "▲"
	d := m.Delta
	if d < 0 {
		arrow, d = "▼", -d
	}
	return []string{up(m.Country), m.Keyword, rank(m.Was), rank(m.Now), fmt.Sprintf("%s %d", arrow, d)}
}

func deadRows(dead []Dead) [][]string {
	by := map[string][]string{}
	var countries []string
	for _, d := range dead {
		if by[d.Country] == nil {
			countries = append(countries, d.Country)
		}
		by[d.Country] = append(by[d.Country], d.Keyword)
	}
	sort.SliceStable(countries, func(i, j int) bool { return len(by[countries[i]]) > len(by[countries[j]]) })
	out := make([][]string, 0, len(countries))
	for _, c := range countries {
		out = append(out, []string{up(c), strconv.Itoa(len(by[c])), strings.Join(by[c], ", ")})
	}
	return out
}

func rows(ms []Move, f func(Move) []string) [][]string {
	out := make([][]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, f(m))
	}
	return out
}

func rank(p int) string {
	if p == 0 {
		return "—"
	}
	return "#" + strconv.Itoa(p)
}

func ptr(v *float64) string {
	if v == nil {
		return "—"
	}
	return num(*v)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }

func up(s string) string { return strings.ToUpper(s) }

// sink is a minimal document writer so text and Markdown share one layout.
// Empty tables are skipped.
type sink interface {
	title(s string)
	heading(s string)
	line(s string)
	table(caption string, header []string, rows [][]string)
	err() error
}

type textSink struct {
	w io.Writer
	e error
}

func (t *textSink) printf(format string, a ...any) {
	if t.e == nil {
		_, t.e = fmt.Fprintf(t.w, format, a...)
	}
}
func (t *textSink) title(s string)   { t.printf("%s\n", s) }
func (t *textSink) heading(s string) { t.printf("\n== %s ==\n", s) }
func (t *textSink) line(s string)    { t.printf("%s\n", s) }
func (t *textSink) err() error       { return t.e }
func (t *textSink) table(caption string, header []string, rows [][]string) {
	if len(rows) == 0 || t.e != nil {
		return
	}
	t.printf("\n%s\n", caption)
	tw := tabwriter.NewWriter(t.w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  "+strings.Join(header, "\t"))
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, "  "+strings.Join(r, "\t"))
	}
	if err := tw.Flush(); err != nil {
		t.e = err
	}
}

type mdSink struct {
	w io.Writer
	e error
}

func (m *mdSink) printf(format string, a ...any) {
	if m.e == nil {
		_, m.e = fmt.Fprintf(m.w, format, a...)
	}
}
func (m *mdSink) title(s string)   { m.printf("# %s\n\n", s) }
func (m *mdSink) heading(s string) { m.printf("## %s\n\n", s) }
func (m *mdSink) line(s string)    { m.printf("%s  \n", s) }
func (m *mdSink) err() error       { return m.e }
func (m *mdSink) table(caption string, header []string, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	m.printf("\n### %s\n\n| %s |\n|%s\n", caption, strings.Join(header, " | "), strings.Repeat(" --- |", len(header)))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = strings.ReplaceAll(c, "|", `\|`)
		}
		m.printf("| %s |\n", strings.Join(cells, " | "))
	}
	m.printf("\n")
}
