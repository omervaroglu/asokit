# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Initial release of `asokit`.
- `run` — fetch and store the ranking proxy for every keyword in the config.
- `once` — one-off lookup of a single term for an app.
- `report` — show the latest position per keyword, with `--trend` for movement.
- `export` — export all snapshots as CSV.
- `serve` — embedded single-page web dashboard (small-multiple rank-over-time
  charts, trend badges, table view, light/dark themes); no external assets.
  Set up the app id, country and keywords from the browser (writes the config),
  and trigger a fetch with "Run now" — no terminal needed. Binds to loopback by
  default.
- `internal/track` — shared "measure every keyword once" routine used by both
  `run` and the dashboard's Run-now action.
- Multiple markets: one app id can track several storefronts, each with its own
  keyword list and history. Config uses a `markets:` list (old single-market
  `country:` + `keywords:` configs are migrated automatically). The dashboard
  shows market tabs with an "Add market" action; `run` sweeps all markets and
  `report --country xx` selects one. Keyword lists accept comma/semicolon
  separated input and are de-duplicated.
- Multiple apps: track several apps from one config and database. The config is
  an `apps:` list, each app with its own markets, competitors and relevance
  settings and its own history (old single-app configs — a top-level `app_id:`
  with `markets:` — are migrated automatically). The dashboard shows an app
  switcher with each app's icon and name (captured from the App Store on run);
  `run` sweeps every app, and `report`/`export` take `--app <id>` to focus on
  one.
- Storefront validation: a market's country must be a real App Store storefront
  (ISO 3166-1 alpha-2). Language codes typed by mistake are rejected with a
  suggestion (e.g. `zh` → "use cn"), surfaced in the setup form instead of
  silently failing every request at run time.
- YAML config, local SQLite storage, request throttling and backoff on API
  rate limiting.
- CI (vet, test, build, lint) and a daily-tracking workflow template.
- Competition metrics: each run keeps the top results per keyword (`store_top`,
  default 50) and computes a **difficulty** (0–100) and **relevance** (0–10)
  score per snapshot, documented and unit-tested in `internal/metrics`. Title
  matching is Unicode-safe (Turkish İ/ı, German ü/ß, CJK). A term returning the
  API's 200-result ceiling is marked *narrow*/shown as "200+". No field is a
  search-volume estimate.
- Competitor tracking: configure `competitors: [{id, name}]` and see each one's
  rank per keyword, derived from the stored results (no extra API calls).
  `report --competitors` adds a column each; the dashboard shows per-competitor
  rank-over-time sparklines in a keyword's detail.
- Keyword detail in the dashboard: click a card for the latest top-10 (name,
  rating count, genre, your own app highlighted). Table view gains sortable
  Difficulty / Relevance / Results columns.
- App version markers: each run records the app's current version and release
  date (deduplicated `releases` table); the dashboard draws them as dashed
  vertical lines with the version label on the rank-over-time charts.
- Phrase groups: a market may carry an optional `phrases:` list beside
  `keywords:`, tracked identically but shown as a separate section in the report
  and dashboard.
- `report --compare <YYYY-MM-DD>`: compare the latest run with the run nearest a
  date — per-keyword position movement, difficulty change, and new/lost flags.
- `export` now includes the difficulty, relevance and narrow columns.

### Fixed
- Retry transient network failures, not just rate limiting: server errors
  (HTTP 5xx) and request timeouts / "context deadline exceeded" now get the
  same exponential backoff, so a keyword is no longer dropped from a run over a
  single flaky request. A caller-cancelled context still stops immediately.

[Unreleased]: https://github.com/omervaroglu/asokit/commits/main
