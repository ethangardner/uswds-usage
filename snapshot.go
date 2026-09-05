package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SnapshotCSVRow is one row of the historical archive's 9-column schema.
// UsesClasses/UsesElements are tri-state strings: "true", "false", or ""
// (unknown -- only possible for rows backfilled from a schema that predates
// that column split).
type SnapshotCSVRow struct {
	Domain               string
	Agency               string
	UswdsSemanticVersion string
	UsesClasses          string
	UsesElements         string
	Pageviews            string
	Visits               string
	SourceScanDate       string
	ReportRunAt          string
}

var snapshotCSVHeader = []string{
	"domain", "agency", "uswds_semantic_version", "uses_classes", "uses_elements",
	"pageviews", "visits", "source_scan_date", "report_run_at",
}

// snapshotRowsFromReport converts freshly-built report rows from a live fetch
// into the archive's row format, where uses_classes/uses_elements are always
// known.
func snapshotRowsFromReport(rows []ReportRow, runAt time.Time) []SnapshotCSVRow {
	runAtStr := runAt.Format(time.RFC3339)
	out := make([]SnapshotCSVRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, SnapshotCSVRow{
			Domain:               r.Domain,
			Agency:               r.Agency,
			UswdsSemanticVersion: r.UswdsSemanticVersion,
			UsesClasses:          fmt.Sprintf("%t", r.UsesClasses()),
			UsesElements:         fmt.Sprintf("%t", r.UsesElements()),
			Pageviews:            fmt.Sprintf("%d", r.Pageviews),
			Visits:               fmt.Sprintf("%d", r.Visits),
			SourceScanDate:       r.ScanDate,
			ReportRunAt:          runAtStr,
		})
	}
	return out
}

// SnapshotMeta records provenance for one dated snapshot in the historical
// archive: where the data came from, and (for live fetches) the top-N
// traffic-weighted coverage SLI, which can only be computed at fetch time
// since it requires the full analytics domain universe, not just the
// USWDS-adopting subset that the archive CSV stores.
type SnapshotMeta struct {
	ReportRunAt           time.Time `json:"report_run_at"`
	GSASourceURL          string    `json:"gsa_source_url,omitempty"`
	GSALastModified       string    `json:"gsa_last_modified,omitempty"`
	GSAETag               string    `json:"gsa_etag,omitempty"`
	GSASourceColumns      []string  `json:"gsa_source_columns,omitempty"`
	AnalyticsSourceURL    string    `json:"analytics_source_url,omitempty"`
	AnalyticsLastModified string    `json:"analytics_last_modified,omitempty"`
	AnalyticsETag         string    `json:"analytics_etag,omitempty"`
	SchemaVersion         int       `json:"schema_version"`
	RowCount              int       `json:"row_count"`
	Source                string    `json:"source"` // "live-fetch" or "backfill"
	BackfilledFrom        string    `json:"backfilled_from,omitempty"`
	// TopNSize/TopNAdopting/TopNCoverage are 0 for backfilled snapshots --
	// they require the raw analytics domain universe, which a backfilled
	// export (already filtered to adopters only) doesn't carry.
	TopNSize     int     `json:"top_n_size,omitempty"`
	TopNAdopting int     `json:"top_n_adopting,omitempty"`
	TopNCoverage float64 `json:"top_n_coverage,omitempty"`
}

const archiveSchemaVersion = 2

// archiveSnapshotDir returns the directory a given snapshot date lives in
// under archiveDir.
func archiveSnapshotDir(archiveDir, date string) string {
	return filepath.Join(archiveDir, date)
}

// writeSnapshot writes snapshots/<date>/uswds-traffic-report.csv and
// snapshots/<date>/meta.json, overwriting any existing snapshot for that
// date.
func writeSnapshot(archiveDir, date string, rows []SnapshotCSVRow, meta SnapshotMeta) error {
	dir := archiveSnapshotDir(archiveDir, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	meta.RowCount = len(rows)
	meta.SchemaVersion = archiveSchemaVersion

	if err := writeSnapshotCSVFile(filepath.Join(dir, "uswds-traffic-report.csv"), rows); err != nil {
		return err
	}

	return writeSnapshotMetaJSON(filepath.Join(dir, "meta.json"), meta)
}

func writeSnapshotCSVFile(path string, rows []SnapshotCSVRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write(snapshotCSVHeader); err != nil {
		return err
	}

	for _, r := range rows {
		if err := w.Write([]string{
			r.Domain, r.Agency, r.UswdsSemanticVersion, r.UsesClasses, r.UsesElements,
			r.Pageviews, r.Visits, r.SourceScanDate, r.ReportRunAt,
		}); err != nil {
			return err
		}
	}

	return w.Error()
}

func writeSnapshotMetaJSON(path string, meta SnapshotMeta) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(meta)
}
