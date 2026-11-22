#!/usr/bin/env python3
"""Checks a design list (the CSV an agent prepared) against the rules in catalog_csv.py and writes a checked copy.

    python check_design_list.py design_list.csv                  prints the problems
    python check_design_list.py design_list.csv --out checked.csv   also writes checked.csv for the importer

The checked copy has the same columns, with the stone sizes rounded to the 0.05 step and the solitaire column filled
in (yes or no), plus three more: check (ok, or every problem), star_locked (the seats that become STAR seats) and
non_round (the stones that get no seat). The exit code is 1 when any row has a problem, so it can gate a script.
"""
import argparse
import csv
import sys
from pathlib import Path

import catalog_csv as cc


def read_rows(path):
    with open(path, newline="", encoding="utf-8-sig") as fh:
        rd = csv.DictReader(fh)
        header = [h.strip() for h in (rd.fieldnames or [])]
        if header[: len(cc.COLUMNS)] != cc.COLUMNS:
            sys.exit(f"the header must start with: {','.join(cc.COLUMNS)}\nit is: {','.join(header)}")
        return [{k.strip(): (v or "").strip() for k, v in r.items() if k} for r in rd]


def check(rows, round_sizes=True):
    out, seen = [], set()
    for r in rows:
        e = cc.evaluate(r, round_sizes)
        if r.get("folder") in seen:
            e.problems.append("this folder is listed twice")
        seen.add(r.get("folder"))
        out.append(e)
    return out


def checked_rows(evals):
    rows = []
    for e in evals:
        r = dict(e.row)
        r["stones"] = "; ".join(s.text() for s in e.stones)
        r["solitaire"] = "yes" if e.solitaire else "no"
        notes = [r.get("notes", "")] if r.get("notes") else []
        if e.rounded:
            notes.append("sizes rounded to the nearest 0.05 mm: " + ", ".join(e.rounded))
        r["notes"] = "; ".join(notes)
        r["check"] = " | ".join(e.problems) if e.problems else "ok"
        r["star_locked"] = "; ".join(s.text() for s in e.star_locked)
        r["non_round"] = "; ".join(s.text() for s in e.non_round)
        rows.append(r)
    return rows


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("design_list")
    ap.add_argument("--out", default="")
    ap.add_argument("--no-round-sizes", action="store_true", help="leave odd sizes as they are (they are then problems only if outside the allowed range)")
    a = ap.parse_args(argv)
    evals = check(read_rows(a.design_list), not a.no_round_sizes)
    bad = [e for e in evals if e.problems]
    for e in bad:
        print(f"{e.row.get('folder') or '(no folder)'}: " + " | ".join(e.problems))
    sol = sum(1 for e in evals if e.solitaire)
    star = sorted({s.text().split("x")[0] for e in evals for s in e.big_round}, key=float)
    print(f"\n{len(evals)} designs: {len(evals) - len(bad)} ok, {len(bad)} with problems; {sol} solitaires; "
          f"{sum(1 for e in evals if e.non_round)} with non-round stones")
    if star:
        print("STAR matrix rows needed (round stones above " + str(cc.MATRIX_MAX_MM) + " mm, mm): " + ", ".join(star))
    if a.out:
        rows = checked_rows(evals)
        with open(a.out, "w", newline="", encoding="utf-8-sig") as fh:
            w = csv.DictWriter(fh, fieldnames=cc.COLUMNS + ["check", "star_locked", "non_round"])
            w.writeheader()
            w.writerows(rows)
        print(f"written to {a.out}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
