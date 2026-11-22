#!/usr/bin/env python3
"""Works out the metal volume of each ring from its STL file, for designs that have no weight on the picture.

    python stl_volume.py work\\part1\\stl --out volumes.csv
    python stl_volume.py work\\part1\\stl --out volumes.csv --check design_list.csv     (compare with the printed weights)

The portal turns a volume into a weight for every alloy (weight = volume x specific gravity / 1000), so a volume is
all a design needs. Files are matched to designs by the number in the name (LR-0450.stl), like the other scripts.
The volume is in cubic millimetres, assuming the STL is in millimetres; sizes that look wrong are flagged.

How a hole in the mesh is noticed: for a closed surface the triangle area vectors add up to zero. What is left is the
area of the hole, and from it comes an upper bound for how wrong the volume can be (max_error_pct). Jewelry STLs are
usually made of several overlapping pieces whose edges do not pair up exactly, so counting edges would warn about
almost every file; this measure only warns when the volume itself may be off by more than 3%.
"""
import argparse
import csv
import re
import statistics
import struct
import sys
from pathlib import Path

import catalog_csv as cc

NUM = re.compile(r"([A-Za-z]+)\s*-?\s*(\d{3,5})")
SG = {k: float(v) for k, v in cc.KARAT_SG.items()}  # specific gravity by karat; change one with --sg 14=13.3
MAX_ERROR_PCT = 3.0


def code_from(text):
    m = NUM.search(text)
    return f"{m.group(1).upper()}{int(m.group(2)):04d}" if m else None


def triangles(path: Path):
    """Yields ((x,y,z), (x,y,z), (x,y,z)) for every triangle of a binary or ASCII STL."""
    data = path.read_bytes()
    if len(data) >= 84:
        n = struct.unpack_from("<I", data, 80)[0]
        if 84 + 50 * n == len(data):  # binary
            for f in struct.iter_unpack("<12fH", data[84:]):
                yield (f[3], f[4], f[5]), (f[6], f[7], f[8]), (f[9], f[10], f[11])
            return
    text = data.decode("ascii", "ignore")
    pts = re.findall(r"vertex\s+(\S+)\s+(\S+)\s+(\S+)", text)
    for i in range(0, len(pts) - 2, 3):
        yield tuple(tuple(float(c) for c in pts[i + k]) for k in range(3))


def measure(path: Path):
    """(volume mm3, triangles, [size x, y, z], upper bound of the volume error in percent)."""
    vol, n = 0.0, 0
    ax = ay = az = 0.0  # the sum of the triangle area vectors: zero for a closed surface
    lo, hi = [1e300] * 3, [-1e300] * 3
    for a, b, c in triangles(path):
        n += 1
        vol += (a[0] * (b[1] * c[2] - b[2] * c[1]) - a[1] * (b[0] * c[2] - b[2] * c[0]) + a[2] * (b[0] * c[1] - b[1] * c[0])) / 6.0
        ux, uy, uz = b[0] - a[0], b[1] - a[1], b[2] - a[2]
        vx, vy, vz = c[0] - a[0], c[1] - a[1], c[2] - a[2]
        ax += (uy * vz - uz * vy) / 2
        ay += (uz * vx - ux * vz) / 2
        az += (ux * vy - uy * vx) / 2
        for p in (a, b, c):
            for k in range(3):
                lo[k], hi[k] = min(lo[k], p[k]), max(hi[k], p[k])
    if not n:
        return 0.0, 0, [0, 0, 0], 100.0
    centre = [(lo[k] + hi[k]) / 2 for k in range(3)]
    # the volume measured from the middle of the box: moving the origin changes an open mesh's volume by c . sum(A) / 3
    vol -= (centre[0] * ax + centre[1] * ay + centre[2] * az) / 3
    size = [hi[k] - lo[k] for k in range(3)]
    open_area = (ax * ax + ay * ay + az * az) ** 0.5
    half_diagonal = (sum(s * s for s in size) ** 0.5) / 2
    vol = abs(vol)
    err = 100.0 if vol == 0 else min(100.0, 100 * open_area * half_diagonal / 3 / vol)
    return vol, n, size, err


def printed_weights(design_list):
    """code -> [(karat or None, grams)] from the weights column of a design list."""
    out = {}
    with open(design_list, newline="", encoding="utf-8-sig") as fh:
        for r in csv.DictReader(fh):
            folder = (r.get("folder") or "").strip()
            weights, _ = cc.parse_weights(r.get("weights", ""))
            if weights and re.match(r"^[A-Za-z]+-\d{3,5}$", folder):
                out[code_from(folder)] = [(k, float(g)) for k, g in weights]
    return out


def check_against_weights(rows, design_list):
    """How the STL volumes compare with the weights printed on the pictures: volume x specific gravity / printed weight."""
    weights = printed_weights(design_list)
    ratios, implied = [], []
    for r in rows:
        vol = float(r["volume_mm3"])
        if r["code"] not in weights or vol <= 0 or r["note"]:
            continue
        for karat, grams in weights[r["code"]]:
            if karat in SG:
                ratios.append((r["code"], karat, vol * SG[karat] / 1000 / grams))
            else:
                implied.append((r["code"], grams * 1000 / vol))
    if ratios:
        for karat in sorted({k for _, k, _ in ratios}):
            xs = [x for _, k, x in ratios if k == karat]
            print(f"\ncheck {karat}K: {len(xs)} designs, volume weight / printed weight: median {statistics.median(xs):.3f}, min {min(xs):.3f}, max {max(xs):.3f}")
        print("A median near 1.00 and a narrow spread means the STL volume can be trusted for designs without a weight. "
              "A median below 1 means the printed weights are for a lighter alloy than that karat's specific gravity assumes.")
    if implied:
        vals = sorted(d for _, d in implied)
        print(f"\nweights printed without a karat: {len(vals)} designs, weight per volume median {statistics.median(vals):.2f} g/cm3 "
              f"(22K is {SG[22]}, 18K {SG[18]}, 14K {SG[14]}); the nearest karat is {min(SG, key=lambda k: abs(SG[k] - statistics.median(vals)))}K")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("folder")
    ap.add_argument("--out", default="volumes.csv")
    ap.add_argument("--check", default="", help="design_list.csv: compare the volumes with the weights printed on the pictures")
    ap.add_argument("--sg", action="append", default=[], metavar="KARAT=SG", help="your own specific gravity for a karat, for example --sg 14=13.3")
    a = ap.parse_args(argv)
    for item in a.sg:
        try:
            karat, value = item.split("=")
            SG[int(karat)] = float(value)
        except ValueError:
            sys.exit(f"--sg {item}: write it as KARAT=SG, for example 14=13.3")

    root = Path(a.folder)
    if not root.is_dir():
        sys.exit(f"folder not found: {a.folder}")
    rows, seen = [], {}
    for f in sorted(root.rglob("*")):
        if not (f.is_file() and f.suffix.lower() == ".stl"):
            continue
        code = next((c for c in (code_from(p.name) for p in f.relative_to(root).parents if p.name) if c), None) or code_from(f.stem)
        if not code:
            print(f"{f.name}: no design number in the name, skipped")
            continue
        if code in seen:
            print(f"{f.name}: a second STL for {code}, skipped (using {seen[code]})")
            continue
        seen[code] = f.name
        try:
            vol, n, size, err = measure(f)
        except Exception as e:
            print(f"{f.name}: could not be read ({e})")
            continue
        notes = []
        big = max(size)
        if n == 0 or vol == 0:
            notes.append("empty or flat mesh")
        elif big < 3:
            notes.append("very small: is the STL in millimetres?")
        elif big > 60:
            notes.append("very large: is the STL in millimetres?")
        if n and vol and err > MAX_ERROR_PCT:
            notes.append(f"holes in the mesh: the volume may be off by up to {err:.0f}%")
        rows.append({"code": code, "file": f.name, "triangles": n, "volume_mm3": f"{vol:.2f}",
                     "size_x": f"{size[0]:.2f}", "size_y": f"{size[1]:.2f}", "size_z": f"{size[2]:.2f}",
                     "max_error_pct": f"{err:.2f}", "note": "; ".join(notes)})
        print(f"{code}: {vol:.1f} mm3 (error at most {err:.1f}%)" + (f"  <- {'; '.join(notes)}" if notes else ""), flush=True)

    with open(a.out, "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.DictWriter(fh, fieldnames=["code", "file", "triangles", "volume_mm3", "size_x", "size_y", "size_z", "max_error_pct", "note"])
        w.writeheader()
        w.writerows(rows)
    print(f"\n{len(rows)} files, {sum(1 for r in rows if r['note'])} with a note. Written to {a.out}")
    if a.check:
        check_against_weights(rows, a.check)


if __name__ == "__main__":
    main()
