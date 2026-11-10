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
- Storefront validation: a market's country must be a real App Store storefront
  (ISO 3166-1 alpha-2). Language codes typed by mistake are rejected with a
  suggestion (e.g. `zh` → "use cn"), surfaced in the setup form instead of
  silently failing every request at run time.
- YAML config, local SQLite storage, request throttling and backoff on API
  rate limiting.
- CI (vet, test, build, lint) and a daily-tracking workflow template.

### Fixed
- Retry transient network failures, not just rate limiting: server errors
  (HTTP 5xx) and request timeouts / "context deadline exceeded" now get the
  same exponential backoff, so a keyword is no longer dropped from a run over a
  single flaky request. A caller-cancelled context still stops immediately.

[Unreleased]: https://github.com/omervaroglu/asokit/commits/main
