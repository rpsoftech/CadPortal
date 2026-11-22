#!/usr/bin/env python3
"""Stages a folder of design folders into the flat layout the importer reads, with the portal's own numbers.

    python stage_folders.py "D:\\solitaires\\Part 1" --start 598 --out work\\part1            show what it would do
    python stage_folders.py "D:\\solitaires\\Part 1" --start 598 --out work\\part1 --go       copy the files

The source folder holds one folder per design. Each design folder has one .stl, one .3dm (or .jcd), the CAD picture
(the screenshot with the gem report) and many renders. Nothing in the source is changed: the files are COPIED.

Per design it writes, under --out (LR-0598 is the new number):

    pictures/LR-0598.jpg          the CAD picture: only read to prepare the design list, never uploaded
    renders/LR-0598/1-yellow.png  one render per metal, yellow then rose then white, then the model photo, so the
                  2-rose.png      first by name (the yellow one) is the cover
                  3-white.png
                  4-model.jpg
    stl/LR-0598.stl
    cad/LR-0598.3dm               a .jcd keeps its extension; both live in the same slot of the portal
    stage_map.csv                 the new number, the source folder and the files chosen: keep it until you have checked
                                  the import, then you can delete it (the portal does not keep the supplier code)

The numbers follow the natural order of the folder names (2 before 10). Start after the highest code the portal has
used; the importer prints it as "the next free number". Stage one part, import it, then stage the next.

Which render is chosen: the metals are Y (yellow), R (rose) and W (white); for each the view 3D is preferred, then P,
then T (change with --views), and a PNG is preferred over a JPG. The model photo is WH (a hand wearing the ring).
"""
import argparse
import csv
import re
import shutil
import sys
from pathlib import Path

IMG = {".png", ".jpg", ".jpeg", ".webp"}
METALS = [("Y", "yellow"), ("R", "rose"), ("W", "white")]
RENDER = re.compile(r"^(?P<metal>[YRW])(?P<view>3D|P|T)$", re.I)


def natural(s):
    return [int(t) if t.isdigit() else t.lower() for t in re.split(r"(\d+)", s)]


def pick_render(files, metal, views):
    """The best render of one metal: the first wanted view, a PNG before a JPG. None when there is none."""
    for view in views:
        for ext in (".png", ".jpg", ".jpeg", ".webp"):
            for f in files:
                m = RENDER.match(f.stem)
                if m and m["metal"].upper() == metal and m["view"].upper() == view.upper() and f.suffix.lower() == ext:
                    return f
    return None


def pick_model(files):
    for ext in (".png", ".jpg", ".jpeg", ".webp"):
        for f in files:
            if f.stem.upper() == "WH" and f.suffix.lower() == ext:
                return f
    return None


def pick_cad_picture(folder, files, native):
    """The CAD screenshot: the picture named like the 3dm, else like the folder; never a render."""
    stems = [s for s in ([native.stem] if native else []) + [folder.name] if s]
    for stem in stems:
        for ext in (".jpg", ".jpeg", ".png"):
            for f in files:
                if f.stem.lower() == stem.lower() and f.suffix.lower() == ext:
                    return f
    others = [f for f in files if not RENDER.match(f.stem) and f.stem.upper() != "WH" and f.suffix.lower() in IMG]
    return others[0] if len(others) == 1 else None


def inspect_design(folder: Path, views):
    """What a design folder holds and the files chosen, with the problems found."""
    files = sorted((f for f in folder.iterdir() if f.is_file()), key=lambda f: natural(f.name))
    stls = [f for f in files if f.suffix.lower() == ".stl"]
    nat = [f for f in files if f.suffix.lower() in (".3dm", ".jcd")]
    imgs = [f for f in files if f.suffix.lower() in IMG]
    problems = []
    if len(stls) != 1:
        problems.append(f"{len(stls)} stl files")
    if len(nat) != 1:
        problems.append(f"{len(nat)} 3dm/jcd files")
    stl, native = (stls[0] if stls else None), (nat[0] if nat else None)
    cad = pick_cad_picture(folder, imgs, native)
    if not cad:
        problems.append("no CAD picture")
    chosen = []
    for letter, label in METALS:
        r = pick_render(imgs, letter, views)
        if r:
            chosen.append((label, r))
        else:
            problems.append(f"no {label} render")
    model = pick_model(imgs)
    if model:
        chosen.append(("model", model))
    else:
        problems.append("no model photo")
    return {"stl": stl, "native": native, "cad": cad, "renders": chosen, "problems": problems}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("source", help="the folder that holds one folder per design")
    ap.add_argument("--start", type=int, required=True, help="the first number to use, for example 598 gives LR-0598")
    ap.add_argument("--prefix", default="LR")
    ap.add_argument("--out", default="work", help="where the staged layout is written")
    ap.add_argument("--views", default="3D,P,T", help="the views to prefer for the metal renders, best first")
    ap.add_argument("--only-complete", action="store_true", help="skip design folders that miss a file (default: stage what they have)")
    ap.add_argument("--go", action="store_true", help="really copy (without it: show only)")
    a = ap.parse_args(argv)

    src, out = Path(a.source), Path(a.out)
    if not src.is_dir():
        sys.exit(f"folder not found: {a.source}  (check the path; put it in quotes if it has spaces)")
    if (out / "stage_map.csv").exists() and a.go:
        sys.exit(f"{out / 'stage_map.csv'} exists: this part was staged already. Use another --out, or delete the folder first.")
    folders = sorted((p for p in src.iterdir() if p.is_dir() and not p.name.startswith(".")), key=lambda p: natural(p.name))
    views = [v.strip() for v in a.views.split(",") if v.strip()]
    plan, rows = [], []
    for i, folder in enumerate(folders):
        info = inspect_design(folder, views)
        code = f"{a.prefix}-{a.start + i:04d}"
        if a.only_complete and info["problems"]:
            rows.append({"code": code, "source": folder.name, "status": "skipped: " + "; ".join(info["problems"])})
            continue
        plan.append((code, folder, info))
        rows.append({"code": code, "source": folder.name, "status": "ok" if not info["problems"] else "incomplete: " + "; ".join(info["problems"]),
                     "stl": info["stl"].name if info["stl"] else "", "cad": info["native"].name if info["native"] else "",
                     "picture": info["cad"].name if info["cad"] else "",
                     "renders": ", ".join(f"{l}={f.name}" for l, f in info["renders"])})

    last = a.start + len(folders) - 1
    print(f"{len(folders)} design folders in {src}: {a.prefix}-{a.start:04d} to {a.prefix}-{last:04d}" if folders else "no design folders found")
    bad = [r for r in rows if r["status"] != "ok"]
    print(f"  {len(rows) - len(bad)} complete, {len(bad)} with something missing")
    for r in bad[:15]:
        print(f"  {r['code']} <- {r['source']}: {r['status']}")
    if len(bad) > 15:
        print(f"  ... {len(bad) - 15} more (see stage_map.csv)")
    for r in rows[:6]:
        print(f"  {r['source']:<28} -> {r['code']}")
    if len(rows) > 6:
        print(f"  ... {len(rows) - 6} more")
    if not a.go:
        print("\nNothing was copied. Add --go to do it.")
        return

    for sub in ("pictures", "renders", "stl", "cad"):
        (out / sub).mkdir(parents=True, exist_ok=True)
    for code, folder, info in plan:
        if info["cad"]:
            shutil.copy2(info["cad"], out / "pictures" / f"{code}{info['cad'].suffix.lower()}")
        if info["stl"]:
            shutil.copy2(info["stl"], out / "stl" / f"{code}.stl")
        if info["native"]:
            shutil.copy2(info["native"], out / "cad" / f"{code}{info['native'].suffix.lower()}")
        if info["renders"]:
            d = out / "renders" / code
            d.mkdir(exist_ok=True)
            for n, (label, f) in enumerate(info["renders"], 1):
                shutil.copy2(f, d / f"{n}-{label}{f.suffix.lower()}")
    with open(out / "stage_map.csv", "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.DictWriter(fh, fieldnames=["code", "source", "status", "stl", "cad", "picture", "renders"])
        w.writeheader()
        w.writerows(rows)
    print(f"\nCopied {len(plan)} designs to {out}. The map is {out / 'stage_map.csv'}. The next free number after this part is {a.prefix}-{last + 1:04d}.")


if __name__ == "__main__":
    main()
