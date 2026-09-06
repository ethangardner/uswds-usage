package app

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const analyticsURL = "https://analytics.usa.gov/data/live/top-100000-domains-30-days.csv"

// AnalyticsRecord holds 30-day traffic totals for a hostname.
type AnalyticsRecord struct {
	Pageviews int
	Visits    int
}

// fetchAnalyticsRecords returns a map of lowercased hostname -> traffic
// totals, along with the response headers (Last-Modified/ETag provenance).
func fetchAnalyticsRecords() (map[string]AnalyticsRecord, http.Header, error) {
	r, closer, respHeader, err := fetchCSVReader(analyticsURL)
	if err != nil {
		return nil, nil, err
	}
	defer closer.Close()

	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("reading analytics header: %w", err)
	}

	idx, err := columnIndex(header, "hostname", "pageviews", "visits")
	if err != nil {
		return nil, nil, fmt.Errorf("analytics file: %w", err)
	}

	records := make(map[string]AnalyticsRecord)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading analytics row: %w", err)
		}

		hostname := strings.ToLower(strings.TrimSpace(row[idx["hostname"]]))
		pageviews, _ := strconv.Atoi(strings.TrimSpace(row[idx["pageviews"]]))
		visits, _ := strconv.Atoi(strings.TrimSpace(row[idx["visits"]]))

		records[hostname] = AnalyticsRecord{Pageviews: pageviews, Visits: visits}
	}

	return records, respHeader, nil
}
