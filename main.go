// Command uswds-usage downloads GSA's site-scanning data and analytics.usa.gov's
// top-100000-domains traffic snapshot, and reports 30-day traffic for sites
// that use USWDS.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	output := flag.String("output", "uswds-traffic-report.csv", "path to write the CSV report to")
	top := flag.Int("top", 20, "number of top sites (by pageviews) to print to the console")
	topClasses := flag.Int("top-classes", 25, "number of most common USWDS classes to print to the console")
	flag.Parse()

	if err := run(*output, *top, *topClasses); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(output string, top int, topClasses int) error {
	fmt.Println("Downloading site-scanning data...")
	siteScans, err := fetchSiteScanRecords()
	if err != nil {
		return err
	}

	fmt.Println("Downloading analytics.usa.gov traffic data...")
	analytics, err := fetchAnalyticsRecords()
	if err != nil {
		return err
	}

	rows := buildReport(siteScans, analytics)

	if err := writeReportCSV(output, rows); err != nil {
		return err
	}
	fmt.Printf("Wrote report to %s\n\n", output)

	printSummary(rows, top)
	printAgencyStats(agencyStats(siteScans))
	printClassFrequency(classFrequency(rows), topClasses)

	return nil
}
