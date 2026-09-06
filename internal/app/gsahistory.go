package app

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

// GSAHistoryRow is one row of data/external/gsa-uswds-report-history.csv --
// GSA's site-scanning-analysis cohort-level report, flattened into long
// format across its full commit history. Every field is kept as a raw
// string, exactly as Python's csv.DictReader would hand it back; callers
// convert to int only where the report generator needs to.
type GSAHistoryRow struct {
	ReportDate       string
	Commit           string
	Group            string
	Count            string
	Agencies         string
	SemanticVersion  string
	AgenciesSV       string
	V1X              string
	AgenciesV1       string
	V2X              string
	AgenciesV2       string
	V3X              string
	AgenciesV3       string
	Banner           string
	AgenciesBanner   string
	UsaClass         string
	AgenciesUsaClass string
}

var gsaHistoryColumns = []string{
	"report_date", "commit", "group", "count", "agencies", "semantic_version",
	"agencies_sv", "v1_x", "agencies_v1", "v2_x", "agencies_v2", "v3_x",
	"agencies_v3", "banner", "agencies_banner", "usa_class", "agencies_usa_class",
}

// gsaFieldMap maps GSA's source column names (from a historical
// reports/uswds.csv blob) to this repo's output column names, in the exact
// order that defines gsaHistoryColumns[2:] (report_date/commit are prepended
// separately). Order matters -- this mirrors Python's FIELD_MAP, an ordered
// dict in the original script.
var gsaFieldMap = []struct{ Src, Dst string }{
	{"Group", "group"},
	{"count", "count"},
	{"Agencies", "agencies"},
	{"semantic version", "semantic_version"},
	{"Agencies_sv", "agencies_sv"},
	{"v1.x", "v1_x"},
	{"Agencies_v1", "agencies_v1"},
	{"v2.x", "v2_x"},
	{"Agencies_v2", "agencies_v2"},
	{"v3.x", "v3_x"},
	{"Agencies_v3", "agencies_v3"},
	{"banner", "banner"},
	{"Agencies_banner", "agencies_banner"},
	{"usa-class", "usa_class"},
	{"Agencies_usa_class", "agencies_usa_class"},
}

// readGSAHistoryCSV reads data/external/gsa-uswds-report-history.csv, our
// own output file, so a strict header check (via columnIndex) is
// appropriate here -- unlike the historical GSA blobs refresh-gsa-history
// extracts from, which may be missing columns depending on vintage.
func readGSAHistoryCSV(path string) ([]GSAHistoryRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header of %s: %w", path, err)
	}

	idx, err := columnIndex(header, gsaHistoryColumns...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var rows []GSAHistoryRow
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading row of %s: %w", path, err)
		}

		rows = append(rows, GSAHistoryRow{
			ReportDate:       row[idx["report_date"]],
			Commit:           row[idx["commit"]],
			Group:            row[idx["group"]],
			Count:            row[idx["count"]],
			Agencies:         row[idx["agencies"]],
			SemanticVersion:  row[idx["semantic_version"]],
			AgenciesSV:       row[idx["agencies_sv"]],
			V1X:              row[idx["v1_x"]],
			AgenciesV1:       row[idx["agencies_v1"]],
			V2X:              row[idx["v2_x"]],
			AgenciesV2:       row[idx["agencies_v2"]],
			V3X:              row[idx["v3_x"]],
			AgenciesV3:       row[idx["agencies_v3"]],
			Banner:           row[idx["banner"]],
			AgenciesBanner:   row[idx["agencies_banner"]],
			UsaClass:         row[idx["usa_class"]],
			AgenciesUsaClass: row[idx["agencies_usa_class"]],
		})
	}

	return rows, nil
}
