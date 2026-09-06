# uswds-usage

A command-line utility that reports which U.S. federal government websites
use the [U.S. Web Design System (USWDS)](https://designsystem.digital.gov/)
and how much traffic they get.

It downloads two public datasets, joins them by domain, and writes a CSV
report plus a console summary:

1. **[GSA Site Scanning](https://api.gsa.gov/technology/site-scanning/)** —
   scans of federal websites that detect USWDS usage (CSS class names,
   semantic version, agency/bureau ownership).
2. **[analytics.usa.gov](https://analytics.usa.gov/)** — 30-day pageview and
   visit totals for the top 100,000 domains across federal sites.

## Requirements

- Go 1.26.3+ (see `go.mod`)
- Node.js + npm (for the USWDS theme build below)
- Internet access (the tool fetches live data on every run; nothing is
  bundled or cached)

## `docs/index.html` is built with real USWDS

The report itself uses actual [USWDS](https://designsystem.digital.gov/)
components (`usa-card`, `usa-alert`, `usa-table`), the USWDS grid, and a
compiled USWDS theme — not a look-alike. `theme/` is a minimal Sass build:

```bash
npm install
npm run build   # compiles theme/styles.scss -> docs/assets/uswds/css/uswds.css,
                 # and copies the fonts/icons/js it needs into docs/assets/uswds/
```

`theme/_uswds-theme.scss` is the one *typography* customization on top of
stock USWDS — following the settings-first approach from
[uswds/uswds#6765](https://github.com/uswds/uswds/discussions/6765#discussioncomment-17674633)
("modify Sass variables" before utility classes or custom CSS), it switches
the heading font role to the built-in `public-sans` typeface token so the
whole report reads in one voice. `./uswds-usage build-report` (below) then
generates `docs/index.html` against that compiled CSS; run `npm run build`
again any time `theme/` changes or USWDS is upgraded in `package.json`.

### It's a selective build, not the full USWDS bundle

`theme/styles.scss` forwards only the USWDS packages this report's markup
actually uses (`usa-card`, `usa-alert`, `usa-table`, `usa-layout-grid`,
`uswds-typography`, `uswds-global`) instead of `@forward "uswds"` — the
monolith that pulls in every component (accordion, banner, header, hero,
modal, nav, date picker, and everything else) regardless of whether a
project uses them. `theme/_uswds-theme.scss` further restricts the utilities
package to only the modules this page's classes need
(`$output-these-utilities`) and turns off the serif/mono font *type* slots
entirely (`$theme-font-type-serif/-mono: false`) since nothing here renders
in those roles — USWDS otherwise ships a Merriweather and Roboto Mono
`@font-face` block unconditionally, regardless of whether any role points
at them. `theme/copy-assets.js` (run by `npm run build:assets`) mirrors the
same idea for non-CSS assets: it only copies the Public Sans font files and
the exact handful of icon SVGs the compiled CSS actually references (parsed
out of `docs/assets/uswds/css/uswds.css` itself), not the full ~250-icon set.
Neither `uswds-init.js` nor `uswds.min.js` is loaded at all — every
component in use here (card, alert, table, prose) is static markup and CSS
with no JS of its own; those two scripts exist for banner/header/modal FOUC
prevention and interactive-component initialization (accordion, combo-box,
sortable tables, dismissible alerts), none of which this page has.

Net effect: the compiled CSS goes from ~570KB (full `uswds` bundle) to
~240KB, and the asset directory from ~16MB (full icon/font sets vendored) to
under 1MB.

**If you add a new component or utility class to `buildreport.go`**,
it may silently not exist in the compiled output — no error, the class just
won't match anything. Re-run this to see the real class inventory, and
extend `theme/styles.scss` / `$output-these-utilities` accordingly:

```bash
grep -o 'class="[^"]*"' docs/index.html | tr ' ' '\n' | sort -u
```

## The monthly USWDS Adoption Pulse report

`.github/workflows/monthly-report.yml` runs on the 1st of every month (and
on demand via `workflow_dispatch`) and regenerates `docs/index.html` — a
program-health report covering .gov adoption, version-currency, and a
Core Web Vitals performance comparison against the web at large. Each run:

1. `./uswds-usage report` — a fresh dated snapshot under `data/snapshots/`.
2. `./uswds-usage refresh-history` — pulls any new commits from GSA's
   [site-scanning-analysis](https://github.com/GSA/site-scanning-analysis)
   repo into `data/external/gsa-uswds-report-history.csv` (incremental —
   only fetches dates newer than what's already there).
3. `npm ci && npm run build` — compiles the USWDS theme (see below); a
   no-op in practice unless `theme/` or the USWDS version changed.
4. `./uswds-usage build-report` — recomputes every KPI and chart from the
   checked-in data and writes `docs/index.html`.
5. Commits the updated snapshot, history file, theme build, and report back
   to the repo.

**What doesn't auto-refresh:** the two HTTP Archive datasets
(`data/external/httparchive-uswds-origins.csv` and
`httparchive-uswds-good-cwv.csv`). Those come from querying HTTP Archive's
public dataset directly — replace those two files with fresh exports
(same `DateTime,ALL,USWDS` shape) whenever you have new numbers, then either
re-run `./uswds-usage build-report` locally or just let the next
scheduled run pick them up. `build-report` re-checks the GSA data for new
methodology-break-shaped anomalies (a cohort's day-over-day count more than
doubling or halving) on every run and prints a warning rather than silently
trusting a bad month — check the Actions log if a run's numbers look off.

**One-time setup**, not automated by the workflow:
- Enable GitHub Pages: repo Settings → Pages → Source: Deploy from a branch
  → `main` / `/docs`. Once enabled, `docs/index.html` is served at
  `https://<owner>.github.io/uswds-usage/`.
- To trigger a run manually instead of waiting for the 1st: `gh workflow run
  monthly-report.yml`, or use the "Run workflow" button on the Actions tab.

A hand-published version of this same report also exists as a Claude
artifact — that one isn't wired into this pipeline (there's no API to
publish to it from CI) and has to be refreshed by asking Claude to update it
from the latest `docs/index.html`.

## Running it

```bash
go run ./cmd/uswds-usage report
```

(bare `go run ./cmd/uswds-usage` with no subcommand, or with only flags, is a shorthand for
`report` and keeps working the same way it always has.)

This downloads both datasets, writes `uswds-traffic-report.csv` in the
current directory, prints a summary to the console (overall totals, top
sites by pageviews, agency/subagency counts, most common USWDS classes and
elements in use), and archives a dated snapshot of the report under
`data/snapshots/<YYYY-MM-DD>/` for historical/trend tracking (see below).

### `report` flags

| Flag                | Default                     | Description                                              |
|---------------------|------------------------------|------------------------------------------------------------|
| `-output`           | `uswds-traffic-report.csv`  | Path to write the CSV report to                          |
| `-top`              | `20`                        | Number of top sites (by pageviews) to print to the console |
| `-top-classes`      | `25`                        | Number of most common USWDS classes to print to the console |
| `-top-elements`     | `25`                        | Number of most common USWDS custom elements to print to the console |
| `-archive-dir`      | `data/snapshots`            | Directory to write the dated historical snapshot to      |
| `-no-archive`       | `false`                     | Skip writing to the historical archive                   |
| `-snapshot-date`    | today, UTC                  | Override the archive snapshot date (`YYYY-MM-DD`)         |
| `-top-n-coverage`   | `500`                       | Size of the top-by-traffic `.gov` cohort used for the traffic-weighted coverage SLI |

Example:

```bash
go run ./cmd/uswds-usage report -output report.csv -top 100 -top-classes 100
```

You can also build a binary and run it directly:

```bash
go build -o uswds-usage ./cmd/uswds-usage
./uswds-usage
```

## What's in the CSV report

One row per USWDS-using domain (deduplicated — a domain can appear multiple
times in the raw site-scanning data if several agencies/bureaus share it):

`domain, agency, uswds_semantic_version, uses_classes, uses_elements, pageviews, visits`

Rows are sorted descending by pageviews. Domains with no matching traffic
data get `0` for pageviews/visits.

## Historical archive and trend metrics

Every `report` run (unless `-no-archive` is passed) also writes a dated
snapshot under `data/snapshots/<YYYY-MM-DD>/`: the same report data plus
`source_scan_date`/`report_run_at` timestamps (`uswds-traffic-report.csv`,
9-column archive schema) and a `meta.json` with source provenance (upstream
URLs, `Last-Modified`/`ETag`, GSA's full column list at fetch time, and the
top-N traffic-weighted coverage SLI). `data/snapshots/` is tracked in git — it's
small, diffable, and is the historical record adoption-trend reporting is
built from.

### `backfill` — importing prior exports

If you have older report CSV exports (from before this archive existed),
fold them in:

```bash
go run ./cmd/uswds-usage backfill -date=2024-03-15 -file=/path/to/old-export.csv
```

`-date` is required and is never inferred from the file's mtime — supply the
date you know the export is actually from. Both the current 7-column schema
and the older 5-column schema (`domain, agency, uswds_semantic_version,
pageviews, visits`, from before the classes/elements split) are recognized;
an unrecognized header is a hard error rather than a guess. Pass `-force` to
overwrite an existing snapshot for that date.

### `trend` — computing adoption metrics over time

```bash
go run ./cmd/uswds-usage trend
```

Reads every snapshot in the archive and prints, per date: adopting domain
count, traffic-weighted top-N coverage, version-currency (with its reporting-
coverage caveat — a large share of domains report no USWDS version at all),
class-only/element-only/both counts, and total pageviews, plus the change
between the first and last snapshot. Pass `-output=trend.csv` to also write
the computed metrics as CSV.

## `data/external/gsa-uswds-report-history.csv`

GSA's own [site-scanning-analysis](https://github.com/GSA/site-scanning-analysis)
repo commits an updated `reports/uswds.csv` roughly daily. That report is a
different shape from this tool's per-domain output — it's a cohort-level
summary (by branch: All/Executive/Legislative/Judicial/IDEA, and by scan
stage: scanned/live/filtered/non-redirecting), with counts of sites on
v1.x/v2.x/v3.x, sites showing the USWDS banner, and sites using `usa-*`
classes, each broken out by distinct-agency count too.

`data/external/gsa-uswds-report-history.csv` is that report's full commit
history (2026-01-27 through 2026-09-04, ~208 daily snapshots) flattened into
one long-format CSV (`report_date, commit, group, count, agencies,
semantic_version, agencies_sv, v1_x, agencies_v1, v2_x, agencies_v2, v3_x,
agencies_v3, banner, agencies_banner, usa_class, agencies_usa_class`),
extracted via `git log --follow` + `git show` against that repo.

**Two known data-quality issues, before trending anything from this file:**
- A commit titled `Fix USWDS report` landed on **2026-03-25** and changed how
  the "filtered"/"filtered, non-redirecting" cohorts are computed — their
  site counts jump roughly 10x overnight. Don't compare data from before and
  after that date for those cohorts; the "All sites scanned" and "All live
  sites" cohorts are unaffected and stay comparable across the full range.
- **2026-06-26** is a single broken scan day (`All live, filtered,
  non-redirecting sites` briefly reads ~340 instead of ~8,800) — drop that
  date rather than trend through it.

This long-format file doesn't fit this tool's per-domain `backfill` schema
detection (it's cohort-aggregate, not one-row-per-domain) — it's kept as a
reference dataset for direct analysis (spreadsheet/pandas/etc.), not
something `uswds-usage trend` reads today.

## `data/external/httparchive-uswds-origins.csv`

Monthly counts of USWDS-detected origins against HTTP Archive's total crawl
universe (`DateTime,ALL,USWDS`), 2020-01 through 2026-07 — an independent,
web-wide corroboration of the GSA-based numbers above (not limited to
`.gov`). Two data-quality quirks to know before trending it:

- USWDS only starts appearing in HTTP Archive's technology detection in
  **2021-10** (84 origins), then jumps 5x to 392 the next month — a
  detection-signature rollout, not real adoption. Start comparisons at
  **2022-01** once that ramp settles.
- HTTP Archive's total crawl universe (`ALL`) jumped **+41%** between
  2022-06 and 2022-08 (a known crawl-methodology expansion). Use the
  USWDS/ALL *share*, not the raw `USWDS` count, to compare across that
  boundary.

## `data/external/httparchive-uswds-good-cwv.csv`

Same shape (`DateTime,ALL,USWDS`), but the values are the monthly % of page
loads rated "Good" on Core Web Vitals (HTTP Archive joined against the
Chrome UX Report), USWDS sites vs. the web average. USWDS has led the web
average every year since tracking began, and the gap has widened each year:
+2.9pt (2022) → +5.2pt (2023) → +8.9pt (2024) → +9.2pt (2025) → +10.1pt
(2026 YTD). Both series dip in **2024-03** — that's real and industry-wide
(Google replaced First Input Delay with Interaction to Next Paint as the
third Core Web Vital that month), not a data error; both recover over the
following months and the USWDS-vs-web gap is unaffected.

## `data/external/httparchive-uswds-accessibility.csv`

Same shape (`DateTime,ALL,USWDS`), monthly **median** Lighthouse
accessibility score (0–100) — HTTP Archive publishes these as medians, not
means — USWDS sites vs. the web median. No known data-quality break here
— unlike the other two HTTP Archive files, the gap has held between +11 and
+15 points in every single one of the 55 months measured (Jan 2022–Jul
2026); USWDS has stayed in the mid-90s to high-90s the whole time (near the
practical ceiling for automated accessibility auditing) while the web
median has only slowly climbed from ~82 to ~86. Worth noting when a fresh
export is dropped in: this is the one series where you'd actually notice if
something changed, since it's been this stable for 4.5 years.

## Source files

All CLI/report-generator code lives in one package, `internal/app`; `cmd/uswds-usage` is a thin wrapper that calls into it.

| File                            | Responsibility                                                                 |
|----------------------------------|----------------------------------------------------------------------------------|
| `cmd/uswds-usage/main.go`        | Binary entrypoint; calls `app.Run()`                                             |
| `internal/app/run.go`            | Subcommand dispatch (`report`/`backfill`/`trend`/`serve`/`build-report`/`refresh-history`) and the report flow |
| `internal/app/fetch.go`          | Shared HTTP + CSV streaming helper (`fetchCSVReader`) used by both data sources    |
| `internal/app/sitescanning.go`   | Downloads and parses the GSA site-scanning CSV into `SiteScanRecord`s              |
| `internal/app/analytics.go`      | Downloads and parses the analytics.usa.gov CSV into `AnalyticsRecord`s             |
| `internal/app/report.go`         | Dedupes/joins the two datasets into `ReportRow`s, computes stats, writes the CSV, and prints console summaries |
| `internal/app/snapshot.go`       | Historical archive schema and writer (`data/snapshots/<date>/uswds-traffic-report.csv` + `meta.json`) |
| `internal/app/backfill.go`       | `backfill` subcommand — ingests prior report exports into the archive             |
| `internal/app/trend.go`          | `trend` subcommand — loads the archive and computes adoption/health metrics       |
| `internal/app/gsahistory.go`     | Shared schema for `data/external/gsa-uswds-report-history.csv`                    |
| `internal/app/refreshhistory.go` | `refresh-history` subcommand — pulls new commits from GSA's site-scanning-analysis repo |
| `internal/app/buildreport.go`    | `build-report` subcommand — recomputes KPIs/charts and writes `docs/index.html`   |
| `internal/app/jsonenc.go`        | Python-`json.dumps`-compatible encoder for `build-report`'s embedded chart data   |
