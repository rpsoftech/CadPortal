#!/usr/bin/env python3
"""Reads the staged CAD pictures with a vision model and writes the design list (design_list.csv).

    set CAD_API_KEY=your-key
    python read_pictures.py work\\part1 --model YOUR-MODEL --base-url https://your.host/v1 --limit 5      try 5 first
    python read_pictures.py work\\part1 --model YOUR-MODEL --base-url https://your.host/v1                all of them

It reads work\\part1\\pictures\\LR-0598.jpg, LR-0599.jpg ... (from stage_folders.py) and writes in work\\part1:

    design_list.csv   one row per ring, in the format of catalog_csv.py (then check_design_list.py)
    not_rings.csv     pictures that are not rings (folder, what), so they can be moved out
    cache\\           the model's raw answer per picture: a stopped run continues, and nothing is asked twice

It works with your own endpoint in either of the two common formats: --api-style openai (the default: POST
<base-url>/chat/completions) or --api-style anthropic (POST <base-url>/v1/messages, needs: pip install anthropic).
The key is read from CAD_API_KEY (or OPENAI_API_KEY / ANTHROPIC_API_KEY); never put it on the command line.
The model must accept images. AGENT_BRIEF.md says the same rules in words, for an agent working by hand.
"""
import argparse
import base64
import csv
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import catalog_csv as cc

EXT = {".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

PROMPT = """This is a screenshot of a CAD program showing a piece of jewelry from several sides, usually with a printed
"gem report" box (often at the bottom right) and sometimes a gold-weight box or a text label. The metal is often drawn
green and the stones blue: that is only how the program draws them. Reply with ONE JSON object in the shape below and
nothing else: no comments, no text before or after it, no code fences.
{
  "is_ring": true,
  "what": null,
  "name": "Twin Leaf Bypass Ring",
  "weights": [{"karat": 22, "grams": 1.95}, {"karat": 18, "grams": 1.65}],
  "stones": [{"shape": "Round", "length_mm": "1.20", "width_mm": "1.20", "count": 26, "material": "Diamond"}],
  "gem_count": 26,
  "solitaire": null,
  "notes": null
}
Fields (use null, or an empty list, when it is not on the picture):
- is_ring: false when the piece is not a ring (a chain, a pendant, a chain pendant, an earring, a bangle). Then also set
  what to a few words saying what it is, and leave the rest empty.
- name: 2 to 4 words in Title Case ending in Ring, describing the design (leaf, bow, halo, eternity, bypass, solitaire,
  floral, heart, wave, twist). No numbers, no codes, no metal names.
- weights: the gold weights printed on the picture, for example "gold wt 22kt=1.95gm" is karat 22, grams 1.95. A weight
  printed without a karat (for example next to a label such as G.W.T-2) has karat null. Copy numbers exactly; never
  calculate or estimate a weight.
- stones: ONE entry per row of the gem report, in the order printed. shape is the shape of the row (Round, Pear,
  Marquise, Oval, Cushion, Emerald ...). material is the heading of that row (Diamond, Ruby, Sapphire ...). For a size
  such as 3.00 X 2.00, length_mm is the first number and width_mm the second, as text with two decimals; for round
  stones they are equal. count is a whole number, or null when you cannot read it.
- gem_count: the "Total Gem Count" printed in the gem report, a whole number.
- solitaire: true when the ring has one big stone in the centre, false when it clearly has none, null when unsure.
- notes: anything a person should look at: a number you could not read, a label you did not understand.
Rules: the stones list comes ONLY from the rows of a gem report printed on the picture. If there is none, use an empty
list and a null gem_count: never count the stones you can see, never read sizes off the drawing or the dimension
lines. Copy numbers exactly as printed; if you cannot read one clearly, use null and say so in notes. Never guess. Use
plain double quotes and no line breaks inside text values."""


def encode(path: Path):
    return base64.standard_b64encode(path.read_bytes()).decode(), EXT[path.suffix.lower()]


def extract_json(text: str):
    """The JSON object in an answer, forgiving about the usual slips: code fences, // comments, trailing commas."""
    text = re.sub(r"```(?:json)?", "", text)
    m = re.search(r"\{.*\}", text, re.S)
    if not m:
        raise ValueError("no JSON in the answer: " + text[:200].replace("\n", " "))
    body = re.sub(r"(?m)^\s*//.*$", "", m.group(0))
    body = re.sub(r"(?<=[,\]\}0-9a-z\"])[ \t]+//[^\n]*", "", body)
    body = re.sub(r",\s*([}\]])", r"\1", body)
    try:
        return json.loads(body)
    except json.JSONDecodeError as e:
        raise ValueError(f"{e.msg} near: ...{body[max(0, e.pos - 60):e.pos + 40]!r}")


class OpenAIStyle:
    """POST <base>/chat/completions with the pictures as data URLs."""
    def __init__(self, base, key):
        self.url, self.key = base.rstrip("/") + "/chat/completions", key

    def complete(self, model, images, prompt):
        parts = [{"type": "text", "text": prompt}] + [{"type": "image_url", "image_url": {"url": f"data:{m};base64,{d}"}} for d, m in images]
        for extra in ({"temperature": 0}, {}):  # some endpoints refuse a temperature: then try without it
            body = json.dumps({"model": model, "max_tokens": 1500, **extra, "messages": [{"role": "user", "content": parts}]}).encode()
            req = urllib.request.Request(self.url, body, {"Content-Type": "application/json", "Authorization": "Bearer " + self.key})
            try:
                with urllib.request.urlopen(req, timeout=180) as r:
                    return json.load(r)["choices"][0]["message"]["content"]
            except urllib.error.HTTPError as e:
                if e.code != 400 or not extra:
                    raise


class AnthropicStyle:
    def __init__(self, base, key):
        import anthropic
        self.c = anthropic.Anthropic(api_key=key, base_url=base or None)

    def complete(self, model, images, prompt):
        content = [{"type": "image", "source": {"type": "base64", "media_type": m, "data": d}} for d, m in images] + [{"type": "text", "text": prompt}]
        r = self.c.messages.create(model=model, max_tokens=1500, temperature=0, messages=[{"role": "user", "content": content}])
        return "".join(b.text for b in r.content if getattr(b, "type", "") == "text")


def ask(client, model, paths, prompt=PROMPT, tries=3):
    images = [encode(p) for p in paths]
    last = None
    for attempt in range(tries):
        try:
            return extract_json(client.complete(model, images, prompt))
        except Exception as e:  # network, rate limit, a broken answer: try again
            last = e
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"gave up after {tries} tries: {last}")


def number(v):
    try:
        return None if v in (None, "") else float(v)
    except (TypeError, ValueError):
        return None


def row_from_answer(folder: str, d: dict) -> dict:
    """The design list row for one answer. Nothing is judged here: the checker applies the rules."""
    weights, seen = [], set()
    for w in d.get("weights") or []:
        g = number(w.get("grams")) if isinstance(w, dict) else None
        k = w.get("karat") if isinstance(w, dict) else None
        k = int(k) if isinstance(k, (int, float)) and not isinstance(k, bool) else None
        if g is None or (k, ) in seen:
            continue
        seen.add((k,))
        weights.append(f"{'?' if k is None else k}={g:g}")
    stones = []
    for s in d.get("stones") or []:
        if not isinstance(s, dict):
            continue
        length, width = number(s.get("length_mm") or s.get("size_mm")), number(s.get("width_mm") or s.get("length_mm") or s.get("size_mm"))
        if length is None:
            continue
        count = s.get("count")
        count = str(int(count)) if isinstance(count, (int, float)) and not isinstance(count, bool) else "?"
        shape = str(s.get("shape") or "round").strip().lower()
        if shape == "round":
            colored = "diamond" not in str(s.get("material") or "diamond").lower()
            stones.append(f"{length:.2f}x{count}" + ("*" if colored else ""))
        else:
            stones.append(f"{shape} {length:.2f}*{(width if width is not None else length):.2f}x{count}")
    gem = d.get("gem_count")
    sol = d.get("solitaire")
    return {"folder": folder, "name": str(d.get("name") or "").strip(), "weights": ";".join(weights), "stones": "; ".join(stones),
            "gem_count": str(int(gem)) if isinstance(gem, (int, float)) and not isinstance(gem, bool) else "",
            "solitaire": "yes" if sol is True else "no" if sol is False else "", "notes": str(d.get("notes") or "").strip()}


def pictures(stage: Path):
    return sorted((p for p in (stage / "pictures").glob("*") if p.suffix.lower() in EXT), key=lambda p: p.name)


def main(argv=None, client=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("stage", help="the staged part (the folder with pictures/, renders/, stl/ ...)")
    ap.add_argument("--model", required=True, help="the model name your endpoint expects")
    ap.add_argument("--base-url", default=os.environ.get("CAD_API_BASE_URL", ""), help="for example https://your.host/v1")
    ap.add_argument("--api-style", choices=["openai", "anthropic"], default="openai")
    ap.add_argument("--with-render", action="store_true", help="also show the first render of the design, to help with its name")
    ap.add_argument("--offset", type=int, default=0, help="skip the first N pictures")
    ap.add_argument("--limit", type=int, default=0, help="then read only N pictures (0 = all the rest)")
    ap.add_argument("--refresh", action="store_true", help="ask again even where an answer is saved")
    a = ap.parse_args(argv)

    stage = Path(a.stage)
    files = pictures(stage)
    if not files:
        sys.exit(f"no pictures in {stage / 'pictures'} (stage first with stage_folders.py)")
    files = files[a.offset:]
    if a.limit:
        files = files[: a.limit]
    if client is None:
        key = os.environ.get("CAD_API_KEY") or os.environ.get("OPENAI_API_KEY") or os.environ.get("ANTHROPIC_API_KEY")
        if not key:
            sys.exit("set CAD_API_KEY to your key first")
        if a.api_style == "openai" and not a.base_url:
            sys.exit("--base-url is needed (for example https://your.host/v1)")
        client = (OpenAIStyle if a.api_style == "openai" else AnthropicStyle)(a.base_url, key)

    (stage / "cache").mkdir(exist_ok=True)
    rows, others = {}, {}
    for i, p in enumerate(files, 1):
        folder = p.stem
        cache = stage / "cache" / f"{folder}.json"
        try:
            if cache.exists() and not a.refresh:
                d = json.loads(cache.read_text(encoding="utf-8"))
            else:
                shown = [p]
                if a.with_render:
                    renders = sorted((stage / "renders" / folder).glob("*"))
                    shown += renders[:1]
                d = ask(client, a.model, shown)
                cache.write_text(json.dumps(d, indent=1), encoding="utf-8")
            if d.get("is_ring") is False:
                others[folder] = str(d.get("what") or "not a ring").strip()
                print(f"[{i}/{len(files)}] {folder}: not a ring ({others[folder]})", flush=True)
                continue
            rows[folder] = row_from_answer(folder, d)
            print(f"[{i}/{len(files)}] {folder}: {rows[folder]['name'] or '(no name)'}", flush=True)
        except Exception as e:
            rows[folder] = {"folder": folder, "name": "", "weights": "", "stones": "", "gem_count": "", "solitaire": "",
                            "notes": f"could not be read: {e}"}
            print(f"[{i}/{len(files)}] {folder}: could not be read: {e}", flush=True)

    # the files keep what earlier runs wrote for other pictures: this run's rows replace the same folders
    lst, nr = stage / "design_list.csv", stage / "not_rings.csv"
    merged = {}
    if lst.exists():
        with open(lst, newline="", encoding="utf-8-sig") as fh:
            merged = {r["folder"]: r for r in csv.DictReader(fh)}
    merged.update(rows)
    for folder in others:
        merged.pop(folder, None)
    with open(lst, "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.DictWriter(fh, fieldnames=cc.COLUMNS)
        w.writeheader()
        for folder in sorted(merged):
            w.writerow({c: merged[folder].get(c, "") for c in cc.COLUMNS})
    old = {}
    if nr.exists():
        with open(nr, newline="", encoding="utf-8-sig") as fh:
            old = {r["folder"]: r["what"] for r in csv.DictReader(fh)}
    old = {k: v for k, v in old.items() if k not in rows}
    old.update(others)
    with open(nr, "w", newline="", encoding="utf-8-sig") as fh:
        w = csv.writer(fh)
        w.writerow(["folder", "what"])
        w.writerows(sorted(old.items()))
    unread = sum(1 for r in rows.values() if r["notes"].startswith("could not be read"))
    print(f"\n{len(rows)} rings, {len(others)} not rings, {unread} could not be read. design_list.csv now has {len(merged)} rows: {lst}\n"
          f"Next: python check_design_list.py {lst} --out {stage / 'checked.csv'}")


if __name__ == "__main__":
    main()
