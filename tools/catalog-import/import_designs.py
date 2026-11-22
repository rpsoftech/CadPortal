#!/usr/bin/env python3
"""Creates the designs of a checked design list in the portal and completes them: stone seats, tags, photos, STL and 3dm.

    set PORTAL_EMAIL=you@example.com
    set PORTAL_PASSWORD=...
    python import_designs.py checked.csv --stage work\\part1 --portal https://portal.example --category "Ladies Rings"
    python import_designs.py checked.csv --stage work\\part1 --portal https://portal.example --category "Ladies Rings" --go

checked.csv comes from check_design_list.py, --stage from stage_folders.py. Without --go it only CHECKS (the portal
validates the whole list and nothing is created). With --go it creates what is missing. Running it again is safe: it
only adds what a design still lacks and never replaces anything.

What a design gets
  base input  the printed weight of the first karat the portal has an alloy for (22, then 18, then 14 ...), else the STL
              volume from --volumes (volumes.csv of stl_volume.py), else the design is left out and the reason is printed
  seats       round stones become seats; a coloured round stone and a round stone above 2.00 mm become seats locked to
              STAR; other shapes get no seat and are listed in non_round.csv. A STAR seat whose size has no row in the
              stone matrix yet cannot be made: it is listed in pending_star.csv and added by a later run, once the row exists
  tags        the solitaire tag for one big centre stone; fancy and colour tags (see the options)
  photos      the renders of renders/<code>/ in name order, the first one is the cover; the CAD picture is not uploaded
  files       stl/<code>.stl and cad/<code>.3dm (or .jcd) go to the design's variation
"""
import argparse
import csv
import os
import sys
from decimal import Decimal
from pathlib import Path

import catalog_csv as cc
from check_design_list import read_rows
from portal import Portal, multipart

KARAT_ALLOYS = {22: ["22KY"], 18: ["18KY", "18KR"], 14: ["14KY", "14KR"], 20: ["20KY"], 24: ["24KY"], 10: ["10KY"]}
KARAT_ORDER = [22, 18, 14, 20, 24, 10]
MAX_PHOTOS = 5  # what the portal holds per design


def code_of(folder):
    prefix, number = folder.split("-")
    return f"{prefix.upper()}{int(number):04d}"


def choose_base(e, metals, volume, weight_metal):
    """('weight', grams, alloy) or ('volume', mm3) or None, with the reason when None."""
    by_karat = {k: g for k, g in e.weights if k is not None}
    for karat in KARAT_ORDER:
        if karat in by_karat:
            for alloy in KARAT_ALLOYS.get(karat, []):
                if alloy in metals:
                    return ("weight", by_karat[karat], alloy), ""
    for k, g in e.weights:
        if k is None and weight_metal and weight_metal in metals:
            return ("weight", g, weight_metal), ""
    if volume:
        return ("volume", volume), ""
    if by_karat:
        return None, "its weight is for " + ", ".join(f"{k}K" for k in sorted(by_karat)) + " and the portal has no such alloy (add it, or give --volumes)"
    return None, "it has no weight the portal can use and no STL volume (give --volumes)"


def seats_for(e):
    """(flexible seats, locked seats, non-round stones) as API seat dicts."""
    flexible, locked = [], []
    for s in e.stones:
        if not s.is_round or s.count is None:
            continue
        if s.colored or s.length > cc.MATRIX_MAX_MM:
            locked.append({"size_mm": float(s.length), "stone_count": s.count, "locked_stone_code": "STAR"})
        else:
            flexible.append({"size_mm": float(s.length), "stone_count": s.count})
    return flexible, locked


def tags_for(e, a):
    out = []
    if e.solitaire and a.solitaire_tag:
        out.append(a.solitaire_tag)
    if e.non_round and a.fancy_tag:
        out.append(a.fancy_tag)
    if any(s.colored for s in e.stones) and a.color_tag:
        out.append(a.color_tag)
    return out


def photo_plan(code, stage, have_names, upload_cad_picture):
    """[(path, upload name)] still to upload: the renders in name order (the first is the cover), then the CAD picture."""
    plan = []
    for f in sorted((stage / "renders" / code_dash(code)).glob("*"), key=lambda p: p.name) if (stage / "renders" / code_dash(code)).is_dir() else []:
        name = f"{code}-{f.stem}{f.suffix.lower()}"
        if name not in have_names:
            plan.append((f, name))
    if upload_cad_picture:
        for f in (stage / "pictures").glob(f"{code_dash(code)}.*"):
            name = f"{code}-cad{f.suffix.lower()}"
            if name not in have_names:
                plan.append((f, name))
    return plan[: max(0, MAX_PHOTOS - len(have_names))]


def code_dash(code):
    return f"{code[:2]}-{code[2:]}" if code[:2].isalpha() and code[2:].isdigit() else code


def main(argv=None, portal=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("design_list", help="checked.csv from check_design_list.py")
    ap.add_argument("--stage", required=True, help="the staged layout (pictures, renders, stl, cad) from stage_folders.py")
    ap.add_argument("--portal", required=True)
    ap.add_argument("--category", required=True, help="the category name exactly as in Master data")
    ap.add_argument("--volumes", default="", help="volumes.csv from stl_volume.py: used for a design with no usable weight")
    ap.add_argument("--weight-metal", default="", help="the alloy of a weight printed without a karat (?=2.05), for example 14KY")
    ap.add_argument("--solitaire-tag", default="Sub-Style:Solitaire")
    ap.add_argument("--fancy-tag", default="Attribute:Fancy stone", help="for designs with a stone that is not round; empty for none")
    ap.add_argument("--color-tag", default="Attribute:Color stone", help="for designs with a coloured stone; empty for none")
    ap.add_argument("--upload-cad-picture", action="store_true", help="also upload the CAD picture as the last photo (default: it is only read)")
    ap.add_argument("--offset", type=int, default=0)
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--go", action="store_true", help="really create and complete (without it: check only)")
    a = ap.parse_args(argv)

    stage, out_dir = Path(a.stage), Path(a.design_list).resolve().parent
    rows = read_rows(a.design_list)[a.offset:]
    if a.limit:
        rows = rows[: a.limit]
    evals = [cc.evaluate(r) for r in rows]

    if portal is None:
        email, password = os.environ.get("PORTAL_EMAIL"), os.environ.get("PORTAL_PASSWORD")
        if not email or not password:
            sys.exit("set PORTAL_EMAIL and PORTAL_PASSWORD first")
        portal = Portal(a.portal)
        portal.login(email, password)
    metals = portal.metal_codes()
    have, gone = portal.existing_codes(), portal.deleted_codes()
    nxt = portal.next_number()
    print(f"highest code in the portal (the trash counts too): the next free number is LR{nxt:04d}")

    volumes = {}
    if a.volumes:
        with open(a.volumes, newline="", encoding="utf-8-sig") as fh:
            volumes = {x["code"]: x["volume_mm3"] for x in csv.DictReader(fh) if x.get("volume_mm3") and not (x.get("note") or "").strip()}

    ready, left = [], []
    for e in evals:
        folder = (e.row.get("folder") or "").strip()
        if e.problems:
            left.append(f"{folder}: " + " | ".join(e.problems))
            continue
        code = code_of(folder)
        base, why = choose_base(e, metals, volumes.get(code), a.weight_metal.upper())
        if base is None:
            left.append(f"{folder}: {why}")
            continue
        flexible, locked = seats_for(e)
        ready.append({"e": e, "folder": folder, "code": code, "name": e.row["name"].strip(), "base": base,
                      "flexible": flexible, "locked": locked, "tags": tags_for(e, a)})
    print(f"{len(evals)} designs: {len(ready)} ready, {len(left)} left out")
    for line in left[:15]:
        print("  left out - " + line)
    if len(left) > 15:
        print(f"  ... {len(left) - 15} more")

    wanted_tags = sorted({t for r in ready for t in r["tags"]})
    known_tags = portal.tag_ids()
    if wanted_tags:
        absent = [t for t in wanted_tags if t.lower() not in known_tags]
        print("tags used: " + ", ".join(wanted_tags) + (f"  -> CREATE FIRST under Master data, Tags: {', '.join(absent)}" if absent else " (all exist)"))
    matrix = portal.matrix_pairs()
    star_sizes = sorted({f"{s['size_mm']:.2f}" for r in ready for s in r["locked"]}, key=float)
    missing_star = [s for s in star_sizes if (s, "STAR") not in matrix]
    if star_sizes:
        print(f"STAR seats at {', '.join(star_sizes)} mm" + (f"; the stone matrix has no STAR row for {', '.join(missing_star)} yet, so those seats wait (pending_star.csv)" if missing_star else " (all have a row)"))

    blocked = [r for r in ready if r["code"] in gone]
    if blocked:
        print(f"{len(blocked)} designs are in the trash (their codes stay reserved) and are left out: " + ", ".join(r["code"] for r in blocked[:10]))
    new = [r for r in ready if r["code"] not in have and r["code"] not in gone]
    existing = [r for r in ready if r["code"] in have]
    print(f"{len(existing)} already in the portal (completed below), {len(new)} to create")

    if new:
        body = [{"line": i + 2, "code": r["code"], "name": r["name"], "category": a.category, "tags": ";".join(r["tags"]),
                 "volume_mm3": str(r["base"][1]) if r["base"][0] == "volume" else "",
                 "ref_weight_g": str(r["base"][1]) if r["base"][0] == "weight" else "",
                 "ref_metal": r["base"][2] if r["base"][0] == "weight" else ""} for i, r in enumerate(new)]
        status, js, _ = portal.call("POST", "/api/v1/designs/import", {"dry_run": not a.go, "rows": body})
        if status not in (200, 201):
            print("The server refused the list:")
            for err in (js.get("errors") or [js]):
                line = err.get("line", 0) if isinstance(err, dict) else 0
                who = new[line - 2]["folder"] if 2 <= line < len(new) + 2 else "?"
                print(f"  {who}: {err.get('message', err) if isinstance(err, dict) else err}")
            return 1
        print(f"{'Created' if a.go else 'Check passed for'} {js.get('designs')} designs")

    with open(out_dir / "non_round.csv", "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.writer(fh)
        w.writerow(["code", "name", "stone"])
        for r in ready:
            for s in r["e"].non_round:
                w.writerow([r["code"], r["name"], s.text()])

    pending = []
    todo = {"photos": 0, "stl": 0, "cad": 0, "tags": 0, "seats": 0}
    for r in [x for x in ready if x["code"] not in gone] if a.go else existing:  # a design in the trash is left alone
        code = r["code"]
        try:
            d, _ = portal.ok("GET", "/api/v1/designs/" + code)
        except RuntimeError:
            continue  # a design that was only checked is not there yet
        v = (d.get("variations") or [None])[0]
        if v is None:
            continue
        # seats: the flexible ones and every STAR one whose row exists
        wanted = r["flexible"] + [s for s in r["locked"] if (f"{s['size_mm']:.2f}", "STAR") in matrix]
        for s in r["locked"]:
            if (f"{s['size_mm']:.2f}", "STAR") not in matrix:
                pending.append((code, r["name"], f"{s['size_mm']:.2f}", s["stone_count"]))
        have_seats, _ = portal.ok("GET", f"/api/v1/variations/{v['id']}/seats")
        if wanted and len(have_seats or []) < len(wanted):
            todo["seats"] += 1
            if a.go:
                portal.ok("PUT", f"/api/v1/variations/{v['id']}/seats", {"seats": wanted})
        # tags
        want_ids = {known_tags[t.lower()] for t in r["tags"] if t.lower() in known_tags}
        have_ids = {t["id"] for t in d.get("tags") or []}
        if want_ids - have_ids:
            todo["tags"] += 1
            if a.go:
                portal.ok("PUT", f"/api/v1/designs/{d['id']}", {"name": d["name"], "category_id": d["category_id"], "tag_ids": sorted(have_ids | want_ids)})
        # photos
        plan = photo_plan(code, stage, {m.get("filename") for m in d.get("media") or []}, a.upload_cad_picture)
        if plan:
            todo["photos"] += 1
            if a.go:
                had_none = not d.get("media")
                for i, (path, name) in enumerate(plan):
                    body, ctype = multipart(path, name=name)
                    item, _ = portal.ok("POST", f"/api/v1/designs/{d['id']}/media", raw=body, ctype=ctype)
                    if (i == 0 and had_none) and not item.get("is_cover"):
                        portal.ok("PUT", f"/api/v1/media/{item['id']}/cover")
        # CAD files
        for key, field, files in (("stl", "file_stl_name", list((stage / "stl").glob(f"{code_dash(code)}.stl"))),
                                  ("cad", "file_3dm_name", [f for ext in ("3dm", "jcd") for f in (stage / "cad").glob(f"{code_dash(code)}.{ext}")])):
            if v.get(field) or not files:
                continue
            todo[key] += 1
            if a.go:
                body, ctype = multipart(files[0], ctype="application/octet-stream")
                status, js, _ = portal.call("POST", f"/api/v1/variations/{v['id']}/upload-cad", raw=body, ctype=ctype)
                if status == 409:
                    print(f"{code}: {files[0].name} is the same file as one already stored")
                elif status != 201:
                    print(f"{code}: {files[0].name}: {status} {str(js)[:160]}; run again later")

    with open(out_dir / "pending_star.csv", "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.writer(fh)
        w.writerow(["code", "name", "star_size_mm", "count"])
        w.writerows(pending)
    print(f"\n{'Done' if a.go else 'Still to do'}: {todo['seats']} designs need seats, {todo['tags']} tags, {todo['photos']} photos, {todo['stl']} STL, {todo['cad']} 3dm/jcd."
          + (f" {len(pending)} STAR seats wait for matrix rows (pending_star.csv)." if pending else ""))
    if not a.go:
        print("Check only. Add --go to create and complete them.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
