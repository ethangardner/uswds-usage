package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
)

// FetchMeta captures response provenance from an upstream CSV fetch (response
// headers and, when the caller records it, the CSV's own header row), so it
// can be written into a snapshot's meta.json for later provenance checks.
type FetchMeta struct {
	Header  http.Header
	Columns []string
}

// fetchCSVReader downloads url and returns a csv.Reader streaming its body,
// the response body so the caller can close it once parsing is done, and the
// response headers (used to record Last-Modified/ETag provenance).
func fetchCSVReader(url string) (*csv.Reader, io.Closer, http.Header, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, nil, fmt.Errorf("fetching %s: unexpected status %s", url, resp.Status)
	}

	r := csv.NewReader(resp.Body)
	r.LazyQuotes = true
	return r, resp.Body, resp.Header, nil
}
