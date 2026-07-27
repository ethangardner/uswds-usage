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
- Internet access (the tool fetches live data on every run; nothing is
  bundled or cached)

## Running it

```bash
go run .
```

This downloads both datasets, writes `uswds-traffic-report.csv` in the
current directory, and prints a summary to the console: overall totals, the
top sites by pageviews, agency/subagency counts, and the most common USWDS
classes in use.

### Flags

| Flag            | Default                     | Description                                              |
|-----------------|------------------------------|------------------------------------------------------------|
| `-output`       | `uswds-traffic-report.csv`  | Path to write the CSV report to                          |
| `-top`          | `20`                        | Number of top sites (by pageviews) to print to the console |
| `-top-classes`  | `25`                        | Number of most common USWDS classes to print to the console |

Example:

```bash
go run . -output report.csv -top 100 -top-classes 100
```

You can also build a binary and run it directly:

```bash
go build -o uswds-usage .
./uswds-usage
```

## What's in the CSV report

One row per USWDS-using domain (deduplicated — a domain can appear multiple
times in the raw site-scanning data if several agencies/bureaus share it):

`domain, agency, uswds_semantic_version, pageviews, visits`

Rows are sorted descending by pageviews. Domains with no matching traffic
data get `0` for pageviews/visits.

## Source files

| File               | Responsibility                                                                 |
|--------------------|----------------------------------------------------------------------------------|
| `main.go`          | Entry point; parses flags and orchestrates the fetch → build → write → print flow |
| `fetch.go`         | Shared HTTP + CSV streaming helper (`fetchCSVReader`) used by both data sources    |
| `sitescanning.go`  | Downloads and parses the GSA site-scanning CSV into `SiteScanRecord`s              |
| `analytics.go`     | Downloads and parses the analytics.usa.gov CSV into `AnalyticsRecord`s             |
| `report.go`        | Dedupes/joins the two datasets into `ReportRow`s, computes stats, writes the CSV, and prints console summaries |
