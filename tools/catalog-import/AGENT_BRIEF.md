# Brief: prepare the design list from CAD pictures

(`read_pictures.py` does this with a model endpoint, using these same rules. This brief is for an agent that does it
by hand, and the rules in it are the reference.)

You are given a folder of CAD pictures, one per ring design, and you write one CSV file, the **design list**. A person
checks it, then a script creates the designs in the catalog portal from it. Your job is to read what is printed on
each picture, exactly, and to say so when you cannot. A wrong number that looks right is worse than an empty cell.

## Rings only

This list is for **rings** (the codes start with LR). If a picture shows something else (a chain pendant, a pendant, a
chain, an earring, a bangle), **do not write a row for it**. List it instead in a second small CSV, `not_rings.csv`, with
the header `folder,what` (for example `LR-0612,chain pendant`), so a person can move it to its own folder. When you are
not sure whether it is a ring, write the row and say so in `notes`.

## What you get

A folder `pictures/` with one file per design, named like the design: `LR-0598.jpg`, `LR-0599.jpg`, and so on. Each is
a screenshot of the CAD program: four views of the ring (the metal is often green and the stones often blue; that is
only how the CAD program draws them), and a **gem report** box, usually at the bottom right. It may also have a
gold-weight box, a text label such as `G.W.T-2`, or neither.

Optionally the folder `renders/LR-0598/` holds finished renders of the same ring. Use them only to understand the
design when you write its name. Never read numbers from them.

## What you write

One CSV file, UTF-8, comma separated, **exactly this header**, one row per picture, in file-name order:

```
folder,name,weights,stones,gem_count,solitaire,notes
```

Write nothing else in that file: no explanation, no code fence, no extra columns. Put a cell in double quotes if it contains a comma.

| column | what to put |
|---|---|
| `folder` | the file name without its extension: `LR-0598` |
| `name` | 2 to 4 words in Title Case, ending in `Ring`, that describe the design (leaf, bow, halo, eternity, bypass, solitaire, floral, heart, wave, twist). **No numbers, no codes, no metal names.** Example: `Twin Leaf Bypass Ring` |
| `weights` | the gold weights printed on the picture, as `karat=grams` joined with `;`. See below. Empty when no weight is printed |
| `stones` | the rows of the gem report, joined with `;`. See below. Empty when the picture has no gem report |
| `gem_count` | the **Total Gem Count** printed in the gem report, a whole number. Empty when there is none |
| `solitaire` | `yes` when the ring has one big stone in the centre, `no` when it clearly does not, **empty when you are not sure** (the checker then decides from the stones) |
| `notes` | anything a person should look at: a picture you could not read, a label you did not understand, a stone you could not place. Empty otherwise |

### weights

Printed weights look like `gold wt 22kt=1.95gm` or `18kt = 1.65 gm`. Write each as `karat=grams`:

```
22=1.95;18=1.65
```

- Use the karat number only: `22`, `18`, `14`. Grams with a dot, as printed (`2.05`).
- A weight printed **without** a karat (for example `G.W.T-2` next to a number) is written with a question mark:
  `?=2.05`. Do not decide which karat it is.
- Copy the numbers exactly. Never calculate one weight from another, and never estimate a weight.
- No weight printed: leave the cell empty.

### stones

The gem report has one row per stone type, for example:

```
Round | Diamond   1.20 X 1.20   17   0.11tw
```

- A **round** stone is `SIZExCOUNT`: `1.20x17`. The size is the first number of `1.20 X 1.20`.
- If the **material heading** of the row says something other than Diamond (Ruby, Sapphire, Emerald, ...), put a `*`
  after the count: `1.20x4*`. Decide this only from the heading. **Never decide from the colour of the render**: many
  pictures draw every stone blue.
- Any **other shape** is `shape LENGTH*WIDTHxCOUNT`: `pear 3.00*2.00x1`, `marquise 5.00*2.50x1`, `cushion square 6.00*6.00x1`,
  `emerald 6.00*4.00x1`, `oval 7.00*5.00x1`. Length is the first number of `3.00 X 2.00`, width the second.
- Join the rows with `;`: `1.10x18; 6.00x1`.
- If a count cannot be read, write `?` for a non-round stone (`pear 3.00*2.00x?`). For a round stone **leave the picture
  for a person**: write what you can and say so in `notes`.
- Sizes go in steps of 0.05 mm in real life; if you read 1.18 write 1.18, the checker rounds it. Do not round it yourself.

**The only source of stones is a gem report printed on the picture.** If there is none, leave `stones` and `gem_count`
empty. Never count the stones you can see, never read sizes off the drawing or the dimension lines (those are the
ring's size, not stone sizes).

### solitaire

A solitaire has **one big stone in the centre** and that stone is listed once, count 1, 2.5 mm or larger. If the gem
report has such a row, you can leave the cell empty: the checker finds it. Write `yes` only when the picture clearly
shows a big centre stone that the gem report does not list. Write `no` only when you are sure it has none.

A **three stone ring** (a big stone with a pair of big stones beside it, 2.5 mm or larger) is never a solitaire; the
checker applies this, so leave the cell empty or write `no`.

A small **square princess stone** (up to 2.00 mm, for example `princess 1.70*1.70x36`) becomes a normal seat of that
size, like a round stone. Bigger or rectangular princess stones, and every other shape, get no seat.

## How to work

1. List the files in `pictures/` in name order. Do one picture at a time.
2. Find the gem report and read each row. Read the numbers twice; the print is small.
3. Look for printed weights. Note any label you cannot explain in `notes`.
4. Look at the render (if any) for the name.
5. Write the row. If you are unsure about a number, **leave it out and say so in `notes`**.
6. When all rows are written, check your own work before you hand it over:
   - the counts in `stones` add up to `gem_count`;
   - no name has a digit in it;
   - the same file does not appear twice.

## After you hand it over

A person runs the checker:

```
python check_design_list.py design_list.csv --out checked.csv
```

It rounds the sizes, decides the solitaire column where it is empty, and lists every row that has a problem: a count
that does not add up, weights of different karats that do not fit each other, a name with a number, a stone it cannot
read. Rows with a problem are not imported until they are fixed. Fixing them is quick when `notes` says what you were
unsure about, so write it down.

## Examples

A picture with a gem report listing `Round Diamond 1.20 X 1.20, 26, 0.13tw` and `gold wt 22kt=1.95gm / gold wt 18kt=1.65gm`:

```
LR-0605,Twin Leaf Bypass Ring,22=1.95;18=1.65,1.20x26,26,,
```

A solitaire: gem report `Round 1.10 X 1.10, 18` and `Round 6.50 X 6.50, 1`, no weights printed:

```
LR-0606,Classic Solitaire Ring,,1.10x18; 6.50x1,19,,
```

A ring with a pear stone and a ruby accent (the heading of the 1.30 row says Ruby), one weight with no karat:

```
LR-0607,Pear Crown Ring,?=2.05,1.20x16; 1.30x2*; pear 3.00*2.00x1,19,,
```

A picture with no gem report and no weight, and a label you did not understand:

```
LR-0608,Plain Wave Ring,,,,,"label says G.W.T-X, no gem report"
```
