package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const siteScanningURL = "https://api.gsa.gov/technology/site-scanning/data/site-scanning-live-filtered-latest.csv"

// SiteScanRecord holds the site-scanning fields relevant to USWDS reporting.
type SiteScanRecord struct {
	Domain               string
	Agency               string
	UswdsCount           int
	UswdsSemanticVersion string
	UswdsClasses         []string
}

// UsesUSWDS reports whether the site-scanning data detected USWDS on this site.
func (s SiteScanRecord) UsesUSWDS() bool {
	return s.UswdsCount > 0
}

func fetchSiteScanRecords() ([]SiteScanRecord, error) {
	r, closer, err := fetchCSVReader(siteScanningURL)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading site-scanning header: %w", err)
	}

	idx, err := columnIndex(header, "domain", "agency", "uswds_count", "uswds_semantic_version", "uswds_usa_class_list")
	if err != nil {
		return nil, fmt.Errorf("site-scanning file: %w", err)
	}

	var records []SiteScanRecord
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading site-scanning row: %w", err)
		}

		count, _ := strconv.Atoi(strings.TrimSpace(row[idx["uswds_count"]]))

		records = append(records, SiteScanRecord{
			Domain:               strings.ToLower(strings.TrimSpace(row[idx["domain"]])),
			Agency:               row[idx["agency"]],
			UswdsCount:           count,
			UswdsSemanticVersion: row[idx["uswds_semantic_version"]],
			UswdsClasses:         parseClassList(row[idx["uswds_usa_class_list"]]),
		})
	}

	return records, nil
}

// parseClassList parses the uswds_usa_class_list column, which holds either
// a JSON array of detected "usa-*" class names (e.g. `["usa-accordion"]`) or
// a placeholder value ("0" or empty) when no classes were detected.
func parseClassList(field string) []string {
	field = strings.TrimSpace(field)
	if field == "" || field == "0" {
		return nil
	}

	var classes []string
	if err := json.Unmarshal([]byte(field), &classes); err != nil {
		return nil
	}
	return classes
}

// columnIndex builds a name->index map for the requested columns, failing
// fast if any expected column is missing from header.
func columnIndex(header []string, names ...string) (map[string]int, error) {
	positions := make(map[string]int, len(header))
	for i, h := range header {
		positions[h] = i
	}

	idx := make(map[string]int, len(names))
	var missing []string
	for _, name := range names {
		pos, ok := positions[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		idx[name] = pos
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing expected column(s): %s", strings.Join(missing, ", "))
	}

	return idx, nil
}
