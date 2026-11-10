package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omervaroglu/asokit/internal/config"
	"github.com/omervaroglu/asokit/internal/itunes"
	"github.com/omervaroglu/asokit/internal/metrics"
	"github.com/omervaroglu/asokit/internal/rank"
	"github.com/omervaroglu/asokit/internal/store"
	"github.com/omervaroglu/asokit/internal/track"
	"github.com/omervaroglu/asokit/internal/web"
)

// cmdRun fetches the ranking proxy for every keyword in the config and stores a
// snapshot for each.
func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "asokit.yaml", "path to config file")
	dbPath := fs.String("db", "asokit.db", "path to SQLite database")
	delay := fs.Duration("delay", 3*time.Second, "delay between keyword requests (throttle)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	client := itunes.New()
	// Resolve the relevance criteria once (one Lookup at most), outside the
	// per-market loop; the genre is effectively the same across storefronts.
	crit := track.ResolveRelevance(ctx, client, cfg.AppID, cfg.Markets[0].Country, cfg.Relevance)

	var totalStored, totalFailures int
	var at time.Time
	for _, m := range cfg.Markets {
		fmt.Printf("[%s]\n", strings.ToUpper(m.Country))
		// Version marker: record the app's current version for this storefront.
		// Non-fatal — a failed lookup must not abort keyword tracking.
		if err := track.CaptureRelease(ctx, client, db, cfg.AppID, m.Country); err != nil {
			fmt.Fprintf(os.Stderr, "  (version lookup failed: %v)\n", err)
		}
		rc := track.RunConfig{
			AppID:       cfg.AppID,
			Country:     m.Country,
			Keywords:    m.Terms(), // keywords + phrases, tracked together
			SearchLimit: cfg.SearchLimit,
			StoreTop:    cfg.StoreTop,
			Weights:     metrics.DefaultWeights,
			Relevance:   crit,
			Client:      client,
			Delay:       *delay,
		}
		res, err := track.RunOnce(ctx, db, rc,
			func(keyword string, r rank.Result, rerr error) {
				if rerr != nil {
					fmt.Fprintf(os.Stderr, "  %-30s error: %v\n", keyword, rerr)
					return
				}
				fmt.Printf("  %-30s %s\n", keyword, positionLabel(r.Position, r.Found))
			})
		if err != nil {
			return err
		}
		totalStored += res.Stored
		totalFailures += res.Failures
		at = res.At
	}

	fmt.Printf("\nStored %d keyword(s) across %d market(s) for app %d at %s\n",
		totalStored, len(cfg.Markets), cfg.AppID, at.Format(time.RFC3339))
	if totalFailures > 0 {
		return fmt.Errorf("%d keyword(s) failed", totalFailures)
	}
	return nil
}

// cmdOnce performs a single ad-hoc lookup without touching the database.
func cmdOnce(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("once", flag.ContinueOnError)
	appID := fs.Int64("app-id", 0, "numeric App Store app id (required)")
	country := fs.String("country", config.DefaultCountry, "two-letter storefront code")
	limit := fs.Int("limit", config.DefaultSearchLimit, "max results to fetch (<=200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	term := fs.Arg(0)
	if term == "" {
		return errors.New("usage: asokit once --app-id <id> [--country xx] <term>")
	}
	if *appID <= 0 {
		return errors.New("--app-id is required and must be positive")
	}

	results, err := track.Search(ctx, itunes.New(), term, *country, *limit)
	if err != nil {
		return err
	}
	res := rank.Find(*appID, results)
	fmt.Printf("term=%q app=%d country=%s -> %s (of %d results)\n",
		term, *appID, *country, positionLabel(res.Position, res.Found), res.ResultCount)
	return nil
}

// cmdReport prints the latest stored position per keyword.
func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	cfgPath := fs.String("config", "asokit.yaml", "path to config file")
	dbPath := fs.String("db", "asokit.db", "path to SQLite database")
	country := fs.String("country", "", "storefront to report (default: first configured market)")
	trend := fs.Bool("trend", false, "show movement versus the previous snapshot")
	competitors := fs.Bool("competitors", false, "add a column per configured competitor with its rank")
	compare := fs.String("compare", "", "compare the latest run with the run nearest this date (YYYY-MM-DD)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	market := cfg.Markets[0]
	if *country != "" {
		m, ok := cfg.Market(*country)
		if !ok {
			return fmt.Errorf("market %q is not in the config (have: %s)", *country, strings.Join(cfg.Countries(), ", "))
		}
		market = m
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if *compare != "" {
		return reportCompare(db, cfg, market, *compare)
	}

	latest, err := db.Latest(cfg.AppID, market.Country)
	if err != nil {
		return err
	}
	if len(latest) == 0 {
		fmt.Printf("No snapshots yet for %s. Run `asokit run` first.\n", strings.ToUpper(market.Country))
		return nil
	}
	byKeyword := make(map[string]store.Snapshot, len(latest))
	for _, s := range latest {
		byKeyword[s.Keyword] = s
	}

	// Competitor columns are opt-in; their ranks come from the stored results,
	// so a competitor below store_top shows as "—".
	var comps []config.Competitor
	var compIDs []int64
	if *competitors {
		comps = cfg.Competitors
		for _, c := range comps {
			compIDs = append(compIDs, c.ID)
		}
	}

	fmt.Printf("Market: %s  (app %d)\n", strings.ToUpper(market.Country), cfg.AppID)

	header := []string{"KEYWORD", "POSITION"}
	if *trend {
		header = append(header, "TREND")
	}
	header = append(header, "DIFF", "REL", "RESULTS")
	for _, c := range comps {
		header = append(header, competitorHeader(c))
	}
	header = append(header, "AS OF")

	// printSection renders one group (keywords or phrases) as its own aligned
	// table, iterating the configured terms so the report follows the config.
	printSection := func(title string, terms []string) error {
		if len(terms) == 0 {
			return nil
		}
		if title != "" {
			fmt.Printf("\n%s\n", title)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, kw := range terms {
			s, ok := byKeyword[kw]
			if !ok {
				// Configured but not measured yet.
				row := []string{kw, "—"}
				for i := 2; i < len(header)-1; i++ {
					row = append(row, "—")
				}
				row = append(row, "no data yet")
				_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
				continue
			}
			row := []string{kw, positionLabel(s.Position, s.Found)}
			if *trend {
				prevPos, prevFound, perr := db.PreviousPosition(cfg.AppID, market.Country, kw)
				if perr != nil {
					return perr
				}
				row = append(row, trendLabel(s, prevPos, prevFound))
			}
			row = append(row, difficultyLabel(s.Difficulty), relevanceLabel(s.Relevance), resultsLabel(s.ResultCount))
			if len(comps) > 0 {
				positions, perr := db.CompetitorPositions(s.ID, compIDs)
				if perr != nil {
					return perr
				}
				for _, c := range comps {
					row = append(row, competitorRank(positions, c.ID))
				}
			}
			row = append(row, s.Timestamp.Format("2006-01-02 15:04"))
			_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		return tw.Flush()
	}

	// A single untitled table when there are no phrases; two labelled sections
	// otherwise.
	kwTitle := ""
	if len(market.Phrases) > 0 {
		kwTitle = "Keywords"
	}
	if err := printSection(kwTitle, market.Keywords); err != nil {
		return err
	}
	return printSection("Phrases", market.Phrases)
}

// reportCompare shows, per keyword, how the latest run moved versus the run
// nearest a chosen date: position movement, difficulty change, and a new/lost
// flag when a keyword appears in only one of the two runs.
func reportCompare(db *store.Store, cfg *config.Config, market config.Market, dateStr string) error {
	target, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("--compare: invalid date %q, want YYYY-MM-DD", dateStr)
	}

	history, err := db.History(cfg.AppID, market.Country)
	if err != nil {
		return err
	}
	if len(history) == 0 {
		fmt.Printf("No snapshots yet for %s. Run `asokit run` first.\n", strings.ToUpper(market.Country))
		return nil
	}

	// Index by keyword→run-timestamp and collect the distinct run timestamps.
	byKwTS := map[string]map[string]store.Snapshot{}
	tsSet := map[string]time.Time{}
	for _, s := range history {
		ts := s.Timestamp.Format(time.RFC3339)
		if byKwTS[s.Keyword] == nil {
			byKwTS[s.Keyword] = map[string]store.Snapshot{}
		}
		byKwTS[s.Keyword][ts] = s
		tsSet[ts] = s.Timestamp
	}

	// The latest run, and the run whose timestamp is nearest the target date.
	var latestTS, baseTS string
	var latestT time.Time
	bestDiff := time.Duration(1) << 62
	for ts, t := range tsSet {
		if latestTS == "" || t.After(latestT) {
			latestTS, latestT = ts, t
		}
		d := t.Sub(target)
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			bestDiff, baseTS = d, ts
		}
	}

	fmt.Printf("Market: %s  (app %d)\n", strings.ToUpper(market.Country), cfg.AppID)
	fmt.Printf("Comparing %s → %s\n",
		tsSet[baseTS].Format("2006-01-02 15:04"), tsSet[latestTS].Format("2006-01-02 15:04"))
	if baseTS == latestTS {
		fmt.Println("(only one run near that date — nothing to compare)")
		return nil
	}

	header := []string{"KEYWORD", "WAS", "NOW", "MOVE", "DIFF Δ", "FLAG"}
	printSection := func(title string, terms []string) error {
		if len(terms) == 0 {
			return nil
		}
		if title != "" {
			fmt.Printf("\n%s\n", title)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, kw := range terms {
			base, hasBase := byKwTS[kw][baseTS]
			cur, hasCur := byKwTS[kw][latestTS]
			if !hasBase && !hasCur {
				continue
			}
			was, now := "—", "—"
			if hasBase {
				was = positionLabel(base.Position, base.Found)
			}
			if hasCur {
				now = positionLabel(cur.Position, cur.Found)
			}
			flag := ""
			switch {
			case hasCur && !hasBase:
				flag = "new"
			case hasBase && !hasCur:
				flag = "lost"
			}
			row := []string{kw, was, now,
				compareMove(base, hasBase, cur, hasCur),
				compareDiffDelta(base, hasBase, cur, hasCur), flag}
			_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		return tw.Flush()
	}

	kwTitle := ""
	if len(market.Phrases) > 0 {
		kwTitle = "Keywords"
	}
	if err := printSection(kwTitle, market.Keywords); err != nil {
		return err
	}
	return printSection("Phrases", market.Phrases)
}

// compareMove describes the position movement between two snapshots (positive
// movement = improved, i.e. a smaller rank number).
func compareMove(base store.Snapshot, hasBase bool, cur store.Snapshot, hasCur bool) string {
	if !hasBase || !hasCur {
		return "—"
	}
	switch {
	case !base.Found && !cur.Found:
		return "—"
	case base.Found && !cur.Found:
		return "dropped out"
	case !base.Found && cur.Found:
		return "entered"
	}
	switch d := base.Position - cur.Position; {
	case d > 0:
		return fmt.Sprintf("▲ %d", d)
	case d < 0:
		return fmt.Sprintf("▼ %d", -d)
	default:
		return "="
	}
}

// compareDiffDelta is the signed difficulty change, or "—" when either side
// lacks a difficulty score.
func compareDiffDelta(base store.Snapshot, hasBase bool, cur store.Snapshot, hasCur bool) string {
	if !hasBase || !hasCur || base.Difficulty == nil || cur.Difficulty == nil {
		return "—"
	}
	return fmt.Sprintf("%+.0f", *cur.Difficulty-*base.Difficulty)
}

// cmdExport writes all snapshots as CSV to stdout or a file.
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	dbPath := fs.String("db", "asokit.db", "path to SQLite database")
	out := fs.String("out", "", "output file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	snaps, err := db.All()
	if err != nil {
		return err
	}

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("creating %q: %w", *out, err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"timestamp", "app_id", "country", "keyword", "position", "found", "result_count", "difficulty", "relevance", "narrow"}); err != nil {
		return err
	}
	for _, s := range snaps {
		rec := []string{
			s.Timestamp.Format(time.RFC3339),
			strconv.FormatInt(s.AppID, 10),
			s.Country,
			s.Keyword,
			strconv.Itoa(s.Position),
			strconv.FormatBool(s.Found),
			strconv.Itoa(s.ResultCount),
			csvFloat(s.Difficulty),
			csvFloat(s.Relevance),
			csvBool(s.Narrow),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	if *out != "" {
		fmt.Fprintf(os.Stderr, "Wrote %d snapshot(s) to %s\n", len(snaps), *out)
	}
	return cw.Error()
}

// cmdServe runs the local web dashboard until interrupted.
func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "asokit.yaml", "path to config file")
	dbPath := fs.String("db", "asokit.db", "path to SQLite database")
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The dashboard can be set up from the browser, so a missing or malformed
	// config is not fatal: start uninitialized and let the user fix it there.
	var (
		cfg    *config.Config
		cfgErr string
	)
	switch loaded, lerr := config.Load(*cfgPath); {
	case lerr == nil:
		cfg = loaded
	case errors.Is(lerr, os.ErrNotExist):
		fmt.Println("No config yet — open the dashboard to set it up.")
	default:
		cfgErr = lerr.Error()
		fmt.Fprintf(os.Stderr, "config problem (fixable in the dashboard): %v\n", lerr)
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	handler, err := web.NewServer(db, cfg, cfgErr, *cfgPath).Handler()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	where := "not set up yet — open it to configure"
	if cfg != nil {
		where = fmt.Sprintf("app %d, markets: %s", cfg.AppID, strings.Join(cfg.Countries(), ", "))
	}
	errc := make(chan error, 1)
	go func() {
		fmt.Printf("asokit dashboard → %s  (%s)\nPress Ctrl+C to stop.\n",
			listenURL(*addr), where)
		errc <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nshutting down…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// listenURL turns a listen address into a clickable localhost URL.
func listenURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "127.0.0.1" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func positionLabel(position int, found bool) string {
	if !found {
		return "not in top results"
	}
	return "#" + strconv.Itoa(position)
}

// difficultyLabel and relevanceLabel render the nullable metrics, showing "—"
// for snapshots taken before the metrics existed.
func difficultyLabel(d *float64) string {
	if d == nil {
		return "—"
	}
	return strconv.FormatFloat(*d, 'f', 0, 64)
}

func relevanceLabel(r *float64) string {
	if r == nil {
		return "—"
	}
	return strconv.FormatFloat(*r, 'f', 0, 64)
}

// resultsLabel shows the result count, rendering the API's 200 ceiling as
// "200+" since it is a cap, not a true total.
func resultsLabel(count int) string {
	if count >= itunes.MaxResults {
		return "200+"
	}
	return strconv.Itoa(count)
}

// competitorHeader is the column title for a competitor: its name, or its id
// when unnamed.
func competitorHeader(c config.Competitor) string {
	if c.Name != "" {
		return c.Name
	}
	return strconv.FormatInt(c.ID, 10)
}

// competitorRank renders a competitor's stored rank, or "—" when it is below
// store_top (and so not tracked for this keyword).
func competitorRank(positions map[int64]int, id int64) string {
	if pos, ok := positions[id]; ok {
		return "#" + strconv.Itoa(pos)
	}
	return "—"
}

// csvFloat and csvBool render nullable metrics for CSV: empty cell when unset.
func csvFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

func csvBool(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}

// trendLabel describes how the latest position moved versus the previous one.
// Remember positions are ascending-is-better, so a smaller number is an
// improvement.
func trendLabel(cur store.Snapshot, prevPos int, prevFound bool) string {
	switch {
	case !prevFound && !cur.Found:
		return "—"
	case !prevFound && cur.Found:
		return "new"
	case prevFound && !cur.Found:
		return "dropped out"
	case cur.Position < prevPos:
		return fmt.Sprintf("▲ %d", prevPos-cur.Position)
	case cur.Position > prevPos:
		return fmt.Sprintf("▼ %d", cur.Position-prevPos)
	default:
		return "="
	}
}
