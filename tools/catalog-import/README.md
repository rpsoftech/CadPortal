# Catalog import tools

Scripts that load designs into the portal in bulk: from a folder of design folders (an STL, a 3dm or jcd, a CAD
screenshot with a gem report, and renders) to designs with stone seats, tags, photos and CAD files. They run on a
person's computer, talk to the portal's API, and use only Python 3.9 or newer (no packages to install).

```
tools/catalog-import/
  AGENT_BRIEF.md        what to give an agent so it prepares the design list from the CAD pictures
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

:: 3. give AGENT_BRIEF.md and work\part1\pictures to the agent; it writes work\part1\design_list.csv
:: 4. check it, fix the rows with a problem
python check_design_list.py work\part1\design_list.csv --out work\part1\checked.csv

:: 5. import: check first, then create
python import_designs.py work\part1\checked.csv --stage work\part1 --portal https://portal.example --category "Ladies Rings" --volumes work\part1\volumes.csv
python import_designs.py work\part1\checked.csv --stage work\part1 --portal https://portal.example --category "Ladies Rings" --volumes work\part1\volumes.csv --go
```

Start the next part at the number the import prints as "the next free number". The staged copy and the maps can be
deleted once you have checked the designs in the portal; the source folders are never changed.

## Rules the tools apply

- **Photos.** One render per metal (yellow, rose, white; view 3D, else P, else T; PNG before JPG), then the model photo
  (WH). The yellow one is the cover. The CAD screenshot is only read, not uploaded (`--upload-cad-picture` changes that).
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
