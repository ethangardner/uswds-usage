package app

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const gsaHistoryRepoURL = "https://github.com/GSA/site-scanning-analysis.git"

func runRefreshHistoryCmd(args []string) error {
	fs := flag.NewFlagSet("refresh-history", flag.ExitOnError)
	historyFile := fs.String("history-file", "data/external/gsa-uswds-report-history.csv", "path to the GSA history CSV to append new rows to")
	repoURL := fs.String("repo-url", gsaHistoryRepoURL, "URL (or local path) of the site-scanning-analysis git repo to read from")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return refreshHistory(*historyFile, *repoURL)
}

func refreshHistory(historyFile, repoURL string) error {
	since, err := lastExtractedDate(historyFile)
	if err != nil {
		return fmt.Errorf("refresh-history: %w", err)
	}
	if since == "" {
		fmt.Fprintln(os.Stderr, "last extracted date: (none -- full history)")
	} else {
		fmt.Fprintf(os.Stderr, "last extracted date: %s\n", since)
	}

	cloneDir, err := os.MkdirTemp("", "site-scanning-analysis")
	if err != nil {
		return fmt.Errorf("refresh-history: %w", err)
	}
	defer os.RemoveAll(cloneDir)

	if _, err := runGit("", "clone", "--filter=blob:none", "--no-checkout", repoURL, cloneDir); err != nil {
		return fmt.Errorf("refresh-history: %w", err)
	}

	logOutput, err := runGit(cloneDir, "log", "--follow", "--format=%H|%ad", "--date=format:%Y-%m-%d", "origin/main", "--", "reports/uswds.csv")
	if err != nil {
		return fmt.Errorf("refresh-history: %w", err)
	}

	var commits [][2]string // {hash, date}, newest first
	for _, line := range strings.Split(strings.TrimSpace(logOutput), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		commits = append(commits, [2]string{parts[0], parts[1]})
	}

	seenDates := make(map[string]bool)
	var newRows []GSAHistoryRow
	for _, c := range commits {
		hash, commitDate := c[0], c[1]
		if since != "" && commitDate <= since {
			continue
		}
		if seenDates[commitDate] {
			continue // keep the newest commit per date (log is reverse-chronological)
		}
		seenDates[commitDate] = true

		stdout, ok, stderr := gitShow(cloneDir, hash+":reports/uswds.csv")
		if !ok {
			fmt.Fprintf(os.Stderr, "skip %s %s: %s\n", commitDate, hash, stderr)
			continue
		}

		rows, err := parseGSABlob(stdout, commitDate, hash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s %s: %s\n", commitDate, hash, err)
			continue
		}
		newRows = append(newRows, rows...)
	}

	if len(newRows) == 0 {
		fmt.Fprintln(os.Stderr, "no new dates to add")
		return nil
	}

	newDates := make(map[string]bool, len(newRows))
	for _, r := range newRows {
		newDates[r.ReportDate] = true
	}
	sortedNewDates := make([]string, 0, len(newDates))
	for d := range newDates {
		sortedNewDates = append(sortedNewDates, d)
	}
	sort.Strings(sortedNewDates)
	fmt.Fprintf(os.Stderr, "adding %d rows across %d new dates: %s..%s\n",
		len(newRows), len(sortedNewDates), sortedNewDates[0], sortedNewDates[len(sortedNewDates)-1])

	sort.SliceStable(newRows, func(i, j int) bool { return newRows[i].ReportDate < newRows[j].ReportDate })

	return appendGSAHistoryRows(historyFile, newRows)
}

// lastExtractedDate returns the string-max report_date already present in
// historyFile, or "" (Python's None) if the file doesn't exist or is empty.
func lastExtractedDate(historyFile string) (string, error) {
	if _, err := os.Stat(historyFile); os.IsNotExist(err) {
		return "", nil
	}
	rows, err := readGSAHistoryCSV(historyFile)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	max := rows[0].ReportDate
	for _, r := range rows[1:] {
		if r.ReportDate > max {
			max = r.ReportDate
		}
	}
	return max, nil
}

// runGit runs a git command to completion and fails fast on a non-zero
// exit, mirroring Python's run() helper (used for clone + log).
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// gitShow runs `git show <spec>` without failing fast, mirroring Python's
// raw subprocess.run(...) call for `git show`: the caller decides whether to
// log-and-skip on a non-zero exit rather than aborting the whole run.
func gitShow(dir, spec string) (stdout string, ok bool, stderrTrimmed string) {
	cmd := exec.Command("git", "show", spec)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	return out.String(), err == nil, strings.TrimSpace(errBuf.String())
}

// headerIndex builds a name->column-index map for a historical GSA blob's
// header row. Unlike columnIndex (sitescanning.go), this is lenient: a
// missing column just means fieldOrEmpty returns "" for it, since older
// GSA report snapshots don't all carry the same columns.
func headerIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i // last occurrence wins, matching csv.DictReader
	}
	return idx
}

func fieldOrEmpty(row []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}

// parseGSABlob parses one historical reports/uswds.csv blob (as returned by
// `git show <commit>:reports/uswds.csv`) into GSAHistoryRows tagged with
// commitDate/hash. FieldsPerRecord is relaxed to -1 because historical
// snapshots are schema-evolving and may be ragged in ways Python's lenient
// DictReader tolerates but Go's default strict row-length check would not.
func parseGSABlob(blob, commitDate, hash string) ([]GSAHistoryRow, error) {
	r := csv.NewReader(strings.NewReader(blob))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("unexpected header (%w)", err)
	}
	idx := headerIndex(header)
	if _, ok := idx["Group"]; !ok {
		return nil, fmt.Errorf("unexpected header %v", header)
	}

	var rows []GSAHistoryRow
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		out := GSAHistoryRow{ReportDate: commitDate, Commit: hash}
		for _, m := range gsaFieldMap {
			v := fieldOrEmpty(row, idx, m.Src)
			switch m.Dst {
			case "group":
				out.Group = v
			case "count":
				out.Count = v
			case "agencies":
				out.Agencies = v
			case "semantic_version":
				out.SemanticVersion = v
			case "agencies_sv":
				out.AgenciesSV = v
			case "v1_x":
				out.V1X = v
			case "agencies_v1":
				out.AgenciesV1 = v
			case "v2_x":
				out.V2X = v
			case "agencies_v2":
				out.AgenciesV2 = v
			case "v3_x":
				out.V3X = v
			case "agencies_v3":
				out.AgenciesV3 = v
			case "banner":
				out.Banner = v
			case "agencies_banner":
				out.AgenciesBanner = v
			case "usa_class":
				out.UsaClass = v
			case "agencies_usa_class":
				out.AgenciesUsaClass = v
			}
		}
		rows = append(rows, out)
	}

	return rows, nil
}

// appendGSAHistoryRows appends rows to historyFile, writing a header only if
// the file didn't already exist. UseCRLF=true is a deliberate deviation from
// every other CSV writer in this codebase (all others default to "\n") --
// the real checked-in history file is entirely CRLF-terminated, an artifact
// of the original Python script's csv module. Do not "fix" this to match
// the other writers; doing so would split the file's line-ending convention
// mid-file.
func appendGSAHistoryRows(historyFile string, rows []GSAHistoryRow) error {
	writeHeader := false
	if _, err := os.Stat(historyFile); os.IsNotExist(err) {
		writeHeader = true
	}

	if dir := filepath.Dir(historyFile); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	f, err := os.OpenFile(historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", historyFile, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	w.UseCRLF = true
	defer w.Flush()

	if writeHeader {
		if err := w.Write(gsaHistoryColumns); err != nil {
			return err
		}
	}

	for _, r := range rows {
		if err := w.Write([]string{
			r.ReportDate, r.Commit, r.Group, r.Count, r.Agencies, r.SemanticVersion,
			r.AgenciesSV, r.V1X, r.AgenciesV1, r.V2X, r.AgenciesV2, r.V3X, r.AgenciesV3,
			r.Banner, r.AgenciesBanner, r.UsaClass, r.AgenciesUsaClass,
		}); err != nil {
			return err
		}
	}

	return w.Error()
}
