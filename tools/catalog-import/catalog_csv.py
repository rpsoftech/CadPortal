"""The design list: one CSV row per design, prepared by an agent from the CAD picture, checked here and then imported.

Columns, in this order (the header must match exactly):

    folder,name,weights,stones,gem_count,solitaire,notes

    folder      the design's staged name, for example LR-0443 (the name of its files after staging)
    name        2 to 4 words in Title Case ending in Ring, no digits or codes
    weights     gold weights printed on the CAD picture, "karat=grams" joined with ";": 22=1.95;18=1.65;14=1.50.
                A weight printed without a karat is ?=2.05. Empty when no weight is printed.
    stones      the rows of the gem report, joined with ";". A round stone is SIZExCOUNT (1.20x17), with a * after the
                count when its material heading is not Diamond (1.20x4*). Any other shape is "shape LENGTH*WIDTHxCOUNT"
                (pear 3.00*2.00x1). Empty when the picture has no gem report.
    gem_count   the Total Gem Count printed in the gem report; empty when there is none
    solitaire   yes or no when the picture clearly shows (or clearly does not show) one big centre stone; empty to
                let the rule below decide
    notes       anything unclear, for a person to look at

This module holds the rules. The importer and the checker both use it, so the two never disagree.
"""
import re
from dataclasses import dataclass, field
from decimal import ROUND_HALF_UP, Decimal, InvalidOperation

COLUMNS = ["folder", "name", "weights", "stones", "gem_count", "solitaire", "notes"]

MATRIX_MAX_MM = Decimal("2.00")   # the largest size the stone matrix has rows for: a round stone above it is a STAR seat
SOLITAIRE_FROM_MM = Decimal("2.50")  # one stone (count 1) of at least this size makes a design a solitaire
STEP = Decimal("0.05")            # real stone sizes go in steps of this many mm

# Specific gravities used to judge whether the weights printed for different karats fit each other. Approximate on
# purpose: the portal's own figures decide the real weights; this only catches a misread number.
KARAT_SG = {9: Decimal("11.2"), 10: Decimal("11.6"), 14: Decimal("13.1"), 18: Decimal("15.5"), 20: Decimal("16.5"), 22: Decimal("17.75"), 24: Decimal("19.3")}
WEIGHT_TOLERANCE = Decimal("0.12")


@dataclass
class Stone:
    shape: str            # "round" or another shape name
    length: Decimal
    width: Decimal
    count: "int | None"
    colored: bool = False

    @property
    def is_round(self):
        return self.shape == "round"

    @property
    def biggest(self):
        return max(self.length, self.width)

    def text(self):
        c = "?" if self.count is None else str(self.count)
        if self.is_round:
            return f"{fmt(self.length)}x{c}" + ("*" if self.colored else "")
        return f"{self.shape} {fmt(self.length)}*{fmt(self.width)}x{c}"


def fmt(d: Decimal) -> str:
    return str(d.quantize(Decimal("0.01")))


def snap(value) -> "Decimal | None":
    """A size rounded to the nearest 0.05 mm, halves up. None when it is not a number."""
    try:
        x = Decimal(str(value).strip())
    except (InvalidOperation, ValueError):
        return None
    return ((x / STEP).quantize(Decimal(1), rounding=ROUND_HALF_UP) * STEP).quantize(Decimal("0.01"))


def parse_weights(text: str):
    """[(karat or None, grams)], and the problems found."""
    out, problems = [], []
    for part in [p.strip() for p in (text or "").split(";") if p.strip()]:
        m = re.match(r"^(\d{1,2}|\?)\s*=\s*(\d+(?:\.\d+)?)$", part)
        if not m:
            problems.append(f"cannot read the weight '{part}' (write karat=grams, for example 22=1.95)")
            continue
        karat = None if m.group(1) == "?" else int(m.group(1))
        grams = Decimal(m.group(2))
        if karat is not None and karat not in KARAT_SG:
            problems.append(f"karat {karat} is not one of {sorted(KARAT_SG)}")
        if not Decimal("0.2") <= grams <= Decimal("60"):
            problems.append(f"weight {grams} g is outside 0.2 to 60 g")
        out.append((karat, grams))
    return out, problems


def check_weights(weights):
    """Problems when the weights of different karats do not fit each other (weight follows specific gravity)."""
    problems = []
    known = [(k, g) for k, g in weights if k in KARAT_SG]
    for i in range(len(known)):
        for j in range(i + 1, len(known)):
            (k1, g1), (k2, g2) = known[i], known[j]
            if k1 == k2:
                problems.append(f"karat {k1} is given twice")
                continue
            expected = KARAT_SG[k1] / KARAT_SG[k2]
            ratio = g1 / g2
            if abs(ratio / expected - 1) > WEIGHT_TOLERANCE:
                problems.append(f"{k1}K {g1} g and {k2}K {g2} g do not fit each other (expected a ratio near {expected:.2f}, got {ratio:.2f})")
    return problems


def parse_stones(text: str, round_sizes=True):
    """[Stone], the problems found, and the sizes that were rounded to the 0.05 step ("1.18->1.20")."""
    stones, problems, rounded = [], [], []
    for part in [p.strip() for p in (text or "").split(";") if p.strip()]:
        m = re.match(r"^([\d.]+)\s*x\s*(\d+|\?)(\*?)$", part, re.I)
        if m:
            shape, length, width, count, colored = "round", m.group(1), m.group(1), m.group(2), bool(m.group(3))
        else:
            m = re.match(r"^([A-Za-z][A-Za-z \-]*?)\s+([\d.]+)\*([\d.]+)\s*x\s*(\d+|\?)$", part)
            if not m:
                problems.append(f"cannot read the stone '{part}' (write 1.20x17, or pear 3.00*2.00x1)")
                continue
            shape, length, width, count, colored = m.group(1).strip().lower(), m.group(2), m.group(3), m.group(4), False
        l, w = Decimal(length) if re.match(r"^\d+(\.\d+)?$", length) else None, Decimal(width) if re.match(r"^\d+(\.\d+)?$", width) else None
        if l is None or w is None:
            problems.append(f"'{part}' has a size that is not a number")
            continue
        if round_sizes:
            for old, new in ((l, snap(l)), (w, snap(w))):
                if new != old.quantize(Decimal("0.01")) and f"{old}->{new}" not in rounded:
                    rounded.append(f"{old}->{new}")
            l, w = snap(l), snap(w)
        n = None if count == "?" else int(count)
        if not Decimal("0.3") <= max(l, w) <= Decimal("40"):
            problems.append(f"'{part}' has a size outside 0.3 to 40 mm")
        if n is not None and not 1 <= n <= 3000:
            problems.append(f"'{part}' has a count outside 1 to 3000")
        stones.append(Stone(shape, l, w, n, colored))
    return merge(stones), problems, rounded


def merge(stones):
    """Rows that are the same stone (after rounding) become one row."""
    out, index = [], {}
    for s in stones:
        k = (s.shape, s.length, s.width, s.colored)
        if k in index and index[k].count is not None and s.count is not None:
            index[k].count += s.count
        else:
            out.append(s)
            index.setdefault(k, s)
    return out


@dataclass
class Evaluated:
    row: dict
    weights: list = field(default_factory=list)
    stones: list = field(default_factory=list)
    solitaire: bool = False
    problems: list = field(default_factory=list)
    rounded: list = field(default_factory=list)

    @property
    def big_round(self):
        """Round stones above the matrix's largest size: seats locked to STAR, whose rows the owner still has to enter."""
        return [s for s in self.stones if s.is_round and s.length > MATRIX_MAX_MM]

    @property
    def non_round(self):
        return [s for s in self.stones if not s.is_round]

    @property
    def star_locked(self):
        """The seats that are locked to STAR: coloured round stones and big round stones."""
        return [s for s in self.stones if s.is_round and (s.colored or s.length > MATRIX_MAX_MM)]

    @property
    def ok(self):
        return not self.problems


def evaluate(row: dict, round_sizes=True) -> Evaluated:
    """Reads one row of the design list and applies every rule. The row is not changed."""
    e = Evaluated(row=row)
    folder = (row.get("folder") or "").strip()
    if not re.match(r"^[A-Za-z]+-\d{3,5}$", folder):
        e.problems.append("folder should look like LR-0443")
    name = (row.get("name") or "").strip()
    if not name:
        e.problems.append("name is missing")
    elif re.search(r"\d", name):
        e.problems.append("the name must not contain numbers")
    elif not name.lower().endswith("ring"):
        e.problems.append("the name must end in Ring: these tools load rings only, other items (pendants, chains, earrings) go in their own folders")
    elif not 2 <= len(name.split()) <= 5:
        e.problems.append("the name should be 2 to 4 words")
    e.weights, p = parse_weights(row.get("weights", ""))
    e.problems += p + check_weights(e.weights)
    e.stones, p, e.rounded = parse_stones(row.get("stones", ""), round_sizes)
    e.problems += p
    gem = (row.get("gem_count") or "").strip()
    if gem and not gem.isdigit():
        e.problems.append("gem_count must be a whole number")
    elif gem and e.stones and all(s.count is not None for s in e.stones):
        total = sum(s.count for s in e.stones)
        if total != int(gem):
            e.problems.append(f"the stone counts add up to {total}, the gem report says {gem}")
    if any(s.count is None and s.is_round for s in e.stones):
        e.problems.append("a round stone has no count")
    said = (row.get("solitaire") or "").strip().lower()
    if said not in ("", "yes", "no"):
        e.problems.append("solitaire must be yes, no or empty")
    by_rule = any(s.count == 1 and s.biggest >= SOLITAIRE_FROM_MM for s in e.stones)
    if said == "no" and by_rule:
        e.problems.append("solitaire is no, but the gem report has one big stone: check the picture")
    e.solitaire = True if said == "yes" else (False if said == "no" else by_rule)
    return e
