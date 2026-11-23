# Catalog import tools

Scripts that load designs into the portal in bulk: from a folder of design folders (an STL, a 3dm or jcd, a CAD
screenshot with a gem report, and renders) to designs with stone seats, tags, photos and CAD files. They run on a
person's computer, talk to the portal's API, and use only Python 3.9 or newer (no packages to install).

```
tools/catalog-import/
  read_pictures.py      reads the staged CAD pictures with your own vision model endpoint and writes the design list
  AGENT_BRIEF.md        the same rules in words, for an agent that prepares the design list by hand instead
  stage_folders.py      copies the files of each design folder into a flat layout, with the portal's own numbers
  stl_volume.py         measures the metal volume of each STL file (for designs with no printed weight)
  check_design_list.py  checks the agent's CSV against the rules and writes checked.csv
  import_designs.py     creates and completes the designs in the portal
  catalog_csv.py        the CSV format and every rule (the checker and the importer both use it)
  portal.py             a small API client
  tests/                python -m unittest discover -s tests
```

## The flow, one part (about 40 design folders) at a time

Numbers are never reused once a design has them, so do each part completely before starting the next.

```
set PORTAL_EMAIL=you@example.com
set PORTAL_PASSWORD=...

:: 1. stage: copy the files under the portal's numbers (the first run only shows the plan)
python stage_folders.py "D:\solitaires\Part 1" --start 598 --out work\part1
python stage_folders.py "D:\solitaires\Part 1" --start 598 --out work\part1 --go

:: 2. measure the STL files
python stl_volume.py work\part1\stl --out work\part1\volumes.csv

:: 3. read the CAD pictures with your model endpoint (try 5 first, then all); this writes work\part1\design_list.csv
::    and not_rings.csv. Or give AGENT_BRIEF.md and work\part1\pictures to an agent that does it by hand.
set CAD_API_KEY=your-key
python read_pictures.py work\part1 --model YOUR-MODEL --base-url https://your.host/v1 --limit 5
python read_pictures.py work\part1 --model YOUR-MODEL --base-url https://your.host/v1

:: 4. check it, fix the rows with a problem
python check_design_list.py work\part1\design_list.csv --out work\part1\checked.csv

:: 5. import: check first, then create
python import_designs.py work\part1\checked.csv --stage work\part1 --portal https://portal.example --category "Ladies Rings" --volumes work\part1\volumes.csv
python import_designs.py work\part1\checked.csv --stage work\part1 --portal https://portal.example --category "Ladies Rings" --volumes work\part1\volumes.csv --go
```

Start the next part at the number the import prints as "the next free number". The staged copy and the maps can be
deleted once you have checked the designs in the portal; the source folders are never changed.

## Reading the pictures

`read_pictures.py` sends each picture in `work\part1\pictures` to your model endpoint (`--api-style openai`, the default,
posts to `<base-url>/chat/completions`; `--api-style anthropic` posts to `<base-url>/v1/messages` and needs
`pip install anthropic`) with the rules of `AGENT_BRIEF.md`, and writes `design_list.csv` and `not_rings.csv`. The key
comes from `CAD_API_KEY` (never the command line). The model must accept images.

- Each answer is saved under `cache\`, so a stopped run continues and nothing is paid for twice; `--refresh` asks again.
- `--offset` and `--limit` read part of the pictures; the rows of the others are kept.
- A picture that cannot be read still gets a row, with an empty name and a note saying why, so the checker flags it
  and it is never imported by mistake.
- `--with-render` also shows the model the first render, which can help with the name.
- The model's answer is forgiven the usual slips (a code fence, a comment, a trailing comma); a broken answer is
  tried three times.

## Rings only

The tools load rings. Other items (chain pendants, earrings) go in their own folders and their own run. Keep them out of
a part with `stage_folders.py --include "^LR"` or `--exclude "^CP"` (regular expressions on the folder name, case does not
matter; folders left out get no number). The agent lists anything that is not a ring in `not_rings.csv`, and the checker
refuses a name that does not end in Ring.

To choose the folders by hand instead, write the list and edit it in a spreadsheet:

```
python stage_folders.py "D:\solitaires\Part 1" --list folders.csv
:: open folders.csv, put no in the keep column of every folder that must not be staged, save, then:
python stage_folders.py "D:\solitaires\Part 1" --start 598 --out work\part1 --from-list folders.csv --go
```

The `picture` column is the CAD picture of each folder, so you can look at it. A folder that is not in the list is not
staged, and the run names it.

## Rules the tools apply

- **Photos.** One render per metal (yellow, rose, white; view 3D, else P, else T; PNG before JPG), then the model photo
  (WH), when the folder has one: neither a missing metal nor a missing model photo stops a design. The yellow one is the cover. The CAD screenshot is only read, not uploaded (`--upload-cad-picture` changes that).
  A design holds 5 photos at most.
- **Base input.** The printed weight of the first karat the portal has an alloy for (22, 18, 14, ...), else the STL
  volume. A design with neither is left out, and the reason is printed.
- **Seats.** A round stone is a seat of its size and count. A coloured round stone (`*` in the list) and any round stone
  above 2.00 mm, the largest size in the stone matrix, are seats **locked to STAR**. STAR rows for those sizes must exist
  in the stone matrix before the seats can be made; the importer lists the sizes it needs and keeps the others waiting
  in `pending_star.csv`. Run it again after the rows are entered. A stone of any other shape gets no seat and is listed in
  `non_round.csv`.
- **Tags.** `Sub-Style:Solitaire` when the design has one stone (count 1) of 2.5 mm or more; `Attribute:Fancy stone` for
  a stone that is not round; `Attribute:Color stone` for a coloured one. The tags must exist (Master data, Tags); the
  importer says which are missing before it creates anything. Change a name with `--solitaire-tag`, `--fancy-tag`,
  `--color-tag`, or give an empty one to skip it.
- **Sizes.** Stone sizes are rounded to the nearest 0.05 mm, and rows that become the same size are added up.
- **Re-running.** Every run only adds what a design lacks (a seat, a tag, a photo, a file) and never replaces anything.
  A design in the portal's trash keeps its code reserved and is skipped.

## Tests

```
python -m unittest discover -s tests
```

They cover the rules, the staging, the volumes and the whole import against a mock portal, and that every example in
`AGENT_BRIEF.md` passes the checker.
