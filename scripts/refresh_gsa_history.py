#!/usr/bin/env python3
"""Append new rows to external-data/gsa-uswds-report-history.csv from GSA's
site-scanning-analysis repo (reports/uswds.csv), without re-processing dates
already extracted.

Run monthly (see .github/workflows/monthly-report.yml) or by hand:

    python3 scripts/refresh_gsa_history.py

It does a blobless partial clone/fetch of the upstream repo (so a full run
touches network for metadata only, then lazily fetches just the blobs for
new commits), so this stays cheap even though the upstream history is long.
"""
import csv
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_URL = "https://github.com/GSA/site-scanning-analysis.git"
REPO_ROOT = Path(__file__).resolve().parent.parent
HISTORY_FILE = REPO_ROOT / "external-data" / "gsa-uswds-report-history.csv"

FIELD_MAP = {
    "Group": "group",
    "count": "count",
    "Agencies": "agencies",
    "semantic version": "semantic_version",
    "Agencies_sv": "agencies_sv",
    "v1.x": "v1_x",
    "Agencies_v1": "agencies_v1",
    "v2.x": "v2_x",
    "Agencies_v2": "agencies_v2",
    "v3.x": "v3_x",
    "Agencies_v3": "agencies_v3",
    "banner": "banner",
    "Agencies_banner": "agencies_banner",
    "usa-class": "usa_class",
    "Agencies_usa_class": "agencies_usa_class",
}
OUT_COLUMNS = ["report_date", "commit"] + list(FIELD_MAP.values())


def run(cmd, cwd=None):
    result = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed: {result.stderr.strip()}")
    return result.stdout


def last_extracted_date():
    if not HISTORY_FILE.exists():
        return None
    with open(HISTORY_FILE) as f:
        rows = list(csv.DictReader(f))
    if not rows:
        return None
    return max(r["report_date"] for r in rows)


def main():
    since = last_extracted_date()
    print(f"last extracted date: {since or '(none -- full history)'}", file=sys.stderr)

    with tempfile.TemporaryDirectory() as tmp:
        clone_dir = Path(tmp) / "site-scanning-analysis"
        run(["git", "clone", "--filter=blob:none", "--no-checkout", REPO_URL, str(clone_dir)])

        log_output = run(
            [
                "git", "log", "--follow", "--format=%H|%ad", "--date=format:%Y-%m-%d",
                "origin/main", "--", "reports/uswds.csv",
            ],
            cwd=clone_dir,
        )
        commits = [line.split("|", 1) for line in log_output.strip().splitlines() if line.strip()]

        seen_dates = set()
        new_rows = []
        skipped = 0
        for commit_hash, commit_date in commits:  # newest first
            if since and commit_date <= since:
                continue
            if commit_date in seen_dates:
                skipped += 1
                continue  # keep the newest commit per date (log is reverse-chronological)
            seen_dates.add(commit_date)

            result = subprocess.run(
                ["git", "show", f"{commit_hash}:reports/uswds.csv"],
                cwd=clone_dir, capture_output=True, text=True,
            )
            if result.returncode != 0:
                print(f"skip {commit_date} {commit_hash}: {result.stderr.strip()}", file=sys.stderr)
                continue

            reader = csv.DictReader(result.stdout.splitlines())
            if reader.fieldnames is None or "Group" not in reader.fieldnames:
                print(f"skip {commit_date} {commit_hash}: unexpected header {reader.fieldnames}", file=sys.stderr)
                continue

            for row in reader:
                out_row = {"report_date": commit_date, "commit": commit_hash}
                for src, dst in FIELD_MAP.items():
                    out_row[dst] = row.get(src, "")
                new_rows.append(out_row)

    if not new_rows:
        print("no new dates to add", file=sys.stderr)
        return

    new_dates = sorted(set(r["report_date"] for r in new_rows))
    print(f"adding {len(new_rows)} rows across {len(new_dates)} new dates: {new_dates[0]}..{new_dates[-1]}", file=sys.stderr)

    write_header = not HISTORY_FILE.exists()
    HISTORY_FILE.parent.mkdir(parents=True, exist_ok=True)
    with open(HISTORY_FILE, "a", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=OUT_COLUMNS)
        if write_header:
            writer.writeheader()
        # keep the file sorted by date even though we only append forward in practice
        for row in sorted(new_rows, key=lambda r: r["report_date"]):
            writer.writerow(row)


if __name__ == "__main__":
    main()
