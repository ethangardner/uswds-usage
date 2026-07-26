package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
)

// ReportRow is one joined USWDS-site + traffic entry in the final report.
type ReportRow struct {
	Domain               string
	Agency               string
	UswdsSemanticVersion string
	Classes              []string
	Pageviews            int
	Visits               int
	HasTrafficData       bool
}

// ClassCount is how many distinct USWDS-using domains use a given "usa-*" class.
type ClassCount struct {
	Class string
	Sites int
}

// buildReport dedupes siteScans down to one row per domain (the source data
// contains a row per agency/bureau catalog entry, so popular shared domains
// like secure.login.gov appear dozens of times), filters to USWDS-using
// domains, and joins each against analytics traffic data by lowercased
// domain/hostname, sorted descending by pageviews.
func buildReport(siteScans []SiteScanRecord, analytics map[string]AnalyticsRecord) []ReportRow {
	type agg struct {
		usesUSWDS bool
		agency    string
		version   string
		classes   map[string]bool
	}

	byDomain := make(map[string]*agg)
	var order []string
	for _, s := range siteScans {
		a, ok := byDomain[s.Domain]
		if !ok {
			a = &agg{classes: make(map[string]bool)}
			byDomain[s.Domain] = a
			order = append(order, s.Domain)
		}
		if s.UsesUSWDS() {
			a.usesUSWDS = true
		}
		if a.agency == "" {
			a.agency = s.Agency
		}
		if a.version == "" {
			a.version = s.UswdsSemanticVersion
		}
		for _, c := range s.UswdsClasses {
			a.classes[c] = true
		}
	}

	var rows []ReportRow
	for _, domain := range order {
		a := byDomain[domain]
		if !a.usesUSWDS {
			continue
		}

		classes := make([]string, 0, len(a.classes))
		for c := range a.classes {
			classes = append(classes, c)
		}
		sort.Strings(classes)

		row := ReportRow{
			Domain:               domain,
			Agency:               a.agency,
			UswdsSemanticVersion: a.version,
			Classes:              classes,
		}
		if traffic, ok := analytics[domain]; ok {
			row.Pageviews = traffic.Pageviews
			row.Visits = traffic.Visits
			row.HasTrafficData = true
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Pageviews > rows[j].Pageviews
	})

	return rows
}

// AgencyStats summarizes how many agencies and subagencies (bureaus) have at
// least one USWDS-using site, per the raw (non-deduped) site-scanning rows.
// Raw rows are used rather than deduped ReportRows because a single shared
// domain (e.g. secure.login.gov) is scanned once per agency/bureau that uses
// it, and each of those agencies genuinely counts as "using USWDS" via that
// shared site.
type AgencyStats struct {
	Agencies    []string
	Subagencies int // distinct (agency, bureau) pairs with a non-empty bureau
}

func agencyStats(siteScans []SiteScanRecord) AgencyStats {
	agencySet := make(map[string]bool)
	subagencySet := make(map[string]bool)

	for _, s := range siteScans {
		if !s.UsesUSWDS() {
			continue
		}
		if s.Agency != "" {
			agencySet[s.Agency] = true
		}
		if s.Bureau != "" {
			subagencySet[s.Agency+"\x00"+s.Bureau] = true
		}
	}

	agencies := make([]string, 0, len(agencySet))
	for a := range agencySet {
		agencies = append(agencies, a)
	}
	sort.Strings(agencies)

	return AgencyStats{Agencies: agencies, Subagencies: len(subagencySet)}
}

func printAgencyStats(stats AgencyStats) {
	fmt.Printf("\nAgencies using USWDS:        %d\n", len(stats.Agencies))
	fmt.Printf("Subagencies (bureaus) using USWDS: %d\n", stats.Subagencies)
}

// classFrequency counts, across all USWDS-using domains, how many domains
// use each detected "usa-*" class, sorted descending by site count.
func classFrequency(rows []ReportRow) []ClassCount {
	counts := make(map[string]int)
	for _, r := range rows {
		for _, c := range r.Classes {
			counts[c]++
		}
	}

	result := make([]ClassCount, 0, len(counts))
	for class, n := range counts {
		result = append(result, ClassCount{Class: class, Sites: n})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Sites != result[j].Sites {
			return result[i].Sites > result[j].Sites
		}
		return result[i].Class < result[j].Class
	})

	return result
}

func printClassFrequency(counts []ClassCount, top int) {
	if top <= 0 || len(counts) == 0 {
		return
	}
	if top > len(counts) {
		top = len(counts)
	}

	fmt.Printf("\nMost common USWDS classes (top %d, by number of sites):\n", top)
	fmt.Printf("%-35s %10s\n", "CLASS", "SITES")
	for _, c := range counts[:top] {
		fmt.Printf("%-35s %10d\n", c.Class, c.Sites)
	}
}

func writeReportCSV(path string, rows []ReportRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{"domain", "agency", "uswds_semantic_version", "pageviews", "visits"}); err != nil {
		return err
	}

	for _, r := range rows {
		if err := w.Write([]string{
			r.Domain,
			r.Agency,
			r.UswdsSemanticVersion,
			fmt.Sprintf("%d", r.Pageviews),
			fmt.Sprintf("%d", r.Visits),
		}); err != nil {
			return err
		}
	}

	return w.Error()
}

func printSummary(rows []ReportRow, top int) {
	matched := 0
	var totalPageviews, totalVisits int
	for _, r := range rows {
		if r.HasTrafficData {
			matched++
		}
		totalPageviews += r.Pageviews
		totalVisits += r.Visits
	}

	fmt.Printf("USWDS sites detected:        %d\n", len(rows))
	fmt.Printf("Matched to traffic data:     %d\n", matched)
	fmt.Printf("Total pageviews (30-day):    %d\n", totalPageviews)
	fmt.Printf("Total visits (30-day):       %d\n", totalVisits)

	if top <= 0 || len(rows) == 0 {
		return
	}
	if top > len(rows) {
		top = len(rows)
	}

	fmt.Printf("\nTop %d USWDS sites by pageviews:\n", top)
	fmt.Printf("%-35s %-45s %10s %10s\n", "DOMAIN", "AGENCY", "PAGEVIEWS", "VISITS")
	for _, r := range rows[:top] {
		agency := r.Agency
		if len(agency) > 45 {
			agency = agency[:42] + "..."
		}
		fmt.Printf("%-35s %-45s %10d %10d\n", r.Domain, agency, r.Pageviews, r.Visits)
	}
}
