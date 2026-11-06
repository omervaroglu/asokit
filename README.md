# asokit

[![CI](https://github.com/omervaroglu/asokit/actions/workflows/ci.yml/badge.svg)](https://github.com/omervaroglu/asokit/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/omervaroglu/asokit.svg)](https://pkg.go.dev/github.com/omervaroglu/asokit)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**asokit** is a small, dependency-light command-line tool that tracks where an
app ranks in App Store search for a set of keywords, over time. It stores each
measurement locally in SQLite so you can watch keywords move up and down.

It's built for App Store Optimization (ASO) work: pick the keywords you care
about, run `asokit run` on a schedule, and see the trend.

```
$ asokit report --trend
KEYWORD     POSITION  TREND  AS OF
calm        #1        =      2026-11-06 12:12
meditation  #3        ▲ 2     2026-11-06 12:12
sleep       #4        ▼ 1     2026-11-06 12:12
```

## How ranking is measured (and its limits)

asokit reads results from Apple's public [iTunes Search API][search-api] and
records the position of your app for each keyword.

**Important:** the Search API returns a *relevance* ordering of apps. It is
**not** the exact, personalized, ad-influenced ranking a user sees in the App
Store app. Treat the position asokit reports as a **ranking proxy** — it is
reliable for tracking *movement over time* for a keyword, but it is not an
absolute "you are #N in the App Store" number. This is a deliberate trade-off:
the Search API is free, needs no account, and works for any app.

Because fetches are capped at 200 results, "not in top results" means your app
was not in the top 200 for that term — not that it is entirely unranked.

## Install

With Go 1.26+:

```bash
go install github.com/omervaroglu/asokit/cmd/asokit@latest
```

Or build from source:

```bash
git clone https://github.com/omervaroglu/asokit
cd asokit
make build        # produces ./asokit
```

## Usage

1. Create a config from the example:

   ```bash
   cp asokit.example.yaml asokit.yaml
   ```

   ```yaml
   app_id: 571800810   # the "id" from the App Store URL
   country: us         # two-letter storefront code
   keywords:
     - meditation
     - sleep
     - calm
   ```

2. Take a measurement — this fetches each keyword and stores a snapshot:

   ```bash
   asokit run
   ```

3. See the latest positions, or the movement since the previous run:

   ```bash
   asokit report
   asokit report --trend
   ```

### Commands

| Command          | Description                                                      |
| ---------------- | ---------------------------------------------------------------- |
| `asokit run`     | Fetch and store the current position for every keyword in config |
| `asokit once`    | One-off lookup of a single term without touching the database    |
| `asokit report`  | Show the latest stored position per keyword (`--trend` for movement) |
| `asokit export`  | Export all stored snapshots as CSV (`--out file.csv`)            |
| `asokit version` | Print the version                                                |

Quick one-off without a config file:

```bash
asokit once --app-id 571800810 --country us "meditation"
# term="meditation" app=571800810 country=us -> #3 (of 192 results)
```

### Flags

- `--config` (default `asokit.yaml`) — config path
- `--db` (default `asokit.db`) — SQLite database path
- `--delay` (default `3s`, `run` only) — pause between keyword requests to stay
  friendly to the API. On throttling (HTTP 429/403) asokit backs off and retries.

## Running on a schedule

Because asokit writes to a local SQLite file, you can run it from `cron`:

```cron
0 9 * * *  cd /path/to/project && /usr/local/bin/asokit run >> asokit.log 2>&1
```

A GitHub Actions workflow that runs daily and commits a CSV export is included
as a template at [`.github/workflows/track.yml`](.github/workflows/track.yml)
(disabled by default — enable the `schedule` trigger when you're ready).

## Development

```bash
make test      # run tests
make lint      # go vet + golangci-lint (if installed)
make build     # build ./asokit
```

## License

[MIT](LICENSE) © Ömer Faruk Varoğlu

[search-api]: https://performance-partners.apple.com/search-api
