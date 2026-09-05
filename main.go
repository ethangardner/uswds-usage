// Command uswds-usage downloads GSA's site-scanning data and analytics.usa.gov's
// top-100000-domains traffic snapshot, and reports 30-day traffic for sites
// that use USWDS. It can also archive dated snapshots of that report and
// compute adoption-trend metrics from the accumulated history.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]

	var err error
	switch {
	case len(args) == 0 || strings.HasPrefix(args[0], "-"):
		// Backward compatible: bare `uswds-usage` or `uswds-usage -flag ...`
		// runs the default report flow, same as every prior version.
		err = runReportCmd(args)
	case args[0] == "report":
		err = runReportCmd(args[1:])
	case args[0] == "backfill":
		err = runBackfillCmd(args[1:])
	case args[0] == "trend":
		err = runTrendCmd(args[1:])
	case args[0] == "serve":
		err = runServeCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q (expected report|backfill|trend|serve)\n", args[0])
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type reportOptions struct {
	output       string
	top          int
	topClasses   int
	topElements  int
	archiveDir   string
	noArchive    bool
	snapshotDate string
	topNCoverage int
}

func runReportCmd(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	output := fs.String("output", "uswds-traffic-report.csv", "path to write the CSV report to")
	top := fs.Int("top", 20, "number of top sites (by pageviews) to print to the console")
	topClasses := fs.Int("top-classes", 25, "number of most common USWDS classes to print to the console")
	topElements := fs.Int("top-elements", 25, "number of most common USWDS custom elements to print to the console")
	archiveDir := fs.String("archive-dir", "snapshots", "directory to write a dated historical snapshot to")
	noArchive := fs.Bool("no-archive", false, "skip writing a dated snapshot to the historical archive")
	snapshotDate := fs.String("snapshot-date", "", "override the archive snapshot date, YYYY-MM-DD (default: today, UTC)")
	topNCoverage := fs.Int("top-n-coverage", 500, "size of the top-by-traffic .gov domain cohort used for the traffic-weighted coverage SLI")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return run(reportOptions{
		output:       *output,
		top:          *top,
		topClasses:   *topClasses,
		topElements:  *topElements,
		archiveDir:   *archiveDir,
		noArchive:    *noArchive,
		snapshotDate: *snapshotDate,
		topNCoverage: *topNCoverage,
	})
}

func run(opts reportOptions) error {
	fmt.Println("Downloading site-scanning data...")
	siteScans, siteScanMeta, err := fetchSiteScanRecords()
	if err != nil {
		return err
	}

	fmt.Println("Downloading analytics.usa.gov traffic data...")
	analytics, analyticsHeader, err := fetchAnalyticsRecords()
	if err != nil {
		return err
	}

	rows := buildReport(siteScans, analytics)

	if err := writeReportCSV(opts.output, rows); err != nil {
		return err
	}
	fmt.Printf("Wrote report to %s\n\n", opts.output)

	printSummary(rows, opts.top)
	printElementsOnlySummary(rows, opts.top)
	printAgencyStats(agencyStats(siteScans))
	printUsageFrequency("USWDS classes", classFrequency(rows), opts.topClasses)
	printUsageFrequency("USWDS elements", elementFrequency(rows), opts.topElements)

	if opts.noArchive {
		return nil
	}

	runAt := time.Now().UTC()
	snapshotDate := opts.snapshotDate
	if snapshotDate == "" {
		snapshotDate = runAt.Format("2006-01-02")
	}

	topNCov := computeTopNCoverage(siteScans, analytics, opts.topNCoverage)
	meta := SnapshotMeta{
		ReportRunAt:           runAt,
		GSASourceURL:          siteScanningURL,
		GSALastModified:       siteScanMeta.Header.Get("Last-Modified"),
		GSAETag:               siteScanMeta.Header.Get("ETag"),
		GSASourceColumns:      siteScanMeta.Columns,
		AnalyticsSourceURL:    analyticsURL,
		AnalyticsLastModified: analyticsHeader.Get("Last-Modified"),
		AnalyticsETag:         analyticsHeader.Get("ETag"),
		Source:                "live-fetch",
		TopNSize:              topNCov.N,
		TopNAdopting:          topNCov.Adopting,
	}
	if topNCov.N > 0 {
		meta.TopNCoverage = float64(topNCov.Adopting) / float64(topNCov.N)
	}

	snapshotRows := snapshotRowsFromReport(rows, runAt)
	if err := writeSnapshot(opts.archiveDir, snapshotDate, snapshotRows, meta); err != nil {
		return fmt.Errorf("writing snapshot: %w", err)
	}
	fmt.Printf("Wrote snapshot to %s\n", archiveSnapshotDir(opts.archiveDir, snapshotDate))

	return nil
}
