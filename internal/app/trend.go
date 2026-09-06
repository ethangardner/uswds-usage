package app

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SnapshotDay holds every domain row recorded for one archive snapshot date,
// plus that snapshot's provenance metadata.
type SnapshotDay struct {
	Date string // YYYY-MM-DD, from the snapshot directory name
	Rows []SnapshotCSVRow
	Meta SnapshotMeta
}

// loadArchive reads every data/snapshots/<date>/uswds-traffic-report.csv (and its
// sibling meta.json, when present) under archiveDir, returning one
// SnapshotDay per dated subdirectory, sorted ascending by date.
func loadArchive(archiveDir string) ([]SnapshotDay, error) {
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("reading archive dir %s: %w", archiveDir, err)
	}

	var days []SnapshotDay
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dir := filepath.Join(archiveDir, e.Name())
		rows, err := readSnapshotCSV(filepath.Join(dir, "uswds-traffic-report.csv"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		meta, err := readSnapshotMeta(filepath.Join(dir, "meta.json"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}

		days = append(days, SnapshotDay{Date: e.Name(), Rows: rows, Meta: meta})
	}

	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return days, nil
}

func readSnapshotCSV(path string) ([]SnapshotCSVRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading %s header: %w", path, err)
	}
	idx, err := columnIndex(header, snapshotCSVHeader...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var rows []SnapshotCSVRow
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s row: %w", path, err)
		}
		rows = append(rows, SnapshotCSVRow{
			Domain:               row[idx["domain"]],
			Agency:               row[idx["agency"]],
			UswdsSemanticVersion: row[idx["uswds_semantic_version"]],
			UsesClasses:          row[idx["uses_classes"]],
			UsesElements:         row[idx["uses_elements"]],
			Pageviews:            row[idx["pageviews"]],
			Visits:               row[idx["visits"]],
			SourceScanDate:       row[idx["source_scan_date"]],
			ReportRunAt:          row[idx["report_run_at"]],
		})
	}
	return rows, nil
}

func readSnapshotMeta(path string) (SnapshotMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SnapshotMeta{}, err
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return SnapshotMeta{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return meta, nil
}

// TrendPoint is the set of computed metrics for one archive snapshot date.
type TrendPoint struct {
	Date                 string
	TotalAdopting        int
	ClassOnly            int
	ElementOnly          int
	Both                 int
	Unknown              int
	TotalPageviews       int64
	VersionReportedCount int
	VersionCoverage      float64 // fraction of adopting domains reporting any version
	PctOnLatestMajor     float64 // fraction of *version-reporting* domains on --latest-major
	// TopNSize/TopNCoverage come straight from the snapshot's meta.json
	// (computed at report-run time against the full analytics domain
	// universe); they're unavailable (TopNAvailable=false) for backfilled
	// snapshots, which only ever see the adopters-only archive CSV.
	TopNAvailable bool
	TopNSize      int
	TopNCoverage  float64
}

func computeTrend(days []SnapshotDay, latestMajor string) []TrendPoint {
	points := make([]TrendPoint, 0, len(days))
	for _, day := range days {
		points = append(points, computeTrendPoint(day, latestMajor))
	}
	return points
}

func computeTrendPoint(day SnapshotDay, latestMajor string) TrendPoint {
	p := TrendPoint{Date: day.Date, TotalAdopting: len(day.Rows)}

	var onLatestMajor int
	for _, r := range day.Rows {
		pv, _ := strconv.ParseInt(r.Pageviews, 10, 64)
		p.TotalPageviews += pv

		switch {
		case r.UsesClasses == "" || r.UsesElements == "":
			p.Unknown++
		case r.UsesClasses == "true" && r.UsesElements == "true":
			p.Both++
		case r.UsesElements == "true":
			p.ElementOnly++
		case r.UsesClasses == "true":
			p.ClassOnly++
		}

		v := strings.TrimSpace(r.UswdsSemanticVersion)
		if v != "" && v != "." {
			p.VersionReportedCount++
			if versionMajor(v) == latestMajor {
				onLatestMajor++
			}
		}
	}

	if p.TotalAdopting > 0 {
		p.VersionCoverage = float64(p.VersionReportedCount) / float64(p.TotalAdopting)
	}
	if p.VersionReportedCount > 0 {
		p.PctOnLatestMajor = float64(onLatestMajor) / float64(p.VersionReportedCount)
	}

	if day.Meta.TopNSize > 0 {
		p.TopNAvailable = true
		p.TopNSize = day.Meta.TopNSize
		p.TopNCoverage = day.Meta.TopNCoverage
	}

	return p
}

// versionMajor extracts the leading major-version component from a semantic
// version string like "v3.12.0" -> "3".
func versionMajor(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 2)
	return parts[0]
}

func printTrendTable(points []TrendPoint) {
	if len(points) == 0 {
		fmt.Println("No snapshots found in the archive.")
		return
	}

	fmt.Printf("%-12s %10s %10s %9s %10s %11s %10s %6s %9s %14s\n",
		"DATE", "ADOPTING", "TOP-N COV", "VER COV", "LATEST-MAJ", "CLASS-ONLY", "ELEM-ONLY", "BOTH", "UNKNOWN", "PAGEVIEWS")
	for _, p := range points {
		topN := "n/a"
		if p.TopNAvailable {
			topN = fmt.Sprintf("%.1f%%", p.TopNCoverage*100)
		}
		fmt.Printf("%-12s %10d %10s %8.1f%% %9.1f%% %11d %10d %6d %9d %14d\n",
			p.Date, p.TotalAdopting, topN, p.VersionCoverage*100, p.PctOnLatestMajor*100,
			p.ClassOnly, p.ElementOnly, p.Both, p.Unknown, p.TotalPageviews)
	}
	if hasUnknown(points) {
		fmt.Println("\nUNKNOWN = uses_classes/uses_elements not recorded in that snapshot's source (typically a backfilled pre-split export), not \"neither\".")
	}

	if len(points) >= 2 {
		first, last := points[0], points[len(points)-1]
		fmt.Printf("\nChange from %s to %s: %+d adopting domains, %+d total pageviews\n",
			first.Date, last.Date, last.TotalAdopting-first.TotalAdopting, last.TotalPageviews-first.TotalPageviews)
	}
}

func hasUnknown(points []TrendPoint) bool {
	for _, p := range points {
		if p.Unknown > 0 {
			return true
		}
	}
	return false
}

func writeTrendCSV(path string, points []TrendPoint) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{
		"date", "total_adopting", "class_only", "element_only", "both", "unknown",
		"total_pageviews", "version_coverage", "pct_on_latest_major", "top_n_size", "top_n_coverage",
	}); err != nil {
		return err
	}

	for _, p := range points {
		if err := w.Write([]string{
			p.Date,
			strconv.Itoa(p.TotalAdopting),
			strconv.Itoa(p.ClassOnly),
			strconv.Itoa(p.ElementOnly),
			strconv.Itoa(p.Both),
			strconv.Itoa(p.Unknown),
			strconv.FormatInt(p.TotalPageviews, 10),
			strconv.FormatFloat(p.VersionCoverage, 'f', 4, 64),
			strconv.FormatFloat(p.PctOnLatestMajor, 'f', 4, 64),
			strconv.Itoa(p.TopNSize),
			strconv.FormatFloat(p.TopNCoverage, 'f', 4, 64),
		}); err != nil {
			return err
		}
	}

	return w.Error()
}

func runTrendCmd(args []string) error {
	fs := flag.NewFlagSet("trend", flag.ExitOnError)
	archiveDir := fs.String("archive-dir", "data/snapshots", "directory the historical archive lives in")
	output := fs.String("output", "", "optional path to write computed trend metrics as CSV")
	latestMajor := fs.String("latest-major", "3", "current/supported USWDS major version, for the version-currency SLI")
	if err := fs.Parse(args); err != nil {
		return err
	}

	days, err := loadArchive(*archiveDir)
	if err != nil {
		return fmt.Errorf("trend: %w", err)
	}

	points := computeTrend(days, *latestMajor)
	printTrendTable(points)

	if *output != "" {
		if err := writeTrendCSV(*output, points); err != nil {
			return fmt.Errorf("trend: %w", err)
		}
		fmt.Printf("\nWrote trend metrics to %s\n", *output)
	}

	return nil
}
