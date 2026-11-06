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
	var totalStored, totalFailures int
	var at time.Time
	for _, m := range cfg.Markets {
		fmt.Printf("[%s]\n", strings.ToUpper(m.Country))
		res, err := track.RunOnce(ctx, db, cfg.AppID, m.Country, m.Keywords, cfg.SearchLimit, client, *delay,
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

	latest, err := db.Latest(cfg.AppID, market.Country)
	if err != nil {
		return err
	}
	if len(latest) == 0 {
		fmt.Printf("No snapshots yet for %s. Run `asokit run` first.\n", strings.ToUpper(market.Country))
		return nil
	}

	fmt.Printf("Market: %s  (app %d)\n", strings.ToUpper(market.Country), cfg.AppID)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if *trend {
		_, _ = fmt.Fprintln(tw, "KEYWORD\tPOSITION\tTREND\tAS OF")
	} else {
		_, _ = fmt.Fprintln(tw, "KEYWORD\tPOSITION\tAS OF")
	}
	for _, s := range latest {
		pos := positionLabel(s.Position, s.Found)
		asOf := s.Timestamp.Format("2006-01-02 15:04")
		if *trend {
			prevPos, prevFound, err := db.PreviousPosition(cfg.AppID, market.Country, s.Keyword)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Keyword, pos,
				trendLabel(s, prevPos, prevFound), asOf)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Keyword, pos, asOf)
		}
	}
	return tw.Flush()
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
	if err := cw.Write([]string{"timestamp", "app_id", "country", "keyword", "position", "found", "result_count"}); err != nil {
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
