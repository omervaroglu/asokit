# Contributing to asokit

Thanks for your interest in improving asokit! Contributions of all sizes are
welcome — bug reports, documentation fixes, and features.

## Getting started

```bash
git clone https://github.com/omervaroglu/asokit
cd asokit
make test
make build
```

You'll need Go 1.26 or newer. There are no other required tools; `golangci-lint`
is optional (`make lint` falls back to `go vet` without it).

## Development workflow

1. Create a branch off `main`.
2. Make your change, with tests where it makes sense.
3. Run the checks locally before opening a PR:

   ```bash
   make fmt
   make lint
   make test
   ```

4. Open a pull request using the template. Keep PRs focused and small.

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add CSV import
fix: handle empty search results
docs: clarify the ranking-proxy caveat
chore: bump dependencies
```

This keeps the history readable and feeds into the changelog.

## Code style

- Keep the dependency footprint small — prefer the standard library.
- Public functions and packages get doc comments.
- Format with `gofmt -s` (`make fmt`).

## Reporting bugs

Open an issue using the bug report template. Include the command you ran, what
you expected, and what happened (the `--db` and config are helpful, minus any
private data).

## Versioning

asokit follows [Semantic Versioning](https://semver.org/). Releases are tagged
`vMAJOR.MINOR.PATCH`.
