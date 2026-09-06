package app

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Data-quality exclusions and thresholds, hardcoded and manually verified --
// see README.md for why each exists. checkAnomalies additionally scans for
// *new* anomalies of the same shape on every run.
const (
	gsaCohort     = "All live, filtered, non-redirecting sites"
	gsaExecCohort = "Executive live, filtered, non-redirecting sites"
	// 2026-03-25: GSA shipped a commit titled "Fix USWDS report" that changed
	// how the filtered cohorts are computed -- cohort size jumped ~10x
	// overnight. Data before this date is not on the same basis.
	gsaCleanStart = "2026-03-25"

	anomalyRatioHigh = 2.0
	anomalyRatioLow  = 0.5
)

// gsaExcludedDates: 2026-06-26 is a single broken scan day (cohort briefly
// read ~340 instead of ~8,800). Excluded rather than plotted as a real
// one-day collapse.
var gsaExcludedDates = map[string]bool{"2026-06-26": true}

// httparchiveCleanStart: HTTP Archive first detects USWDS in 2021-10 at 84
// origins, then jumps 5x the next month -- a Wappalyzer signature rollout,
// not real adoption.
var httparchiveCleanStart = time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

// inpTransitionDate: 2024-03, Google replaced First Input Delay with
// Interaction to Next Paint as the third Core Web Vital, resetting the pass
// bar industry-wide. Real, not a data error -- annotated on the chart
// rather than excluded.
var inpTransitionDate = time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

// ---- HTML rendering ------------------------------------------------------
//
// This report is built with real USWDS: the compiled theme at
// docs/assets/uswds/css/uswds.css (compiled by `npm run build` from
// theme/styles.scss -- a settings-first customization per
// github.com/uswds/uswds/discussions/6765#discussioncomment-17674633,
// switching the heading role to the built-in "public-sans" typeface token).
// Layout uses the USWDS grid (grid-container/grid-row/grid-col), and every
// component below (card, alert, table) is real usa-* markup, not custom CSS.
//
// The only non-USWDS styling is reportCSS: a small, narrowly-scoped
// stylesheet for the D3 trend charts (docs/assets/report.js), which USWDS
// has no component for (see the data-visualizations guidance). Per the same
// discussion's third approach ("add your own class with higher specificity
// ... avoid modifying usa-* classes"), it only ever touches its own
// `.report-*` classes.
const reportCSS = `
.report-chart-panel { background: #fff; border: 1px solid #dfe1e2; border-radius: 4px; padding: 1.5rem 1.5rem 1rem; }
.report-chart-wrap { overflow-x: auto; }
/* aspect-ratio matches the svg's own width/height below width 480px, the
   svg's min-width keeps it 480 wide regardless of container, so the floor
   below keeps this box tall enough to match that too. */
.report-chart { position: relative; aspect-ratio: 920 / 300; min-height: calc(480px * 300 / 920); }
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
`

func runBuildReportCmd(args []string) error {
	fs := flag.NewFlagSet("build-report", flag.ExitOnError)
	gsaHistoryPath := fs.String("gsa-history", "data/external/gsa-uswds-report-history.csv", "path to the GSA history CSV")
	originsPath := fs.String("httparchive-origins", "data/external/httparchive-uswds-origins.csv", "path to the HTTP Archive origins CSV")
	cwvPath := fs.String("httparchive-cwv", "data/external/httparchive-uswds-good-cwv.csv", "path to the HTTP Archive Core Web Vitals CSV")
	a11yPath := fs.String("httparchive-a11y", "data/external/httparchive-uswds-accessibility.csv", "path to the HTTP Archive accessibility CSV")
	snapshotsDir := fs.String("snapshots-dir", "data/snapshots", "directory the historical snapshot archive lives in")
	outPath := fs.String("out", "docs/index.html", "path to write the generated report to")
	if err := fs.Parse(args); err != nil {
		return err
	}

	gsaRows, err := readGSAHistoryCSV(*gsaHistoryPath)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	if _, err := checkAnomalies(gsaRows, gsaCohort, "All cohort"); err != nil {
		return fmt.Errorf("build-report: %w", err)
	}
	if _, err := checkAnomalies(gsaRows, gsaExecCohort, "Executive cohort"); err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	gsa, err := gsaSummary(gsaRows)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	snapDate, snapMeta, snapOK, err := latestSnapshotMeta(*snapshotsDir)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	haOrigins, err := loadHTTPArchive(*originsPath)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}
	haCWV, err := loadHTTPArchive(*cwvPath)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}
	haA11y, err := loadHTTPArchive(*a11yPath)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	html, err := renderHTML(gsa, snapDate, snapMeta, snapOK, haOrigins, haCWV, haA11y)
	if err != nil {
		return fmt.Errorf("build-report: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return fmt.Errorf("build-report: creating %s: %w", filepath.Dir(*outPath), err)
	}
	if err := os.WriteFile(*outPath, []byte(html), 0o644); err != nil {
		return fmt.Errorf("build-report: writing %s: %w", *outPath, err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s\n", *outPath)
	return nil
}

// atoiField parses s as an int, failing loudly (never silently defaulting
// to 0) since the Python original's bare int(...) crashes the whole script
// on bad data -- a report built on a silently-zeroed bad field would be
// wrong in a way nobody would notice.
func atoiField(s, field string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("parsing %q as int (%s): %w", s, field, err)
	}
	return n, nil
}

// cohortSeries filters rows to one GSA cohort, drops excluded dates, keeps
// only dates >= start (plain string comparison -- report_date is always a
// fixed-width ISO YYYY-MM-DD string, so lexicographic comparison is safe
// and intentional, never date-parsed), and sorts ascending by date.
func cohortSeries(rows []GSAHistoryRow, group, start string, exclude map[string]bool) []GSAHistoryRow {
	var out []GSAHistoryRow
	for _, r := range rows {
		if r.Group != group {
			continue
		}
		if exclude[r.ReportDate] {
			continue
		}
		if start != "" && r.ReportDate < start {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ReportDate < out[j].ReportDate })
	return out
}

type anomalyFlag struct {
	date        string
	prev, count int
	ratio       float64
}

// checkAnomalies warns (doesn't fail) if a day-over-day count swing looks
// like the same shape as GSA's known March 2026 methodology break, outside
// the already-handled exclusions -- the guard against a *future* silent
// change. Warnings go to stderr only; this is not part of the committed
// docs/index.html and isn't byte-identical-critical.
func checkAnomalies(rows []GSAHistoryRow, group, label string) ([]anomalyFlag, error) {
	series := cohortSeries(rows, group, gsaCleanStart, gsaExcludedDates)

	var flagged []anomalyFlag
	prev := 0
	havePrev := false
	for _, r := range series {
		count, err := atoiField(r.Count, "count")
		if err != nil {
			return nil, err
		}
		if havePrev && prev > 0 {
			ratio := float64(count) / float64(prev)
			if ratio > anomalyRatioHigh || ratio < anomalyRatioLow {
				flagged = append(flagged, anomalyFlag{r.ReportDate, prev, count, ratio})
			}
		}
		prev = count
		havePrev = true
	}

	if len(flagged) > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: possible new data anomaly in '%s':\n", label)
		for _, f := range flagged {
			fmt.Fprintf(os.Stderr, "  %s: %d -> %d (%.2fx) -- review before trusting this report\n", f.date, f.prev, f.count, f.ratio)
		}
	}
	return flagged, nil
}

// GSASummary holds the headline KPIs computed from the "all" cohort's first
// and last rows in the clean-started, excluded-filtered series.
type GSASummary struct {
	StartDate, EndDate                        string
	V3First, V3Last                           int
	V3AgenciesFirst, V3AgenciesLast           int
	LegacyFirst, LegacyLast                   int
	V1Last                                    int
	V2First, V2Last                           int
	UsaClassFirst, UsaClassLast               int
	SemverFirst, SemverLast                   int
	VersionCoverageFirst, VersionCoverageLast float64
	V3ShareFirst, V3ShareLast                 float64
	Series                                    []GSAHistoryRow
}

func gsaSummary(rows []GSAHistoryRow) (GSASummary, error) {
	allSeries := cohortSeries(rows, gsaCohort, gsaCleanStart, gsaExcludedDates)
	if len(allSeries) == 0 {
		return GSASummary{}, fmt.Errorf("gsa history: no rows for cohort %q on or after %s", gsaCohort, gsaCleanStart)
	}
	first, last := allSeries[0], allSeries[len(allSeries)-1]

	var v1First, v2First, v3First, v3AgenciesFirst, usaClassFirst, semverFirst int
	var v1Last, v2Last, v3Last, v3AgenciesLast, usaClassLast, semverLast int

	var err error
	if v1First, err = atoiField(first.V1X, "v1_x"); err != nil {
		return GSASummary{}, err
	}
	if v2First, err = atoiField(first.V2X, "v2_x"); err != nil {
		return GSASummary{}, err
	}
	if v3First, err = atoiField(first.V3X, "v3_x"); err != nil {
		return GSASummary{}, err
	}
	if v1Last, err = atoiField(last.V1X, "v1_x"); err != nil {
		return GSASummary{}, err
	}
	if v2Last, err = atoiField(last.V2X, "v2_x"); err != nil {
		return GSASummary{}, err
	}
	if v3Last, err = atoiField(last.V3X, "v3_x"); err != nil {
		return GSASummary{}, err
	}
	if v3AgenciesFirst, err = atoiField(first.AgenciesV3, "agencies_v3"); err != nil {
		return GSASummary{}, err
	}
	if v3AgenciesLast, err = atoiField(last.AgenciesV3, "agencies_v3"); err != nil {
		return GSASummary{}, err
	}
	if usaClassFirst, err = atoiField(first.UsaClass, "usa_class"); err != nil {
		return GSASummary{}, err
	}
	if usaClassLast, err = atoiField(last.UsaClass, "usa_class"); err != nil {
		return GSASummary{}, err
	}
	if semverFirst, err = atoiField(first.SemanticVersion, "semantic_version"); err != nil {
		return GSASummary{}, err
	}
	if semverLast, err = atoiField(last.SemanticVersion, "semantic_version"); err != nil {
		return GSASummary{}, err
	}

	legacyFirst := v1First + v2First
	legacyLast := v1Last + v2Last
	majorsFirst := v1First + v2First + v3First
	majorsLast := v1Last + v2Last + v3Last

	// version_coverage has no zero-guard in the original -- dividing by zero
	// there is a crash (ZeroDivisionError), not a silently-wrong number, so
	// error here too rather than let Go's float division produce +Inf/NaN.
	if usaClassFirst == 0 {
		return GSASummary{}, fmt.Errorf("gsa history: usa_class is zero on %s, cannot compute version coverage", first.ReportDate)
	}
	if usaClassLast == 0 {
		return GSASummary{}, fmt.Errorf("gsa history: usa_class is zero on %s, cannot compute version coverage", last.ReportDate)
	}
	versionCoverageFirst := float64(semverFirst) / float64(usaClassFirst)
	versionCoverageLast := float64(semverLast) / float64(usaClassLast)

	// v3_share DOES have an explicit zero-guard in the original (falls back
	// to 0), unlike version_coverage above -- replicate that distinction.
	var v3ShareFirst, v3ShareLast float64
	if majorsFirst != 0 {
		v3ShareFirst = float64(v3First) / float64(majorsFirst)
	}
	if majorsLast != 0 {
		v3ShareLast = float64(v3Last) / float64(majorsLast)
	}

	return GSASummary{
		StartDate:            first.ReportDate,
		EndDate:              last.ReportDate,
		V3First:              v3First,
		V3Last:               v3Last,
		V3AgenciesFirst:      v3AgenciesFirst,
		V3AgenciesLast:       v3AgenciesLast,
		LegacyFirst:          legacyFirst,
		LegacyLast:           legacyLast,
		V1Last:               v1Last,
		V2First:              v2First,
		V2Last:               v2Last,
		UsaClassFirst:        usaClassFirst,
		UsaClassLast:         usaClassLast,
		SemverFirst:          semverFirst,
		SemverLast:           semverLast,
		VersionCoverageFirst: versionCoverageFirst,
		VersionCoverageLast:  versionCoverageLast,
		V3ShareFirst:         v3ShareFirst,
		V3ShareLast:          v3ShareLast,
		Series:               allSeries,
	}, nil
}

// latestSnapshotMeta mirrors Python's latest_snapshot(): find the
// lexicographically-max directory name under archiveDir and read only its
// meta.json -- deliberately not trend.go's loadArchive, which requires the
// snapshot CSV to exist and would silently disagree with this answer if a
// meta.json-only directory ever existed (Python's version never touches the
// CSV at all).
func latestSnapshotMeta(archiveDir string) (date string, meta SnapshotMeta, ok bool, err error) {
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", SnapshotMeta{}, false, nil
		}
		return "", SnapshotMeta{}, false, err
	}

	var latest string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() > latest {
			latest = e.Name()
		}
	}
	if latest == "" {
		return "", SnapshotMeta{}, false, nil
	}

	metaPath := filepath.Join(archiveDir, latest, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return latest, SnapshotMeta{}, true, nil
		}
		return "", SnapshotMeta{}, false, err
	}

	var m SnapshotMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return "", SnapshotMeta{}, false, fmt.Errorf("parsing %s: %w", metaPath, err)
	}
	return latest, m, true, nil
}

// HARow is one row of an data/external/httparchive-*.csv file
// (DateTime,ALL,USWDS). USWDS is nil when the field is blank or absent,
// i.e. before USWDS detection existed in that dataset.
type HARow struct {
	Date  time.Time
	All   int
	USWDS *int
}

// loadHTTPArchive reads one of the three httparchive-*.csv files. The
// header row is skipped unconditionally (no name check), matching Python;
// FieldsPerRecord is relaxed since these files aren't this tool's own
// output and Python's csv.reader never validates row width either.
func loadHTTPArchive(path string) ([]HARow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("reading header of %s: %w", path, err)
	}

	var rows []HARow
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading row of %s: %w", path, err)
		}

		dateStr := row[0]
		if len(dateStr) > 10 {
			dateStr = dateStr[:10]
		}
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("%s: parsing date %q: %w", path, row[0], err)
		}

		all, err := strconv.Atoi(strings.TrimSpace(row[1]))
		if err != nil {
			return nil, fmt.Errorf("%s: parsing ALL %q: %w", path, row[1], err)
		}

		var uswds *int
		if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
			u, err := strconv.Atoi(strings.TrimSpace(row[2]))
			if err != nil {
				return nil, fmt.Errorf("%s: parsing USWDS %q: %w", path, row[2], err)
			}
			uswds = &u
		}

		rows = append(rows, HARow{Date: d, All: all, USWDS: uswds})
	}

	return rows, nil
}

// yearlyAvg groups rows by calendar year and averages valueFn(all, uswds)
// over rows with a non-nil USWDS value, accumulating in the rows' original
// order so the floating-point summation order (and thus the exact bit
// pattern of the result) matches Python's left-to-right sum(). Whether rows
// have already been date-filtered before being passed in varies by call
// site in the original script -- some rely solely on this function's
// nil-skip over the *entire* unfiltered history -- so callers must
// replicate the exact filtering used at each call site, not assume a
// shared filter here.
func yearlyAvg(rows []HARow, valueFn func(all, uswds int) float64) map[int]float64 {
	sums := map[int]float64{}
	counts := map[int]int{}
	for _, r := range rows {
		if r.USWDS == nil {
			continue
		}
		y := r.Date.Year()
		sums[y] += valueFn(r.All, *r.USWDS)
		counts[y]++
	}
	out := make(map[int]float64, len(sums))
	for y, s := range sums {
		out[y] = s / float64(counts[y])
	}
	return out
}

func fmtPct(x float64) string { return fmt.Sprintf("%.0f%%", x*100) }

func fmtDeltaPts(a, b float64) string { return fmt.Sprintf("%+.1fpt", (b-a)*100) }

// thousands renders n with comma thousands-separators, Python's "{:,}"
// format (Go has no stdlib equivalent). Only ever called on non-negative
// counts in this report, but the sign case is handled anyway.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}

func kpiCard(col, label, valueHTML, note string) string {
	return fmt.Sprintf(`<li class="usa-card %s">
      <div class="usa-card__container">
        <div class="usa-card__header">
          <h3 class="usa-card__heading font-body-3xs text-uppercase text-base-dark">%s</h3>
        </div>
        <div class="usa-card__body">
          <p class="report-kpi-value">%s</p>
          <p class="text-base-dark font-body-2xs">%s</p>
        </div>
      </div>
    </li>`, col, label, valueHTML, note)
}

// chartSeries is one named data series in a line chart, e.g. USWDS v3.x
// site counts over time. Field order matches the key order build-report.py
// (now buildreport.go) has always emitted.
type chartSeries struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Color  string `json:"color"`
	Values []int  `json:"values"`
}

// lineChart is the shape shared by the versionMix and originCount charts:
// a set of named series plotted against a shared set of dates.
type lineChart struct {
	Dates     []string      `json:"dates"`
	Series    []chartSeries `json:"series"`
	YLabel    string        `json:"yLabel"`
	AriaLabel string        `json:"ariaLabel"`
}

type shareByYearChart struct {
	Categories  []string  `json:"categories"`
	Values      []pyFloat `json:"values"`
	Colors      []string  `json:"colors"`
	YLabel      string    `json:"yLabel"`
	ValueFormat string    `json:"valueFormat"`
	AriaLabel   string    `json:"ariaLabel"`
}

type chartAnnotation struct {
	Date  string `json:"date"`
	Label string `json:"label"`
}

type cwvChart struct {
	Dates       []string        `json:"dates"`
	Series      []chartSeries   `json:"series"`
	YLabel      string          `json:"yLabel"`
	YTickFormat string          `json:"yTickFormat"`
	YDomain     []int           `json:"yDomain"`
	Annotation  chartAnnotation `json:"annotation"`
	AriaLabel   string          `json:"ariaLabel"`
}

type a11yChartData struct {
	Dates       []string      `json:"dates"`
	Series      []chartSeries `json:"series"`
	YLabel      string        `json:"yLabel"`
	YTickFormat string        `json:"yTickFormat"`
	YDomain     []int         `json:"yDomain"`
	AriaLabel   string        `json:"ariaLabel"`
}

// chartDataPayload is the whole chart_data blob embedded in
// docs/index.html. Field order matches the key order Python's dict literal
// used, which json.dumps (and here, encoding/json on a struct) preserves.
type chartDataPayload struct {
	VersionMix  lineChart        `json:"versionMix"`
	ShareByYear shareByYearChart `json:"shareByYear"`
	OriginCount lineChart        `json:"originCount"`
	CWV         cwvChart         `json:"cwv"`
	A11y        a11yChartData    `json:"a11y"`
}

func sortedYearsAtLeast(m map[int]float64, min int) []int {
	var ys []int
	for y := range m {
		if y >= min {
			ys = append(ys, y)
		}
	}
	sort.Ints(ys)
	return ys
}

type dateInt struct {
	Date  time.Time
	Value int
}

type dateIntInt struct {
	Date time.Time
	A, U int
}

// renderHTML ports render(): the whole function is built with plain Go
// string literals + strings.Builder, not text/template. Two reasons found
// in the source: (1) the skeleton contains genuine literal "%" characters
// (e.g. "...0f}%") that would all need manual %%-escaping if this were one
// big fmt.Sprintf format string -- a single missed one silently corrupts
// output. Routing only individual value expressions through small,
// deliberate fmt.Sprintf calls contains that risk entirely. (2) the inline
// <script type="module"> block uses literal single braces (Python only
// needed {{/}} to escape them in an f-string) -- in a text/template source
// those would collide with template's own delimiters, and importing
// html/template instead (an easy mistake given "HTML" in the name) would
// corrupt the USWDS entities and the raw JSON script tag via auto-escaping.
func renderHTML(gsa GSASummary, snapDate string, snapMeta SnapshotMeta, snapOK bool, haOrigins, haCWV, haA11y []HARow) (string, error) {
	generatedAt := time.Now().UTC().Format("2006-01-02")

	gsaDates := make([]string, len(gsa.Series))
	v1Vals := make([]int, len(gsa.Series))
	v2Vals := make([]int, len(gsa.Series))
	v3Vals := make([]int, len(gsa.Series))
	for i, r := range gsa.Series {
		gsaDates[i] = r.ReportDate
		v, err := atoiField(r.V1X, "v1_x")
		if err != nil {
			return "", err
		}
		v1Vals[i] = v
		if v, err = atoiField(r.V2X, "v2_x"); err != nil {
			return "", err
		}
		v2Vals[i] = v
		if v, err = atoiField(r.V3X, "v3_x"); err != nil {
			return "", err
		}
		v3Vals[i] = v
	}

	yearlyShare := yearlyAvg(haOrigins, func(all, uswds int) float64 { return float64(uswds) / float64(all) * 10000 })
	years := sortedYearsAtLeast(yearlyShare, 2022)
	// bar_categories' current-year asterisk uses Python's date.today() --
	// local system date -- while generatedAt above uses UTC. This mismatch
	// is deliberate output-preservation of an existing Python inconsistency,
	// not a bug to fix here.
	currentYear := time.Now().Year()
	barCategories := make([]string, len(years))
	barValues := make([]float64, len(years))
	barColors := make([]string, len(years))
	for i, y := range years {
		if y == currentYear {
			barCategories[i] = fmt.Sprintf("%d*", y)
		} else {
			barCategories[i] = strconv.Itoa(y)
		}
		barValues[i] = yearlyShare[y]
		switch {
		case y == years[len(years)-1]:
			barColors[i] = "#005ea2"
		case y == years[len(years)-2]:
			barColors[i] = "#c05600"
		default:
			barColors[i] = "#a9aeb1"
		}
	}

	var originSeries []dateInt
	for _, r := range haOrigins {
		if r.USWDS != nil && !r.Date.Before(httparchiveCleanStart) {
			originSeries = append(originSeries, dateInt{r.Date, *r.USWDS})
		}
	}
	originDates := make([]string, len(originSeries))
	originCounts := make([]int, len(originSeries))
	for i, e := range originSeries {
		originDates[i] = e.Date.Format("2006-01-02")
		originCounts[i] = e.Value
	}

	var cwvSeries []dateIntInt
	for _, r := range haCWV {
		if r.USWDS != nil && !r.Date.Before(httparchiveCleanStart) {
			cwvSeries = append(cwvSeries, dateIntInt{r.Date, r.All, *r.USWDS})
		}
	}
	cwvDates := make([]string, len(cwvSeries))
	cwvAll := make([]int, len(cwvSeries))
	cwvUswds := make([]int, len(cwvSeries))
	for i, e := range cwvSeries {
		cwvDates[i] = e.Date.Format("2006-01-02")
		cwvAll[i] = e.A
		cwvUswds[i] = e.U
	}

	var a11ySeries []dateIntInt
	for _, r := range haA11y {
		if r.USWDS != nil && !r.Date.Before(httparchiveCleanStart) {
			a11ySeries = append(a11ySeries, dateIntInt{r.Date, r.All, *r.USWDS})
		}
	}
	a11yDates := make([]string, len(a11ySeries))
	a11yAll := make([]int, len(a11ySeries))
	a11yUswds := make([]int, len(a11ySeries))
	for i, e := range a11ySeries {
		a11yDates[i] = e.Date.Format("2006-01-02")
		a11yAll[i] = e.A
		a11yUswds[i] = e.U
	}
	a11yMinUswds := a11yUswds[0]
	for _, v := range a11yUswds {
		if v < a11yMinUswds {
			a11yMinUswds = v
		}
	}
	a11yCurrentGap := a11yUswds[len(a11yUswds)-1] - a11yAll[len(a11yAll)-1]
	a11yWorstVsCurrentWeb := a11yMinUswds - a11yAll[len(a11yAll)-1]
	a11yMinGap := a11yUswds[0] - a11yAll[0]
	for i := range a11yUswds {
		if d := a11yUswds[i] - a11yAll[i]; d < a11yMinGap {
			a11yMinGap = d
		}
	}

	cwvYearlyAll := yearlyAvg(haCWV, func(all, uswds int) float64 { return float64(all) })
	cwvYearlyUswds := yearlyAvg(haCWV, func(all, uswds int) float64 { return float64(uswds) })
	var cwvYears []int
	for y := range cwvYearlyAll {
		if y < 2022 {
			continue
		}
		if _, ok := cwvYearlyUswds[y]; ok {
			cwvYears = append(cwvYears, y)
		}
	}
	sort.Ints(cwvYears)
	gapFirst := cwvYearlyUswds[cwvYears[0]] - cwvYearlyAll[cwvYears[0]]
	gapLast := cwvYearlyUswds[cwvYears[len(cwvYears)-1]] - cwvYearlyAll[cwvYears[len(cwvYears)-1]]
	var recentYears []int
	for _, y := range cwvYears {
		if y >= cwvYears[len(cwvYears)-1]-2 {
			recentYears = append(recentYears, y)
		}
	}
	var recentUswdsSum, recentAllSum float64
	for _, y := range recentYears {
		recentUswdsSum += cwvYearlyUswds[y]
	}
	for _, y := range recentYears {
		recentAllSum += cwvYearlyAll[y]
	}
	ratioRecent := recentUswdsSum / recentAllSum

	topNHTML := "n/a &mdash; no live snapshot found"
	var topNNote string
	if snapOK && snapMeta.TopNSize != 0 {
		topNHTML = fmt.Sprintf("%.0f%%", snapMeta.TopNCoverage*100)
		topNNote = fmt.Sprintf("of top %d .gov domains by traffic, snapshot %s", snapMeta.TopNSize, snapDate)
	} else {
		topNNote = "no live snapshot found -- run `./uswds-usage report` first"
	}

	legacyDelta := gsa.LegacyLast - gsa.LegacyFirst
	if gsa.V3First == 0 {
		return "", fmt.Errorf("gsa history: v3_x is zero on %s, cannot compute v3 growth", gsa.StartDate)
	}
	v3GrowthPct := float64(gsa.V3Last-gsa.V3First) / float64(gsa.V3First) * 100
	var legacyGrowthPct float64
	if gsa.LegacyFirst != 0 {
		legacyGrowthPct = float64(legacyDelta) / float64(gsa.LegacyFirst) * 100
	}

	chartData := chartDataPayload{
		VersionMix: lineChart{
			Dates: gsaDates,
			Series: []chartSeries{
				{"v3", "v3.x", "#005ea2", v3Vals},
				{"v2", "v2.x", "#c05600", v2Vals},
				{"v1", "v1.x", "#a9aeb1", v1Vals},
			},
			YLabel:    "Sites",
			AriaLabel: "Line chart of USWDS v1, v2 and v3 site counts over time.",
		},
		ShareByYear: shareByYearChart{
			Categories:  barCategories,
			Values:      pyFloats(barValues),
			Colors:      barColors,
			YLabel:      "USWDS origins per 10,000 crawled",
			ValueFormat: ".2f",
			AriaLabel:   "Bar chart of USWDS share of all HTTP Archive origins by year.",
		},
		OriginCount: lineChart{
			Dates:     originDates,
			Series:    []chartSeries{{"origins", "USWDS-detected origins", "#005ea2", originCounts}},
			YLabel:    "USWDS-detected origins",
			AriaLabel: "Line chart of raw USWDS origin counts climbing over time.",
		},
		CWV: cwvChart{
			Dates: cwvDates,
			Series: []chartSeries{
				{"uswds", "USWDS sites", "#005ea2", cwvUswds},
				{"all", "Web average", "#a9aeb1", cwvAll},
			},
			YLabel:      "% of page loads, Good CWV",
			YTickFormat: ".0f",
			YDomain:     []int{0, 70},
			Annotation: chartAnnotation{
				Date:  inpTransitionDate.Format("2006-01-02"),
				Label: "Mar 2024: FID→INP",
			},
			AriaLabel: "Line chart comparing Core Web Vitals pass rates for USWDS sites vs. the web average.",
		},
		A11y: a11yChartData{
			Dates: a11yDates,
			Series: []chartSeries{
				{"uswds", "USWDS sites", "#005ea2", a11yUswds},
				{"all", "Web median", "#a9aeb1", a11yAll},
			},
			YLabel:      "Median Lighthouse accessibility score",
			YTickFormat: ".0f",
			YDomain:     []int{75, 100},
			AriaLabel:   "Line chart comparing median Lighthouse accessibility scores for USWDS sites vs. the web median.",
		},
	}
	chartDataJSON, err := dumpsPy(chartData)
	if err != nil {
		return "", fmt.Errorf("encoding chart data: %w", err)
	}

	legacyArrow := "&#9650;"
	legacyColor := "text-error-dark"
	if legacyDelta < 0 {
		legacyArrow = "&#9660;"
		legacyColor = "text-success-dark"
	}

	kpiCard1 := kpiCard("tablet:grid-col-3", "Current major (v3.x) adoption",
		fmt.Sprintf(`%s <span class="font-body-sm text-success-dark">&#9650; %.0f%%</span>`, thousands(gsa.V3Last), v3GrowthPct),
		fmt.Sprintf(`sites on v3.x, up from %s &middot; %d agencies now on v3`, thousands(gsa.V3First), gsa.V3AgenciesLast))
	kpiCard2 := kpiCard("tablet:grid-col-3", "Legacy (v1.x + v2.x) sites",
		fmt.Sprintf(`%s <span class="font-body-sm %s">%s %.0f%%</span>`, thousands(gsa.LegacyLast), legacyColor, legacyArrow, math.Abs(legacyGrowthPct)),
		fmt.Sprintf(`from %s &middot; still %d sites on v1.x`, thousands(gsa.LegacyFirst), gsa.V1Last))
	kpiCard3 := kpiCard("tablet:grid-col-3", "Version reporting coverage",
		fmt.Sprintf(`%s <span class="font-body-sm text-success-dark">&#9650; %s</span>`, fmtPct(gsa.VersionCoverageLast), fmtDeltaPts(gsa.VersionCoverageFirst, gsa.VersionCoverageLast)),
		fmt.Sprintf(`of USWDS-detected sites report a readable version &mdash; up from %s`, fmtPct(gsa.VersionCoverageFirst)))
	kpiCard4 := kpiCard("tablet:grid-col-3", "Traffic-weighted reach", topNHTML, topNNote)
	kpiCard5 := kpiCard("tablet:grid-col-4", "Share of all crawled origins",
		fmt.Sprintf(`%.2f <span class="font-body-sm text-success-dark">&#9650; %.0f%%</span>`, yearlyShare[years[len(years)-1]], (yearlyShare[years[len(years)-1]]/yearlyShare[years[0]]-1)*100),
		fmt.Sprintf(`per 10,000 origins, %d avg &mdash; up from %.2f in %d`, years[len(years)-1], yearlyShare[years[0]], years[0]))
	kpiCard6 := kpiCard("tablet:grid-col-6", "Performance gap vs. the web",
		fmt.Sprintf(`%+.1fpt <span class="font-body-sm text-success-dark">&#9650;</span>`, gapLast),
		fmt.Sprintf(`up from %+.1fpt in %d`, gapFirst, cwvYears[0]))
	kpiCard7 := kpiCard("tablet:grid-col-6", "Relative advantage",
		fmt.Sprintf(`%.2f&times;`, ratioRecent),
		"a USWDS page's chance of passing CWV vs. the average page, last 3yr avg")
	kpiCard8 := kpiCard("tablet:grid-col-4", "Accessibility score, latest",
		fmt.Sprintf(`%d <span class="font-body-sm text-base-dark">vs %d</span>`, a11yUswds[len(a11yUswds)-1], a11yAll[len(a11yAll)-1]),
		fmt.Sprintf(`USWDS sites vs. the web median &mdash; a %+dpt gap`, a11yCurrentGap))
	kpiCard9 := kpiCard("tablet:grid-col-4", "Worst USWDS month ever measured",
		strconv.Itoa(a11yMinUswds),
		fmt.Sprintf(`still %+dpt above where the median site sits <em>today</em>`, a11yWorstVsCurrentWeb))
	kpiCard10 := kpiCard("tablet:grid-col-4", "Consistency",
		fmt.Sprintf(`%d of %d`, len(a11ySeries), len(a11ySeries)),
		fmt.Sprintf(`months with a double-digit-point USWDS lead &mdash; every month measured, minimum %+dpt`, a11yMinGap))

	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>USWDS Adoption Pulse</title>
  <link rel="stylesheet" href="assets/uswds/css/uswds.css">
  <link rel="preconnect" href="https://cdnjs.cloudflare.com" crossorigin>
  <!-- No uswds-init.js / uswds.min.js: those exist to prevent FOUC on
       Banner/Header/Modal and to initialize interactive component JS
       (accordion, combo-box, sortable tables, dismissible alerts, etc).
       This page uses none of that -- card, alert, table and prose here are
       all static markup+CSS with zero JS files of their own (verified
       against the component sources). Add them back if a future component
       here actually needs JS. -->
  <style>`)
	b.WriteString(reportCSS)
	b.WriteString(`</style>
  <script src="https://cdnjs.cloudflare.com/ajax/libs/d3/7.9.0/d3.min.js" integrity="sha512-vc58qvvBdrDR4etbxMdlTt4GBQk1qjvyORR2nrsPsFPyrs+/u5c3+1Ct6upOgdZoIl7eq6k3a1UPDSNAQi/32A==" crossorigin="anonymous" referrerpolicy="no-referrer" type="module"></script>
  <script src="assets/report.js" type="module"></script>
  <script type="application/json" id="report-chart-data">`)
	b.WriteString(chartDataJSON)
	b.WriteString(`</script>
  <script type="module">
    (function () {
      var data = JSON.parse(document.getElementById("report-chart-data").textContent);
      reportCharts.drawLineChart("#chart-version-mix", data.versionMix);
      reportCharts.drawBarChart("#chart-share-by-year", data.shareByYear);
      reportCharts.drawLineChart("#chart-origin-count", data.originCount);
      reportCharts.drawLineChart("#chart-cwv", data.cwv);
      reportCharts.drawLineChart("#chart-a11y", data.a11y);
    })();
  </script>
</head>
<body>
<div class="grid-container padding-y-4">
<main>

  <section class="usa-prose margin-bottom-5">
    <p class="font-body-3xs text-uppercase text-primary text-bold">Program health &middot; generated `)
	b.WriteString(generatedAt)
	b.WriteString(`</p>
    <h1>USWDS Adoption Pulse</h1>
    <p class="usa-intro">Where federal USWDS adoption stands, whether it's moving, and whether it actually performs better. Regenerated monthly from GSA's site-scanning history, this tool's own live snapshot, and an independent, web-wide corroboration from HTTP Archive.</p>
  </section>

  <section class="usa-prose margin-bottom-5">
    <h2>Headline signals</h2>
    <p>Comparable window: `)
	b.WriteString(gsa.StartDate)
	b.WriteString(` &rarr; `)
	b.WriteString(gsa.EndDate)
	b.WriteString(`.</p>
  </section>
  <ul class="usa-card-group margin-bottom-5">
    `)
	b.WriteString(kpiCard1)
	b.WriteString("\n    ")
	b.WriteString(kpiCard2)
	b.WriteString("\n    ")
	b.WriteString(kpiCard3)
	b.WriteString("\n    ")
	b.WriteString(kpiCard4)
	b.WriteString(`
  </ul>

  <section class="usa-prose margin-bottom-5">
    <h2>Version mix over time</h2>
    <p>Site count by USWDS major version, "`)
	b.WriteString(gsaCohort)
	b.WriteString(`" cohort (GSA's site-scanning-analysis report).</p>
  </section>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">v1.x / v2.x / v3.x site counts</h3>
      <span class="font-body-3xs text-base-dark">`)
	b.WriteString(gsa.StartDate)
	b.WriteString(` &rarr; `)
	b.WriteString(gsa.EndDate)
	b.WriteString(`</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-version-mix" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>v3.x <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(gsa.V3First))
	b.WriteString(` &rarr; `)
	b.WriteString(strconv.Itoa(gsa.V3Last))
	b.WriteString(`</span></span>
      <span><span class="report-swatch" style="background:#c05600"></span>v2.x <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(gsa.V2First))
	b.WriteString(` &rarr; `)
	b.WriteString(strconv.Itoa(gsa.V2Last))
	b.WriteString(`</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>v1.x <span class="text-base-dark">&rarr; `)
	b.WriteString(strconv.Itoa(gsa.V1Last))
	b.WriteString(`</span></span>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>The web-wide view</h2>
    <p>HTTP Archive's independent, web-wide monthly crawl (not just .gov), Wappalyzer USWDS detection since late 2021.</p>
  </section>
  <ul class="usa-card-group margin-bottom-3">
    `)
	b.WriteString(kpiCard5)
	b.WriteString(`
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
      <span class="font-body-3xs text-base-dark">monthly, `)
	b.WriteString(originSeries[0].Date.Format("2006-01-02"))
	b.WriteString(` &rarr; `)
	b.WriteString(originSeries[len(originSeries)-1].Date.Format("2006-01-02"))
	b.WriteString(`</span>
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
    `)
	b.WriteString(kpiCard6)
	b.WriteString("\n    ")
	b.WriteString(kpiCard7)
	b.WriteString(`
  </ul>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">Share of page loads with "Good" Core Web Vitals</h3>
      <span class="font-body-3xs text-base-dark">monthly, `)
	b.WriteString(cwvSeries[0].Date.Format("2006-01-02"))
	b.WriteString(` &rarr; `)
	b.WriteString(cwvSeries[len(cwvSeries)-1].Date.Format("2006-01-02"))
	b.WriteString(`</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-cwv" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>USWDS sites <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(cwvSeries[0].U))
	b.WriteString(`% &rarr; `)
	b.WriteString(strconv.Itoa(cwvSeries[len(cwvSeries)-1].U))
	b.WriteString(`%</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>Web average <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(cwvSeries[0].A))
	b.WriteString(`% &rarr; `)
	b.WriteString(strconv.Itoa(cwvSeries[len(cwvSeries)-1].A))
	b.WriteString(`%</span></span>
    </div>
  </div>

  <section class="usa-prose margin-bottom-5">
    <h2>Is USWDS actually more accessible?</h2>
    <p><strong>Median</strong> Lighthouse accessibility score (0&ndash;100: alt text, color contrast, ARIA labels, form labels, heading structure, and more) &mdash; HTTP Archive publishes these as medians, not means &mdash; USWDS sites vs. the web at large. The gap has held in a tight, stable band for the full 55-month history.</p>
  </section>
  <ul class="usa-card-group margin-bottom-3">
    `)
	b.WriteString(kpiCard8)
	b.WriteString("\n    ")
	b.WriteString(kpiCard9)
	b.WriteString("\n    ")
	b.WriteString(kpiCard10)
	b.WriteString(`
  </ul>
  <div class="report-chart-panel margin-bottom-5">
    <div class="display-flex flex-justify flex-align-baseline flex-wrap margin-bottom-1">
      <h3 class="margin-0">Median Lighthouse accessibility score</h3>
      <span class="font-body-3xs text-base-dark">monthly, `)
	b.WriteString(a11ySeries[0].Date.Format("2006-01-02"))
	b.WriteString(` &rarr; `)
	b.WriteString(a11ySeries[len(a11ySeries)-1].Date.Format("2006-01-02"))
	b.WriteString(`</span>
    </div>
    <div class="report-chart-wrap">
      <div id="chart-a11y" class="report-chart"></div>
    </div>
    <div class="report-legend">
      <span><span class="report-swatch" style="background:#005ea2"></span>USWDS sites <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(a11ySeries[0].U))
	b.WriteString(` &rarr; `)
	b.WriteString(strconv.Itoa(a11ySeries[len(a11ySeries)-1].U))
	b.WriteString(`</span></span>
      <span><span class="report-swatch" style="background:#a9aeb1"></span>Web median <span class="text-base-dark">`)
	b.WriteString(strconv.Itoa(a11ySeries[0].A))
	b.WriteString(` &rarr; `)
	b.WriteString(strconv.Itoa(a11ySeries[len(a11ySeries)-1].A))
	b.WriteString(`</span></span>
    </div>
  </div>

  <footer class="usa-prose font-body-3xs text-base-dark padding-top-2 border-top border-base-lighter">
    <p>Sources: github.com/GSA/site-scanning-analysis (reports/uswds.csv) &middot; api.gsa.gov/technology/site-scanning &middot; analytics.usa.gov &middot; uswds-usage CLI &middot; HTTP Archive technology detection, Chrome UX Report Core Web Vitals &amp; Lighthouse accessibility scores &middot; regenerated `)
	b.WriteString(generatedAt)
	b.WriteString(`</p>
    <p>GSA's "Fix USWDS report" commit on 2026-03-25 changed how the filtered-cohort figures above are calculated (~10x jump), so this report starts at `)
	b.WriteString(gsaCleanStart)
	b.WriteString(`; 2026-06-26 (a single broken scan day) is excluded too. Re-scanned for new breaks of the same shape on every run (see build log).</p>
    <p>SLIs: version-major currency `)
	b.WriteString(fmtPct(gsa.V3ShareFirst))
	b.WriteString(` &rarr; `)
	b.WriteString(fmtPct(gsa.V3ShareLast))
	b.WriteString(` &middot; version reporting coverage `)
	b.WriteString(fmtPct(gsa.VersionCoverageFirst))
	b.WriteString(` &rarr; `)
	b.WriteString(fmtPct(gsa.VersionCoverageLast))
	b.WriteString(` &middot; traffic-weighted reach `)
	b.WriteString(topNHTML)
	b.WriteString(` &middot; web-wide share `)
	b.WriteString(fmt.Sprintf("%.2f", yearlyShare[years[0]]))
	b.WriteString(` &rarr; `)
	b.WriteString(fmt.Sprintf("%.2f", yearlyShare[years[len(years)-1]]))
	b.WriteString(` per 10k origins &middot; performance gap `)
	b.WriteString(fmt.Sprintf("%+.1f", gapFirst))
	b.WriteString(`pt &rarr; `)
	b.WriteString(fmt.Sprintf("%+.1f", gapLast))
	b.WriteString(`pt &middot; accessibility gap (median) `)
	b.WriteString(fmt.Sprintf("%+d", a11yUswds[0]-a11yAll[0]))
	b.WriteString(`pt &rarr; `)
	b.WriteString(fmt.Sprintf("%+d", a11yCurrentGap))
	b.WriteString(`pt.</p>
  </footer>

</main>
</div>
</body>
</html>
`)

	return b.String(), nil
}
