package app

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ReportRow is one joined USWDS-site + traffic entry in the final report.
type ReportRow struct {
	Domain               string
	Agency               string
	UswdsSemanticVersion string
	Classes              []string
	Elements             []string
	Pageviews            int
	Visits               int
	HasTrafficData       bool
	// ScanDate is the most recent GSA scan_date across this domain's raw
	// site-scanning rows -- the real as-of date of the detection, distinct
	// from whenever this tool happens to run.
	ScanDate string
}

// UsesClasses reports whether the site was detected using class-based USWDS markup.
func (r ReportRow) UsesClasses() bool {
	return len(r.Classes) > 0
}

// UsesElements reports whether the site was detected using USWDS custom elements (web components).
func (r ReportRow) UsesElements() bool {
	return len(r.Elements) > 0
}

// UsageCount is how many distinct USWDS-using domains use a given "usa-*"
// class or USWDS custom element.
type UsageCount struct {
	Name  string
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
		scanDate  string
		classes   map[string]bool
		elements  map[string]bool
	}

	byDomain := make(map[string]*agg)
	var order []string
	for _, s := range siteScans {
		a, ok := byDomain[s.Domain]
		if !ok {
			a = &agg{classes: make(map[string]bool), elements: make(map[string]bool)}
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
		if s.ScanDate > a.scanDate {
			a.scanDate = s.ScanDate
		}
		for _, c := range s.UswdsClasses {
			a.classes[c] = true
		}
		for _, e := range s.UswdsElements {
			a.elements[e] = true
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

		elements := make([]string, 0, len(a.elements))
		for e := range a.elements {
			elements = append(elements, e)
		}
		sort.Strings(elements)

		row := ReportRow{
			Domain:               domain,
			Agency:               a.agency,
			UswdsSemanticVersion: a.version,
			Classes:              classes,
			Elements:             elements,
			ScanDate:             a.scanDate,
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

// TopNCoverage summarizes, for a fixed N, how much of the highest-traffic
// .gov domain cohort (by 30-day pageviews) is USWDS-adopting. Unlike the main
// report -- which only lists adopting domains -- this looks at the full
// analytics.usa.gov domain universe, so it can compute a true coverage ratio
// rather than just a count of adopters.
type TopNCoverage struct {
	N        int // domains actually considered, <= the requested N
	Adopting int
}

// computeTopNCoverage ranks .gov domains from analytics by 30-day pageviews,
// takes the top n, and reports how many of those are USWDS-adopting per
// siteScans.
func computeTopNCoverage(siteScans []SiteScanRecord, analytics map[string]AnalyticsRecord, n int) TopNCoverage {
	adopting := make(map[string]bool)
	for _, s := range siteScans {
		if s.UsesUSWDS() {
			adopting[s.Domain] = true
		}
	}

	type domainTraffic struct {
		domain    string
		pageviews int
	}
	var govDomains []domainTraffic
	for domain, rec := range analytics {
		if strings.HasSuffix(domain, ".gov") {
			govDomains = append(govDomains, domainTraffic{domain, rec.Pageviews})
		}
	}
	sort.Slice(govDomains, func(i, j int) bool { return govDomains[i].pageviews > govDomains[j].pageviews })

	if n > len(govDomains) {
		n = len(govDomains)
	}

	cov := TopNCoverage{N: n}
	for _, d := range govDomains[:n] {
		if adopting[d.domain] {
			cov.Adopting++
		}
	}
	return cov
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

// frequency counts, across all USWDS-using domains, how many domains use
// each name returned by selector (e.g. a class or element name), sorted
// descending by site count.
func frequency(rows []ReportRow, selector func(ReportRow) []string) []UsageCount {
	counts := make(map[string]int)
	for _, r := range rows {
		for _, name := range selector(r) {
			counts[name]++
		}
	}

	result := make([]UsageCount, 0, len(counts))
	for name, n := range counts {
		result = append(result, UsageCount{Name: name, Sites: n})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Sites != result[j].Sites {
			return result[i].Sites > result[j].Sites
		}
		return result[i].Name < result[j].Name
	})

	return result
}

// classFrequency counts, across all USWDS-using domains, how many domains
// use each detected "usa-*" class.
func classFrequency(rows []ReportRow) []UsageCount {
	return frequency(rows, func(r ReportRow) []string { return r.Classes })
}

// elementFrequency counts, across all USWDS-using domains, how many domains
// use each detected USWDS custom element (web component).
func elementFrequency(rows []ReportRow) []UsageCount {
	return frequency(rows, func(r ReportRow) []string { return r.Elements })
}

func printUsageFrequency(label string, counts []UsageCount, top int) {
	if top <= 0 || len(counts) == 0 {
		return
	}
	if top > len(counts) {
		top = len(counts)
	}

	fmt.Printf("\nMost common %s (top %d, by number of sites):\n", label, top)
	fmt.Printf("%-35s %10s\n", strings.ToUpper(label), "SITES")
	for _, c := range counts[:top] {
		fmt.Printf("%-35s %10d\n", c.Name, c.Sites)
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

	if err := w.Write([]string{"domain", "agency", "uswds_semantic_version", "uses_classes", "uses_elements", "pageviews", "visits"}); err != nil {
		return err
	}

	for _, r := range rows {
		if err := w.Write([]string{
			r.Domain,
			r.Agency,
			r.UswdsSemanticVersion,
			fmt.Sprintf("%t", r.UsesClasses()),
			fmt.Sprintf("%t", r.UsesElements()),
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

	printTopSites("Top %d USWDS sites by pageviews:", rows, top)
}

// elementsOnlyRows returns the subset of rows using USWDS custom elements
// (web components) but no class-based USWDS markup.
func elementsOnlyRows(rows []ReportRow) []ReportRow {
	var result []ReportRow
	for _, r := range rows {
		if r.UsesElements() && !r.UsesClasses() {
			result = append(result, r)
		}
	}
	return result
}

// printElementsOnlySummary reports on the cohort of sites using USWDS custom
// elements but no class-based markup: how many there are, their combined
// traffic, and a top-N table by pageviews.
func printElementsOnlySummary(rows []ReportRow, top int) {
	elementsOnly := elementsOnlyRows(rows)

	matched := 0
	var totalPageviews, totalVisits int
	for _, r := range elementsOnly {
		if r.HasTrafficData {
			matched++
		}
		totalPageviews += r.Pageviews
		totalVisits += r.Visits
	}

	fmt.Printf("\nSites using USWDS elements but no classes: %d\n", len(elementsOnly))
	fmt.Printf("Matched to traffic data:                   %d\n", matched)
	fmt.Printf("Total pageviews (30-day):                  %d\n", totalPageviews)
	fmt.Printf("Total visits (30-day):                     %d\n", totalVisits)

	printTopSites("Top %d elements-only USWDS sites by pageviews:", elementsOnly, top)
}

// printTopSites prints a pageviews-sorted table of up to top rows.
// titleFormat must contain exactly one %d verb for the row count.
func printTopSites(titleFormat string, rows []ReportRow, top int) {
	if top <= 0 || len(rows) == 0 {
		return
	}
	if top > len(rows) {
		top = len(rows)
	}

	fmt.Printf("\n"+titleFormat+"\n", top)
	fmt.Printf("%-35s %-45s %10s %10s\n", "DOMAIN", "AGENCY", "PAGEVIEWS", "VISITS")
	for _, r := range rows[:top] {
		agency := r.Agency
		if len(agency) > 45 {
			agency = agency[:42] + "..."
		}
		fmt.Printf("%-35s %-45s %10d %10d\n", r.Domain, agency, r.Pageviews, r.Visits)
	}
}
