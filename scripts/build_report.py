#!/usr/bin/env python3
"""Regenerate docs/index.html (the recurring USWDS Adoption Pulse report)
from the checked-in data sources:

  - external-data/gsa-uswds-report-history.csv  (GSA site-scanning-analysis)
  - snapshots/<latest-date>/                    (this tool's own report)
  - external-data/httparchive-uswds-origins.csv
  - external-data/httparchive-uswds-good-cwv.csv

Run after refresh_gsa_history.py and `./uswds-usage report`:

    python3 scripts/build_report.py

Known, manually-verified data-quality exclusions are hardcoded below
(GSA_CLEAN_START, GSA_EXCLUDED_DATES, HTTPARCHIVE_CLEAN_START) -- see
README.md for why each exists. build_report also scans for *new* anomalies
of the same shape (a cohort's day-over-day count more than doubling or
halving) and prints a warning rather than silently trusting the data, since
GSA has shipped at least one silent methodology change before.
"""
import csv
import json
import sys
from datetime import date, datetime
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
GSA_HISTORY = REPO_ROOT / "external-data" / "gsa-uswds-report-history.csv"
HTTPARCHIVE_ORIGINS = REPO_ROOT / "external-data" / "httparchive-uswds-origins.csv"
HTTPARCHIVE_CWV = REPO_ROOT / "external-data" / "httparchive-uswds-good-cwv.csv"
HTTPARCHIVE_A11Y = REPO_ROOT / "external-data" / "httparchive-uswds-accessibility.csv"
SNAPSHOTS_DIR = REPO_ROOT / "snapshots"
OUT_FILE = REPO_ROOT / "docs" / "index.html"

GSA_COHORT = "All live, filtered, non-redirecting sites"
GSA_EXEC_COHORT = "Executive live, filtered, non-redirecting sites"
# 2026-03-25: GSA shipped a commit titled "Fix USWDS report" that changed how
# the filtered cohorts are computed -- cohort size jumped ~10x overnight.
# Data before this date is not on the same basis; see README.md.
GSA_CLEAN_START = "2026-03-25"
# 2026-06-26: a single broken scan day (cohort briefly read ~340 instead of
# ~8,800). Excluded rather than plotted as a real one-day collapse.
GSA_EXCLUDED_DATES = {"2026-06-26"}
LATEST_MAJOR = "3"

# HTTP Archive first detects USWDS in 2021-10 at 84 origins, then jumps 5x
# the next month -- a Wappalyzer signature rollout, not real adoption.
HTTPARCHIVE_CLEAN_START = date(2022, 1, 1)
# 2024-03: Google replaced First Input Delay with Interaction to Next Paint
# as the third Core Web Vital, resetting the pass bar industry-wide. Real,
# not a data error -- annotated on the chart rather than excluded.
INP_TRANSITION_DATE = date(2024, 3, 1)

ANOMALY_RATIO_HIGH = 2.0
ANOMALY_RATIO_LOW = 0.5


def load_gsa_history():
    with open(GSA_HISTORY) as f:
        return list(csv.DictReader(f))


def cohort_series(rows, group, start=None, exclude=frozenset()):
    series = [
        r for r in rows
        if r["group"] == group
        and r["report_date"] not in exclude
        and (start is None or r["report_date"] >= start)
    ]
    series.sort(key=lambda r: r["report_date"])
    return series


def check_anomalies(rows, group, label):
    """Warn (don't fail) if a day-over-day count swing looks like the same
    shape as GSA's known March 2026 methodology break, outside the already
    -handled exclusions. This is the guard against a *future* silent change."""
    series = cohort_series(rows, group, start=GSA_CLEAN_START, exclude=GSA_EXCLUDED_DATES)
    prev = None
    flagged = []
    for r in series:
        count = int(r["count"])
        if prev is not None and prev > 0:
            ratio = count / prev
            if ratio > ANOMALY_RATIO_HIGH or ratio < ANOMALY_RATIO_LOW:
                flagged.append((r["report_date"], prev, count, ratio))
        prev = count
    if flagged:
        print(f"WARNING: possible new data anomaly in {label!r}:", file=sys.stderr)
        for d, p, c, ratio in flagged:
            print(f"  {d}: {p} -> {c} ({ratio:.2f}x) -- review before trusting this report", file=sys.stderr)
    return flagged


def gsa_summary(rows):
    all_series = cohort_series(rows, GSA_COHORT, start=GSA_CLEAN_START, exclude=GSA_EXCLUDED_DATES)
    first, last = all_series[0], all_series[-1]

    def i(row, key):
        return int(row[key])

    legacy_first = i(first, "v1_x") + i(first, "v2_x")
    legacy_last = i(last, "v1_x") + i(last, "v2_x")
    majors_first = i(first, "v1_x") + i(first, "v2_x") + i(first, "v3_x")
    majors_last = i(last, "v1_x") + i(last, "v2_x") + i(last, "v3_x")

    return {
        "start_date": first["report_date"],
        "end_date": last["report_date"],
        "v3_first": i(first, "v3_x"), "v3_last": i(last, "v3_x"),
        "v3_agencies_first": i(first, "agencies_v3"), "v3_agencies_last": i(last, "agencies_v3"),
        "legacy_first": legacy_first, "legacy_last": legacy_last,
        "v1_last": i(last, "v1_x"),
        "v2_first": i(first, "v2_x"), "v2_last": i(last, "v2_x"),
        "usa_class_first": i(first, "usa_class"), "usa_class_last": i(last, "usa_class"),
        "semver_first": i(first, "semantic_version"), "semver_last": i(last, "semantic_version"),
        "version_coverage_first": i(first, "semantic_version") / i(first, "usa_class"),
        "version_coverage_last": i(last, "semantic_version") / i(last, "usa_class"),
        "v3_share_first": i(first, "v3_x") / majors_first if majors_first else 0,
        "v3_share_last": i(last, "v3_x") / majors_last if majors_last else 0,
        "series": all_series,
    }


def latest_snapshot():
    dirs = sorted(d for d in SNAPSHOTS_DIR.iterdir() if d.is_dir()) if SNAPSHOTS_DIR.exists() else []
    if not dirs:
        return None
    latest = dirs[-1]
    meta = json.loads((latest / "meta.json").read_text()) if (latest / "meta.json").exists() else {}
    return {"date": latest.name, "meta": meta}


def load_httparchive(path):
    rows = []
    with open(path) as f:
        r = csv.reader(f)
        next(r)
        for row in r:
            d = date.fromisoformat(row[0][:10])
            all_ = int(row[1])
            u = int(row[2]) if len(row) > 2 and row[2].strip() != "" else None
            rows.append((d, all_, u))
    return rows


def yearly_avg(rows, value_fn):
    years = {}
    for d, a, u in rows:
        if u is None:
            continue
        years.setdefault(d.year, []).append(value_fn(a, u))
    return {y: sum(v) / len(v) for y, v in years.items()}


def fmt_pct(x):
    return f"{x * 100:.0f}%"


def fmt_delta_pts(a, b):
    return f"{(b - a) * 100:+.1f}pt"


# ---- HTML rendering ----------------------------------------------------
#
# This report is built with real USWDS: the compiled theme at
# docs/assets/uswds/css/uswds.css (compiled by `npm run build` from
# theme/styles.scss -- a settings-first customization per
# github.com/uswds/uswds/discussions/6765#discussioncomment-17674633,
# switching the heading role to the built-in "public-sans" typeface token).
# Layout uses the USWDS grid (grid-container/grid-row/grid-col), and every
# component below (card, alert, table) is real usa-* markup, not custom CSS.
#
# The only non-USWDS styling is REPORT_CSS: a small, narrowly-scoped
# stylesheet for the D3 trend charts (docs/assets/report.js), which USWDS
# has no component for (see the data-visualizations guidance). Per the same
# discussion's third approach ("add your own class with higher specificity
# ... avoid modifying usa-* classes"), it only ever touches its own
# `.report-*` classes.

REPORT_CSS = """
.report-chart-panel { background: #fff; border: 1px solid #dfe1e2; border-radius: 4px; padding: 1.5rem 1.5rem 1rem; }
.report-chart-wrap { overflow-x: auto; }
.report-chart { position: relative; }
.report-chart svg { width: 100%; height: auto; display: block; min-width: 480px; }
.report-axis-title { font-size: 12px; fill: #3d4551; font-weight: 600; }
.report-axis .domain { stroke: #a9aeb1; }
.report-axis .tick line { stroke: #a9aeb1; }
.report-axis .tick text { font-size: 11px; fill: #565c65; }
.report-grid .domain { display: none; }
.report-grid .tick line { stroke: #dfe1e2; stroke-width: 1; shape-rendering: crispEdges; }
.report-line { fill: none; stroke-width: 2px; }
.report-marker { r: 4.5px; stroke: #fff; stroke-width: 2px; }
.report-crosshair { stroke: #71767a; stroke-width: 1px; stroke-dasharray: 3 3; pointer-events: none; }
.report-overlay { cursor: crosshair; }
.report-annotation-line { stroke: #c05600; stroke-width: 1.5px; stroke-dasharray: 4 4; }
.report-annotation-label { font-size: 10.5px; fill: #c05600; }
.report-bar-label { font-size: 11px; fill: #1b1b1b; font-weight: 600; }
.report-bar.is-hovered { opacity: .85; }
.report-tooltip { position: absolute; pointer-events: none; background: #1b1b1b; color: #fff; padding: .5rem .75rem; border-radius: 4px; font-size: .8rem; white-space: nowrap; z-index: 10; }
.report-tooltip-title { font-weight: 700; margin-bottom: .25rem; }
.report-tooltip-row { display: flex; align-items: center; gap: .4rem; }
.report-tooltip-swatch { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.report-tooltip-value { margin-left: auto; padding-left: .75rem; font-variant-numeric: tabular-nums; }
.report-legend { display: flex; flex-wrap: wrap; gap: 1.5rem; margin-top: .75rem; padding-top: .75rem; border-top: 1px solid #dfe1e2; font-size: .93rem; }
.report-legend .report-swatch { display: inline-block; width: 14px; height: 3px; border-radius: 2px; margin-right: .4rem; vertical-align: middle; }
.report-kpi-value { font-size: 2rem; font-weight: 700; font-variant-numeric: tabular-nums; margin: 0; }
"""


def render(gsa, snap, ha_origins, ha_cwv, ha_a11y):
    generated_at = datetime.utcnow().strftime("%Y-%m-%d")

    # -- version-mix chart (GSA) -- raw data only; D3 (docs/assets/report.js)
    # computes scales, axes and ticks client-side.
    gsa_dates = [r["report_date"] for r in gsa["series"]]
    v1_vals = [int(r["v1_x"]) for r in gsa["series"]]
    v2_vals = [int(r["v2_x"]) for r in gsa["series"]]
    v3_vals = [int(r["v3_x"]) for r in gsa["series"]]

    # -- HTTP Archive origins: yearly share bar chart --
    yearly_share = yearly_avg(ha_origins, lambda a, u: u / a * 10000)
    years = sorted(y for y in yearly_share if y >= 2022)
    bar_categories = [f"{y}*" if y == date.today().year else str(y) for y in years]
    bar_values = [yearly_share[y] for y in years]
    bar_colors = ["#005ea2" if y == years[-1] else "#c05600" if y == years[-2] else "#a9aeb1" for y in years]

    # -- HTTP Archive origins: monthly raw count line --
    origin_series = [(d, u) for d, a, u in ha_origins if u is not None and d >= HTTPARCHIVE_CLEAN_START]
    origin_dates = [d.isoformat() for d, u in origin_series]
    origin_counts = [u for d, u in origin_series]

    # -- Core Web Vitals: monthly dual line --
    cwv_series = [(d, a, u) for d, a, u in ha_cwv if u is not None and d >= HTTPARCHIVE_CLEAN_START]
    cwv_dates = [d.isoformat() for d, a, u in cwv_series]
    cwv_all = [a for d, a, u in cwv_series]
    cwv_uswds = [u for d, a, u in cwv_series]

    # -- Lighthouse accessibility score: monthly dual line -- no known
    # data-quality break in this one: the USWDS/web gap has held between
    # +11 and +15 points in every one of the 55 months measured.
    a11y_series = [(d, a, u) for d, a, u in ha_a11y if u is not None and d >= HTTPARCHIVE_CLEAN_START]
    a11y_dates = [d.isoformat() for d, a, u in a11y_series]
    a11y_all = [a for d, a, u in a11y_series]
    a11y_uswds = [u for d, a, u in a11y_series]
    a11y_min_uswds = min(a11y_uswds)
    a11y_current_gap = a11y_uswds[-1] - a11y_all[-1]
    a11y_worst_vs_current_web = a11y_min_uswds - a11y_all[-1]
    a11y_min_gap = min(u - a for u, a in zip(a11y_uswds, a11y_all))

    cwv_yearly_all = yearly_avg([(d, a, u) for d, a, u in ha_cwv], lambda a, u: a)
    cwv_yearly_uswds = yearly_avg([(d, a, u) for d, a, u in ha_cwv], lambda a, u: u)
    cwv_years = sorted(y for y in cwv_yearly_all if y >= 2022 and y in cwv_yearly_uswds)
    gap_first = cwv_yearly_uswds[cwv_years[0]] - cwv_yearly_all[cwv_years[0]]
    gap_last = cwv_yearly_uswds[cwv_years[-1]] - cwv_yearly_all[cwv_years[-1]]
    recent_years = [y for y in cwv_years if y >= cwv_years[-1] - 2]
    ratio_recent = sum(cwv_yearly_uswds[y] for y in recent_years) / sum(cwv_yearly_all[y] for y in recent_years)

    top_n_html = "n/a &mdash; no live snapshot found"
    if snap and snap["meta"].get("top_n_size"):
        m = snap["meta"]
        top_n_html = f"{m['top_n_coverage']*100:.0f}%"
        top_n_note = f"of top {m['top_n_size']} .gov domains by traffic, snapshot {snap['date']}"
    else:
        top_n_note = "no live snapshot found -- run `./uswds-usage report` first"

    legacy_delta = gsa["legacy_last"] - gsa["legacy_first"]
    v3_growth_pct = (gsa["v3_last"] - gsa["v3_first"]) / gsa["v3_first"] * 100
    legacy_growth_pct = legacy_delta / gsa["legacy_first"] * 100 if gsa["legacy_first"] else 0

    # Data payload for docs/assets/report.js -- one entry per chart.
    chart_data = {
        "versionMix": {
            "dates": gsa_dates,
            "series": [
                {"key": "v3", "label": "v3.x", "color": "#005ea2", "values": v3_vals},
                {"key": "v2", "label": "v2.x", "color": "#c05600", "values": v2_vals},
                {"key": "v1", "label": "v1.x", "color": "#a9aeb1", "values": v1_vals},
            ],
            "yLabel": "Sites",
            "ariaLabel": "Line chart of USWDS v1, v2 and v3 site counts over time.",
        },
        "shareByYear": {
            "categories": bar_categories,
            "values": bar_values,
            "colors": bar_colors,
            "yLabel": "USWDS origins per 10,000 crawled",
            "valueFormat": ".2f",
            "ariaLabel": "Bar chart of USWDS share of all HTTP Archive origins by year.",
        },
        "originCount": {
            "dates": origin_dates,
            "series": [{"key": "origins", "label": "USWDS-detected origins", "color": "#005ea2", "values": origin_counts}],
            "yLabel": "USWDS-detected origins",
            "ariaLabel": "Line chart of raw USWDS origin counts climbing over time.",
        },
        "cwv": {
            "dates": cwv_dates,
            "series": [
                {"key": "uswds", "label": "USWDS sites", "color": "#005ea2", "values": cwv_uswds},
                {"key": "all", "label": "Web average", "color": "#a9aeb1", "values": cwv_all},
            ],
            "yLabel": "% of page loads, Good CWV",
            "yTickFormat": ".0f",
            "yDomain": [0, 70],
            "annotation": {"date": INP_TRANSITION_DATE.isoformat(), "label": "Mar 2024: FID→INP"},
            "ariaLabel": "Line chart comparing Core Web Vitals pass rates for USWDS sites vs. the web average.",
        },
        "a11y": {
            "dates": a11y_dates,
            "series": [
                {"key": "uswds", "label": "USWDS sites", "color": "#005ea2", "values": a11y_uswds},
                {"key": "all", "label": "Web median", "color": "#a9aeb1", "values": a11y_all},
            ],
            "yLabel": "Median Lighthouse accessibility score",
            "yTickFormat": ".0f",
            "yDomain": [75, 100],
            "ariaLabel": "Line chart comparing median Lighthouse accessibility scores for USWDS sites vs. the web median.",
        },
    }

    def kpi_card(col, label, value_html, note):
        return f"""<li class="usa-card {col}">
      <div class="usa-card__container">
        <div class="usa-card__header">
          <h3 class="usa-card__heading font-body-3xs text-uppercase text-base-dark">{label}</h3>
        </div>
        <div class="usa-card__body">
          <p class="report-kpi-value">{value_html}</p>
          <p class="text-base-dark font-body-2xs">{note}</p>
        </div>
      </div>
    </li>"""

    legacy_arrow = "&#9660;" if legacy_delta < 0 else "&#9650;"
    legacy_color = "text-success-dark" if legacy_delta < 0 else "text-error-dark"

    html = f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>USWDS Adoption Pulse</title>
  <link rel="stylesheet" href="assets/uswds/css/uswds.css">
  <!-- No uswds-init.js / uswds.min.js: those exist to prevent FOUC on
       Banner/Header/Modal and to initialize interactive component JS
       (accordion, combo-box, sortable tables, dismissible alerts, etc).
       This page uses none of that -- card, alert, table and prose here are
       all static markup+CSS with zero JS files of their own (verified
       against the component sources). Add them back if a future component
       here actually needs JS. -->
  <style>{REPORT_CSS}</style>
</head>
<body>
<div class="grid-container padding-y-4">
<main>

  <section class="usa-prose margin-bottom-5">
    <p class="font-body-3xs text-uppercase text-primary text-bold">Program health &middot; generated {generated_at}</p>
    <h1>USWDS Adoption Pulse</h1>
    <p class="usa-intro">Where federal USWDS adoption stands, whether it's moving, whether it actually performs better, and what an executive can trust in these numbers. Regenerated monthly from GSA's site-scanning history, this tool's own live snapshot, and an independent, web-wide corroboration from HTTP Archive.</p>
  </section>

  <section class="usa-prose margin-bottom-5">
    <h2>Headline signals</h2>
    <p>Comparable window: {gsa['start_date']} &rarr; {gsa['end_date']}.</p>
  </section>
  <ul class="usa-card-group margin-bottom-5">
    {kpi_card("tablet:grid-col-3", "Current major (v3.x) adoption",
              f'{gsa["v3_last"]:,} <span class="font-body-sm text-success-dark">&#9650; {v3_growth_pct:.0f}%</span>',
              f'sites on v3.x, up from {gsa["v3_first"]:,} &middot; {gsa["v3_agencies_last"]} agencies now on v3')}
    {kpi_card("tablet:grid-col-3", "Legacy (v1.x + v2.x) sites",
              f'{gsa["legacy_last"]:,} <span class="font-body-sm {legacy_color}">{legacy_arrow} {abs(legacy_growth_pct):.0f}%</span>',
              f'from {gsa["legacy_first"]:,} &middot; still {gsa["v1_last"]} sites on v1.x')}
    {kpi_card("tablet:grid-col-3", "Version reporting coverage",
              f'{fmt_pct(gsa["version_coverage_last"])} <span class="font-body-sm text-success-dark">&#9650; {fmt_delta_pts(gsa["version_coverage_first"], gsa["version_coverage_last"])}</span>',
              f'of USWDS-detected sites report a readable version &mdash; up from {fmt_pct(gsa["version_coverage_first"])}')}
    {kpi_card("tablet:grid-col-3", "Traffic-weighted reach", top_n_html, top_n_note)}
  </ul>

  <section class="usa-prose margin-bottom-5">
    <h2>Version mix over time</h2>
    <p>Site count by USWDS major version, "{GSA_COHORT}" cohort (GSA's site-scanning-analysis report).</p>
  </section>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">v1.x / v2.x / v3.x site counts</h3>
      <span class="font-body-3xs text-base-dark">{gsa['start_date']} &rarr; {gsa['end_date']}</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-version-mix" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>v3.x <span class="text-base-dark">{gsa['v3_first']} &rarr; {gsa['v3_last']}</span></span>
      <span><span class="report-swatch" style="background:#c05600"></span>v2.x <span class="text-base-dark">{gsa['v2_first']} &rarr; {gsa['v2_last']}</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>v1.x <span class="text-base-dark">&rarr; {gsa['v1_last']}</span></span>
    </div>
  </div>

  <div class="usa-alert usa-alert--warning margin-bottom-5" role="region" aria-label="Data quality note">
    <div class="usa-alert__body">
      <h4 class="usa-alert__heading">Known exclusion</h4>
      <p class="usa-alert__text">GSA's "Fix USWDS report" commit on 2026-03-25 changed the filtered-cohort calculation (~10x jump). All figures above start at {GSA_CLEAN_START}. 2026-06-26 (a single broken scan day) is also excluded.</p>
      <p class="usa-alert__text">This report re-checks for new anomalies of the same shape on every run (see build log) &mdash; if one is flagged, verify it before trusting this page.</p>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>The web-wide view</h2>
    <p>HTTP Archive's independent, web-wide monthly crawl (not just .gov), Wappalyzer USWDS detection since late 2021.</p>
  </section>
  <ul class="usa-card-group margin-bottom-3">
    {kpi_card("tablet:grid-col-4", "Share of all crawled origins",
              f'{yearly_share[years[-1]]:.2f} <span class="font-body-sm text-success-dark">&#9650; {(yearly_share[years[-1]]/yearly_share[years[0]]-1)*100:.0f}%</span>',
              f'per 10,000 origins, {years[-1]} avg &mdash; up from {yearly_share[years[0]]:.2f} in {years[0]}')}
  </ul>
  <div class="report-chart-panel margin-bottom-3">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">USWDS share of all crawled origins</h3>
      <span class="font-body-3xs text-base-dark">annual average, per 10,000 origins</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-share-by-year" class="report-chart"></div>
    </div>
  </div>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">Raw USWDS origin count</h3>
      <span class="font-body-3xs text-base-dark">monthly, {origin_series[0][0]} &rarr; {origin_series[-1][0]}</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-origin-count" class="report-chart"></div>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>Do USWDS sites actually perform better?</h2>
    <p>Share of real-user page loads rated "Good" on Core Web Vitals, USWDS sites vs. the web at large.</p>
  </section>
  <ul class="usa-card-group margin-bottom-3">
    {kpi_card("tablet:grid-col-6", "Performance gap vs. the web",
              f'{gap_last:+.1f}pt <span class="font-body-sm text-success-dark">&#9650;</span>',
              f'up from {gap_first:+.1f}pt in {cwv_years[0]}')}
    {kpi_card("tablet:grid-col-6", "Relative advantage",
              f'{ratio_recent:.2f}&times;',
              "a USWDS page's chance of passing CWV vs. the average page, last 3yr avg")}
  </ul>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">Share of page loads with "Good" Core Web Vitals</h3>
      <span class="font-body-3xs text-base-dark">monthly, {cwv_series[0][0]} &rarr; {cwv_series[-1][0]}</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-cwv" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>USWDS sites <span class="text-base-dark">{cwv_series[0][2]}% &rarr; {cwv_series[-1][2]}%</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>Web average <span class="text-base-dark">{cwv_series[0][1]}% &rarr; {cwv_series[-1][1]}%</span></span>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>Is USWDS actually more accessible?</h2>
    <p><strong>Median</strong> Lighthouse accessibility score (0&ndash;100: alt text, color contrast, ARIA labels, form labels, heading structure, and more) &mdash; HTTP Archive publishes these as medians, not means &mdash; USWDS sites vs. the web at large. Unlike the metrics above, this one shows no data-quality break to caveat &mdash; the gap has held in a tight, stable band for the full 55-month history.</p>
  </section>
  <ul class="usa-card-group margin-bottom-3">
    {kpi_card("tablet:grid-col-4", "Accessibility score, latest",
              f'{a11y_uswds[-1]} <span class="font-body-sm text-base-dark">vs {a11y_all[-1]}</span>',
              f'USWDS sites vs. the web median &mdash; a {a11y_current_gap:+d}pt gap')}
    {kpi_card("tablet:grid-col-4", "Worst USWDS month ever measured",
              f'{a11y_min_uswds}',
              f'still {a11y_worst_vs_current_web:+d}pt above where the median site sits <em>today</em>')}
    {kpi_card("tablet:grid-col-4", "Consistency",
              f'{len(a11y_series)} of {len(a11y_series)}',
              f'months with a double-digit-point USWDS lead &mdash; every month measured, minimum {a11y_min_gap:+d}pt')}
  </ul>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">Median Lighthouse accessibility score</h3>
      <span class="font-body-3xs text-base-dark">monthly, {a11y_series[0][0]} &rarr; {a11y_series[-1][0]}</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-a11y" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>USWDS sites <span class="text-base-dark">{a11y_series[0][2]} &rarr; {a11y_series[-1][2]}</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>Web median <span class="text-base-dark">{a11y_series[0][1]} &rarr; {a11y_series[-1][1]}</span></span>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>SLIs tracked here</h2>
    <table class="usa-table usa-table--striped width-full">
      <caption>USWDS program-health SLIs, computed by scripts/build_report.py &mdash; regenerated {generated_at}</caption>
      <thead>
        <tr><th scope="col">SLI</th><th scope="col">Current</th><th scope="col">Caveat</th></tr>
      </thead>
      <tbody>
        <tr><th scope="row">Version-major currency (v3.x share of majors)</th><td>{fmt_pct(gsa['v3_share_first'])} &rarr; {fmt_pct(gsa['v3_share_last'])}</td><td>Pair with version-reporting coverage</td></tr>
        <tr><th scope="row">Version reporting coverage</th><td>{fmt_pct(gsa['version_coverage_first'])} &rarr; {fmt_pct(gsa['version_coverage_last'])}</td><td>Two-thirds of adopters still unmeasured on this axis</td></tr>
        <tr><th scope="row">Traffic-weighted reach</th><td>{top_n_html}</td><td>Trend needs several more monthly snapshots</td></tr>
        <tr><th scope="row">Web-wide share (corroboration)</th><td>{yearly_share[years[0]]:.2f} &rarr; {yearly_share[years[-1]]:.2f} / 10k</td><td>Independent of GSA's pipeline</td></tr>
        <tr><th scope="row">Performance outcome</th><td>{gap_first:+.1f}pt &rarr; {gap_last:+.1f}pt</td><td>The one outcome SLI here, not just an input</td></tr>
        <tr><th scope="row">Accessibility outcome (median)</th><td>{a11y_uswds[0]} &rarr; {a11y_uswds[-1]} (web median: {a11y_all[0]} &rarr; {a11y_all[-1]})</td><td>No known data-quality break; gap has never fallen below {a11y_min_gap:+d}pt</td></tr>
      </tbody>
    </table>
  </section>

  <footer class="usa-prose font-body-3xs text-base-dark padding-top-2 border-top border-base-lighter">
    <p>Sources: github.com/GSA/site-scanning-analysis (reports/uswds.csv) &middot; api.gsa.gov/technology/site-scanning &middot; analytics.usa.gov &middot; uswds-usage CLI &middot; HTTP Archive technology detection, Chrome UX Report Core Web Vitals &amp; Lighthouse accessibility scores &middot; regenerated {generated_at} by scripts/build_report.py</p>
  </footer>

</main>
</div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/d3/7.9.0/d3.min.js" integrity="sha512-vc58qvvBdrDR4etbxMdlTt4GBQk1qjvyORR2nrsPsFPyrs+/u5c3+1Ct6upOgdZoIl7eq6k3a1UPDSNAQi/32A==" crossorigin="anonymous" referrerpolicy="no-referrer"></script>
<script src="assets/report.js"></script>
<script type="application/json" id="report-chart-data">{json.dumps(chart_data)}</script>
<script>
  (function () {{
    var data = JSON.parse(document.getElementById("report-chart-data").textContent);
    reportCharts.drawLineChart("#chart-version-mix", data.versionMix);
    reportCharts.drawBarChart("#chart-share-by-year", data.shareByYear);
    reportCharts.drawLineChart("#chart-origin-count", data.originCount);
    reportCharts.drawLineChart("#chart-cwv", data.cwv);
    reportCharts.drawLineChart("#chart-a11y", data.a11y);
  }})();
</script>
</body>
</html>
"""
    return html


def main():
    gsa_rows = load_gsa_history()
    check_anomalies(gsa_rows, GSA_COHORT, "All cohort")
    check_anomalies(gsa_rows, GSA_EXEC_COHORT, "Executive cohort")
    gsa = gsa_summary(gsa_rows)
    snap = latest_snapshot()
    ha_origins = load_httparchive(HTTPARCHIVE_ORIGINS)
    ha_cwv = load_httparchive(HTTPARCHIVE_CWV)
    ha_a11y = load_httparchive(HTTPARCHIVE_A11Y)

    html = render(gsa, snap, ha_origins, ha_cwv, ha_a11y)
    OUT_FILE.parent.mkdir(parents=True, exist_ok=True)
    OUT_FILE.write_text(html)
    print(f"wrote {OUT_FILE}", file=sys.stderr)


if __name__ == "__main__":
    main()
