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
- YAML config, local SQLite storage, request throttling and backoff on API
  rate limiting.
- CI (vet, test, build, lint) and a daily-tracking workflow template.

[Unreleased]: https://github.com/omervaroglu/asokit/commits/main
