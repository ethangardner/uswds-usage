package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
)

// fetchCSVReader downloads url and returns a csv.Reader streaming its body,
// along with the response body so the caller can close it once parsing is done.
func fetchCSVReader(url string) (*csv.Reader, io.Closer, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("fetching %s: unexpected status %s", url, resp.Status)
	}

	r := csv.NewReader(resp.Body)
	r.LazyQuotes = true
	return r, resp.Body, nil
}
