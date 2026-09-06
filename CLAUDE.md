# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

One Go module with two related pieces:

1. **`uswds-usage`** — a Go CLI that reports which U.S. federal (`.gov`) websites use USWDS, by joining GSA's live Site Scanning CSV with analytics.usa.gov traffic data. It also maintains a dated historical archive (`snapshots/`) for trend tracking over time.
2. **The "USWDS Adoption Pulse" report** (`docs/index.html`) — a generated static report on USWDS program adoption, version-currency, and Core Web Vitals performance, built with a real compiled USWDS theme (not a look-alike CSS). Regenerated monthly by `.github/workflows/monthly-report.yml` and served via GitHub Pages from `docs/`.

The report consumes the CLI's snapshot archive, and both are Go: the CLI and the report generator share one module, with Node/Sass as the one remaining separate toolchain for the USWDS theme build. See `README.md` for full user-facing docs on both; this file is the cross-file "how it fits together" that isn't obvious from any single file.

## Commands

### Go CLI

```bash
go build -o uswds-usage .
go vet ./...
go run . report                     # equivalent to bare `go run .`
go run . backfill -date=YYYY-MM-DD -file=path/to/export.csv
go run . trend
go run . serve                      # serves docs/ at http://localhost:8000
```

```bash
go test ./...
```

There's no broad test suite yet, just `pyjson_test.go`'s regression guard for the Python-`json.dumps`-compatible encoder `build-report` relies on.

### Report pipeline (regenerates `docs/index.html`)

```bash
./uswds-usage report                    # writes snapshots/<today>/
./uswds-usage refresh-gsa-history       # incremental; appends new GSA commits only
npm install && npm run build            # compiles theme/ -> docs/assets/uswds/
./uswds-usage build-report              # writes docs/index.html
```

This exact sequence is what the monthly GitHub Actions workflow runs and commits back to the repo.

## Architecture

### CLI subcommand dispatch

`main.go` does its own arg-based dispatch — no cobra/cli framework. Bare `uswds-usage` or `uswds-usage -flag` is a backward-compatible alias for `report` (this predates the other subcommands and must keep working unmodified). Each subcommand (`report` in `main.go`, `backfill.go`, `trend.go`, `serve.go`, `refreshgsahistory.go`, `buildreport.go`) owns its own `flag.NewFlagSet`.

### CLI data flow

`fetch.go` (shared HTTP+CSV streaming helper) → `sitescanning.go` / `analytics.go` (parse the two upstream CSVs into `SiteScanRecord`/`AnalyticsRecord`) → `report.go` (`buildReport` dedupes multiple site-scanning rows per domain and joins in traffic data, producing one `ReportRow` per adopting domain) → `snapshot.go` writes both the flat `-output` CSV and a dated archive entry under `snapshots/<date>/`: a 9-column CSV plus `meta.json` provenance (upstream source URLs, `Last-Modified`/`ETag`, GSA's full column list at fetch time, and the traffic-weighted top-N coverage SLI computed against the *full* analytics universe, not just adopters). `trend.go` reads that archive back and computes adoption metrics across dates. `backfill.go` ingests older report CSVs — both the current 7-column schema and a legacy 5-column schema — into the same archive format, never inferring the snapshot date from file mtime.

### The report pipeline (`docs/`)

`external-data/` holds three checked-in reference datasets, each with real data-quality caveats that the code — not just the docs — accounts for:

- `gsa-uswds-report-history.csv` — GSA's own daily cohort-level adoption report, extracted from that repo's commit history via `refreshgsahistory.go`. Has a ~10x methodology-break jump on 2026-03-25 (a GSA bug fix) and one broken scan day (2026-06-26).
- `httparchive-uswds-origins.csv` / `httparchive-uswds-good-cwv.csv` — independent, web-wide HTTP Archive data (not limited to `.gov`). Nothing auto-refreshes these; they're replaced by hand when new query results exist.

`buildreport.go` is the single source of truth for how those caveats get applied: `gsaCleanStart`, `gsaExcludedDates`, and `httparchiveCleanStart` near the top of the file are the exclusion boundaries used throughout. It also re-scans the GSA data on every run for *new* anomalies of the same shape (a cohort's day-over-day count more than doubling or halving) and prints a warning rather than silently trusting a bad month — check that output before trusting a regenerated report. `pyjson.go` holds the hand-rolled encoder for the embedded chart-data JSON, matching Python `json.dumps`'s separators, `ensure_ascii` escaping, and float formatting exactly (a holdover requirement from when this was `scripts/build_report.py`, kept so the report's output format didn't change when the generator was ported to Go).

`docs/index.html` and `docs/assets/uswds/` are **generated output that's committed to git** — GitHub Pages serves `docs/` as static files with no build step of its own, so the compiled theme has to live in the repo. Don't hand-edit `docs/index.html`; change `buildreport.go` and regenerate. `theme/` is the Sass source for `docs/assets/uswds/css/uswds.css` (compiled by `npm run build`): it customizes stock USWDS by a single Sass setting (switches the heading font role to the built-in `public-sans` typeface token), following the settings-first customization approach documented in `README.md`.

## Gotchas

- `report.csv` and `uswds-traffic-report.csv` in the repo root are gitignored scratch output from running the CLI — not the historical record. `snapshots/` is the actual archive, and it *is* tracked.
