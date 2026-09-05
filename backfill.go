package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Column layouts this tool's own report CSV has used historically, oldest
// first. backfill accepts either so a user's previously-saved exports (which
// may predate the uses_classes/uses_elements split) can be folded into the
// archive.
var (
	legacyReportColumns  = []string{"domain", "agency", "uswds_semantic_version", "pageviews", "visits"}
	currentReportColumns = []string{"domain", "agency", "uswds_semantic_version", "uses_classes", "uses_elements", "pageviews", "visits"}
)

func runBackfillCmd(args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	date := fs.String("date", "", "snapshot date this export represents, YYYY-MM-DD (required; never inferred from the file's mtime)")
	file := fs.String("file", "", "path to a prior report CSV export to ingest (required)")
	archiveDir := fs.String("archive-dir", "snapshots", "directory the historical archive lives in")
	force := fs.Bool("force", false, "overwrite an existing snapshot for -date")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *file == "" {
		return fmt.Errorf("backfill: -file is required")
	}
	if *date == "" {
		return fmt.Errorf("backfill: -date is required (YYYY-MM-DD); it is never inferred from the file's mtime")
	}
	parsedDate, err := time.Parse("2006-01-02", *date)
	if err != nil {
		return fmt.Errorf("backfill: -date must be YYYY-MM-DD: %w", err)
	}

	dest := archiveSnapshotDir(*archiveDir, *date)
	if !*force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("backfill: %s already exists; pass -force to overwrite", dest)
		}
	}

	runAt := parsedDate.UTC()
	rows, err := readLegacyReportCSV(*file, runAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("backfill: %w", err)
	}

	meta := SnapshotMeta{
		ReportRunAt:    runAt,
		Source:         "backfill",
		BackfilledFrom: *file,
	}

	if err := writeSnapshot(*archiveDir, *date, rows, meta); err != nil {
		return fmt.Errorf("backfill: %w", err)
	}

	fmt.Printf("Backfilled %d rows into %s\n", len(rows), dest)
	return nil
}

// readLegacyReportCSV reads a prior report CSV export (either the current
// 7-column schema or the older 5-column schema that predates the
// uses_classes/uses_elements split) and converts it into the archive's row
// format. uses_classes/uses_elements are left "" (unknown) when the source
// schema doesn't carry them, rather than guessed as false.
func readLegacyReportCSV(path, runAtStr string) ([]SnapshotCSVRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	for i, h := range header {
		header[i] = strings.TrimSpace(h)
	}

	var hasClassesElements bool
	switch {
	case columnsMatch(header, currentReportColumns):
		hasClassesElements = true
	case columnsMatch(header, legacyReportColumns):
		hasClassesElements = false
	default:
		return nil, fmt.Errorf("unrecognized column layout %v (expected %v or %v)", header, currentReportColumns, legacyReportColumns)
	}

	requiredCols := legacyReportColumns
	if hasClassesElements {
		requiredCols = currentReportColumns
	}
	idx, err := columnIndex(header, requiredCols...)
	if err != nil {
		return nil, err
	}

	var rows []SnapshotCSVRow
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading row: %w", err)
		}

		var usesClasses, usesElements string
		if hasClassesElements {
			usesClasses = strings.TrimSpace(row[idx["uses_classes"]])
			usesElements = strings.TrimSpace(row[idx["uses_elements"]])
		}

		rows = append(rows, SnapshotCSVRow{
			Domain:               strings.ToLower(strings.TrimSpace(row[idx["domain"]])),
			Agency:               row[idx["agency"]],
			UswdsSemanticVersion: row[idx["uswds_semantic_version"]],
			UsesClasses:          usesClasses,
			UsesElements:         usesElements,
			Pageviews:            strings.TrimSpace(row[idx["pageviews"]]),
			Visits:               strings.TrimSpace(row[idx["visits"]]),
			SourceScanDate:       "",
			ReportRunAt:          runAtStr,
		})
	}

	return rows, nil
}

func columnsMatch(header, expected []string) bool {
	if len(header) != len(expected) {
		return false
	}
	for i, h := range expected {
		if header[i] != h {
			return false
		}
	}
	return true
}
