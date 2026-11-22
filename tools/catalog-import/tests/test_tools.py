import csv
import http.server
import json
import os
import sys
import tempfile
import threading
import unittest
from decimal import Decimal
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import catalog_csv as cc  # noqa: E402
import check_design_list as chk  # noqa: E402
import import_designs as imp  # noqa: E402
import stage_folders as stg  # noqa: E402
import stl_volume as vol  # noqa: E402
import struct  # noqa: E402


def row(**kw):
    base = {"folder": "LR-0443", "name": "Channel Eternity Band Ring", "weights": "", "stones": "", "gem_count": "", "solitaire": "", "notes": ""}
    base.update(kw)
    return base


class Rules(unittest.TestCase):
    def test_sizes_are_rounded_to_the_step_and_merged(self):
        e = cc.evaluate(row(stones="1.18x3; 1.21x2; 1.32x5", gem_count="10"))
        self.assertEqual([s.text() for s in e.stones], ["1.20x5", "1.30x5"])
        self.assertTrue(e.ok)
        self.assertEqual(cc.snap("0.97"), Decimal("0.95"))
        self.assertEqual(cc.snap("1.025"), Decimal("1.05"))
        self.assertIsNone(cc.snap("abc"))

    def test_counts_must_add_up_to_the_gem_report(self):
        self.assertIn("add up to 10", " ".join(cc.evaluate(row(stones="1.20x10", gem_count="12")).problems))
        self.assertTrue(cc.evaluate(row(stones="1.20x10; pear 3.00*2.00x1", gem_count="11")).ok)

    def test_solitaire_is_one_big_stone(self):
        self.assertTrue(cc.evaluate(row(stones="1.10x18; 6.00x1")).solitaire)
        self.assertTrue(cc.evaluate(row(stones="oval 8.00*6.00x1")).solitaire)
        self.assertFalse(cc.evaluate(row(stones="1.10x18; 2.00x1")).solitaire)
        self.assertFalse(cc.evaluate(row(stones="3.00x2")).solitaire)
        self.assertTrue(cc.evaluate(row(stones="", solitaire="yes")).solitaire)
        no = cc.evaluate(row(stones="6.00x1", solitaire="no"))
        self.assertFalse(no.solitaire)
        self.assertTrue(any("one big stone" in p for p in no.problems))

    def test_star_and_non_round(self):
        e = cc.evaluate(row(stones="1.20x4*; 1.30x10; 6.00x1; pear 3.00*2.00x1"))
        self.assertEqual([s.text() for s in e.star_locked], ["1.20x4*", "6.00x1"])
        self.assertEqual([s.text() for s in e.big_round], ["6.00x1"])
        self.assertEqual([s.text() for s in e.non_round], ["pear 3.00*2.00x1"])

    def test_weights_have_to_fit_each_other(self):
        self.assertTrue(cc.evaluate(row(weights="22=1.95;18=1.65")).ok)
        self.assertTrue(any("do not fit" in p for p in cc.evaluate(row(weights="22=1.6;18=1.9")).problems))
        self.assertTrue(any("given twice" in p for p in cc.evaluate(row(weights="18=1.6;18=1.9")).problems))
        self.assertTrue(cc.evaluate(row(weights="?=2.05")).ok)
        self.assertTrue(any("cannot read" in p for p in cc.evaluate(row(weights="2.05")).problems))

    def test_name_and_folder(self):
        self.assertTrue(any("numbers" in p for p in cc.evaluate(row(name="Floral Ring 2")).problems))
        self.assertTrue(any("folder" in p for p in cc.evaluate(row(folder="ET_RD_104")).problems))
        self.assertTrue(any("name is missing" in p for p in cc.evaluate(row(name="")).problems))

    def test_unreadable_stones(self):
        self.assertTrue(any("cannot read the stone" in p for p in cc.evaluate(row(stones="1.20")).problems))
        self.assertTrue(any("no count" in p for p in cc.evaluate(row(stones="1.20x?")).problems))
        self.assertTrue(cc.evaluate(row(stones="pear 3.00*2.00x?")).ok)


class Stage(unittest.TestCase):
    def test_picks_the_renders(self):
        with tempfile.TemporaryDirectory() as t:
            d = Path(t) / "ET_RD_104"
            d.mkdir()
            for n in ["x RF.stl", "ET_RD_104.3dm", "ET_RD_104.jpg", "Y3D.jpg", "Y3D.png", "YP.png", "R3D.png", "WP.png", "WH.jpg"]:
                (d / n).write_text("x")
            info = stg.inspect_design(d, ["3D", "P", "T"])
            self.assertEqual([(l, f.name) for l, f in info["renders"]], [("yellow", "Y3D.png"), ("rose", "R3D.png"), ("white", "WP.png"), ("model", "WH.jpg")])
            self.assertEqual(info["cad"].name, "ET_RD_104.jpg")
            self.assertEqual(info["problems"], [])

    def test_names_what_is_missing(self):
        with tempfile.TemporaryDirectory() as t:
            d = Path(t) / "198"
            d.mkdir()
            for n in ["a.stl", "b.stl", "198.jpg", "Y3D.png"]:
                (d / n).write_text("x")
            info = stg.inspect_design(d, ["3D"])
            problems = " | ".join(info["problems"])
            for want in ["2 stl files", "0 3dm/jcd files"]:
                self.assertIn(want, problems)
            self.assertNotIn("render", problems)   # a missing render is only a note
            self.assertEqual(info["notes"], ["no rose render", "no white render", "no model photo"])

    def test_no_model_photo_is_not_a_problem(self):
        with tempfile.TemporaryDirectory() as t:
            d = Path(t) / "MHD245"
            d.mkdir()
            for n in ["MHD245.stl", "MHD245.jcd", "MHD245.jpg", "Y3D.png", "R3D.png", "W3D.png"]:
                (d / n).write_text("x")
            info = stg.inspect_design(d, ["3D"])
            self.assertEqual(info["problems"], [])
            self.assertEqual(info["notes"], ["no model photo"])
            self.assertEqual([l for l, _ in info["renders"]], ["yellow", "rose", "white"])

    def test_a_folder_with_no_render_at_all_is_a_problem(self):
        with tempfile.TemporaryDirectory() as t:
            d = Path(t) / "X1"
            d.mkdir()
            for n in ["a.stl", "a.3dm", "a.jpg"]:
                (d / n).write_text("x")
            self.assertEqual(stg.inspect_design(d, ["3D"])["problems"], ["no renders at all"])


class Mock(http.server.BaseHTTPRequestHandler):
    state = {}

    def send(self, status, body, headers=None):
        out = json.dumps(body).encode()
        self.send_response(status)
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def body(self):
        return self.rfile.read(int(self.headers.get("Content-Length") or 0))

    def do_GET(self):
        s, p = self.state, self.path
        if p.startswith("/api/v1/designs?"):
            self.send(200, [{"code": c} for c in s["designs"]], {"X-Total-Count": str(len(s["designs"]))})
        elif p == "/api/v1/trash":
            self.send(200, {"designs": [{"code": c} for c in s["trash"]], "variations": []})
        elif p == "/api/v1/masters/metals":
            self.send(200, [{"code": c, "active": True} for c in s["metals"]])
        elif p == "/api/v1/masters/tags":
            self.send(200, [{"id": i, "name": n, "tag_group": g} for i, (g, n) in enumerate(s["tags"], 1)])
        elif p == "/api/v1/masters/stone-matrix":
            self.send(200, [{"size_mm": z, "stone_code": c} for z, c in s["matrix"]])
        elif p.endswith("/seats"):
            self.send(200, s["seats"].get(int(p.split("/")[4]), []))
        elif p.rsplit("/", 1)[1] in s["designs"]:
            self.send(200, s["designs"][p.rsplit("/", 1)[1]])
        else:
            self.send(404, {"error": "not found"})

    def do_POST(self):
        s, p, raw = self.state, self.path, self.body()
        if p == "/api/v1/auth/login":
            self.send(200, {"token": "T"})
        elif p == "/api/v1/designs/import":
            b = json.loads(raw)
            s["import"] = b
            if not b["dry_run"]:
                for r in b["rows"]:
                    i = len(s["designs"]) + 1
                    s["designs"][r["code"]] = {"id": i, "code": r["code"], "name": r["name"], "category_id": 1, "tags": [], "media": [], "variations": [{"id": 100 + i}]}
            self.send(200 if b["dry_run"] else 201, {"designs": len(b["rows"]), "errors": []})
        elif p.endswith("/media"):
            name = raw.split(b'filename="')[1].split(b'"')[0].decode()
            s["log"].append(("photo", p.split("/")[4], name))
            design = next(d for d in s["designs"].values() if d["id"] == int(p.split("/")[4]))
            design["media"].append({"filename": name})
            self.send(201, {"id": 900 + len(s["log"]), "is_cover": False})
        elif p.endswith("/upload-cad"):
            name = raw.split(b'filename="')[1].split(b'"')[0].decode()
            s["log"].append(("file", p.split("/")[4], name))
            variation = next(v for d in s["designs"].values() for v in d["variations"] if v["id"] == int(p.split("/")[4]))
            variation["file_stl_name" if name.endswith(".stl") else "file_3dm_name"] = name
            self.send(201, {})

    def do_PUT(self):
        s, p, raw = self.state, self.path, self.body()
        if p.endswith("/seats"):
            s["seats"][int(p.split("/")[4])] = json.loads(raw)["seats"]
        elif "/cover" in p:
            s["log"].append(("cover", p.split("/")[4]))
        else:
            s["log"].append(("put", p, json.loads(raw)))
        self.send(200, None)

    def log_message(self, *a):
        pass


class Import(unittest.TestCase):
    def setUp(self):
        Mock.state = {"designs": {}, "trash": ["LR0602"], "metals": ["22KY", "18KY", "18KR"],
                      "tags": [("Sub-Style", "Solitaire"), ("Attribute", "Fancy stone"), ("Attribute", "Color stone")],
                      "matrix": [("1.10", "STAR"), ("1.20", "STAR")], "seats": {}, "log": []}
        self.server = http.server.HTTPServer(("127.0.0.1", 0), Mock)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        for code in ("LR-0598", "LR-0599", "LR-0600", "LR-0601", "LR-0602"):
            (t / "stage" / "renders" / code).mkdir(parents=True)
            for n in ("1-yellow.png", "2-rose.png", "3-white.png", "4-model.jpg"):
                (t / "stage" / "renders" / code / n).write_text("x")
            (t / "stage" / "pictures").mkdir(exist_ok=True)
            (t / "stage" / "pictures" / f"{code}.jpg").write_text("x")
            (t / "stage" / "stl").mkdir(exist_ok=True)
            (t / "stage" / "stl" / f"{code}.stl").write_text("x")
            (t / "stage" / "cad").mkdir(exist_ok=True)
            (t / "stage" / "cad" / f"{code}.jcd").write_text("x")
        rows = [
            ["LR-0598", "Plain Band Ring", "22=1.95;18=1.65", "1.10x20", "20", "", ""],              # weight, flexible seats
            ["LR-0599", "Classic Solitaire Ring", "", "1.10x18; 6.00x1", "19", "", ""],               # volume, big stone -> STAR pending, solitaire
            ["LR-0600", "Pear Crown Ring", "14=1.5", "1.20x16; pear 3.00*2.00x1", "17", "", ""],     # 14K only: no alloy -> volume
            ["LR-0601", "Mystery Ring", "14=1.5", "1.20x10", "10", "", ""],                           # no volume either -> left out
            ["LR-0602", "Trashed Ring", "22=1.9", "1.20x10", "10", "", ""],                           # in the trash
        ]
        self.csv = t / "checked.csv"
        with open(self.csv, "w", newline="") as fh:
            w = csv.writer(fh)
            w.writerow(cc.COLUMNS)
            w.writerows(rows)
        with open(t / "volumes.csv", "w", newline="") as fh:
            w = csv.writer(fh)
            w.writerow(["code", "volume_mm3", "note"])
            w.writerows([["LR0599", "120.5", ""], ["LR0600", "130.0", ""]])
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        os.environ.update(PORTAL_EMAIL="a@b.c", PORTAL_PASSWORD="x")

    def tearDown(self):
        self.server.shutdown()
        self.tmp.cleanup()

    def run_import(self, *extra):
        t = Path(self.tmp.name)
        return imp.main([str(self.csv), "--stage", str(t / "stage"), "--portal", self.url, "--category", "Ladies Rings",
                         "--volumes", str(t / "volumes.csv"), *extra])

    def test_check_only_creates_nothing(self):
        self.assertEqual(self.run_import(), 0)
        s = Mock.state
        self.assertEqual(s["designs"], {})
        self.assertTrue(s["import"]["dry_run"])
        self.assertEqual([r["code"] for r in s["import"]["rows"]], ["LR0598", "LR0599", "LR0600"])

    def test_go_creates_and_completes(self):
        self.assertEqual(self.run_import("--go"), 0)
        s = Mock.state
        by = {r["code"]: r for r in s["import"]["rows"]}
        self.assertEqual((by["LR0598"]["ref_weight_g"], by["LR0598"]["ref_metal"]), ("1.95", "22KY"))   # the 22K weight, first choice
        self.assertEqual((by["LR0599"]["volume_mm3"], by["LR0599"]["ref_weight_g"]), ("120.5", ""))      # no weight: the volume
        self.assertEqual(by["LR0600"]["volume_mm3"], "130.0")                                            # a 14K weight but no 14K alloy
        self.assertEqual(by["LR0599"]["tags"], "Sub-Style:Solitaire")
        self.assertEqual(by["LR0600"]["tags"], "Sub-Style:Solitaire;Attribute:Fancy stone")   # one big pear stone: a solitaire, not round
        self.assertNotIn("LR0601", by)                                                                   # left out
        self.assertNotIn("LR0602", by)                                                                   # in the trash
        # seats: the flexible ones; the 6.00 mm STAR seat waits for its matrix row
        seats = s["seats"]
        self.assertEqual(seats[101], [{"size_mm": 1.1, "stone_count": 20}])
        self.assertEqual(seats[102], [{"size_mm": 1.1, "stone_count": 18}])
        self.assertEqual(seats[103], [{"size_mm": 1.2, "stone_count": 16}])
        t = Path(self.tmp.name)
        self.assertIn("LR0599,Classic Solitaire Ring,6.00,1", (t / "pending_star.csv").read_text(encoding="utf-8-sig").replace("\r", ""))
        self.assertIn("pear 3.00*2.00x1", (t / "non_round.csv").read_text(encoding="utf-8-sig"))
        photos = [x for x in s["log"] if x[0] == "photo" and x[1] == "1"]
        self.assertEqual([x[2] for x in photos], ["LR0598-1-yellow.png", "LR0598-2-rose.png", "LR0598-3-white.png", "LR0598-4-model.jpg"])
        self.assertIn(("file", "101", "LR-0598.jcd"), [tuple(x) for x in s["log"]])    # a jcd goes to the native slot
        self.assertFalse(any("cad" in x[2] for x in photos))                           # the CAD picture is not uploaded

    def test_the_star_seat_is_added_once_its_row_exists(self):
        self.run_import("--go")
        Mock.state["matrix"].append(("6.00", "STAR"))
        Mock.state["log"].clear()
        self.run_import("--go")
        self.assertIn({"size_mm": 6.0, "stone_count": 1, "locked_stone_code": "STAR"}, Mock.state["seats"][102])
        self.assertEqual([x for x in Mock.state["log"] if x[0] in ("photo", "file")], [])   # nothing is uploaded twice


class Check(unittest.TestCase):
    def test_checked_copy(self):
        with tempfile.TemporaryDirectory() as t:
            src, out = Path(t) / "a.csv", Path(t) / "b.csv"
            src.write_text("folder,name,weights,stones,gem_count,solitaire,notes\nLR-0443,Eternity Band Ring,22=1.95,1.18x20,20,,\nLR-0444,Plain Ring 2,,,,,\n")
            self.assertEqual(chk.main([str(src), "--out", str(out)]), 1)
            rows = list(csv.DictReader(open(out, encoding="utf-8-sig")))
            self.assertEqual((rows[0]["stones"], rows[0]["check"], rows[0]["solitaire"]), ("1.20x20", "ok", "no"))
            self.assertIn("numbers", rows[1]["check"])


def write_stl(path, triangles):
    with open(path, "wb") as f:
        f.write(b"\0" * 80)
        f.write(struct.pack("<I", len(triangles)))
        for tri in triangles:
            f.write(struct.pack("<12fH", 0, 0, 0, *tri[0], *tri[1], *tri[2], 0))


def cube(size, origin=(0, 0, 0)):
    v = [(origin[0] + x * size, origin[1] + y * size, origin[2] + z * size) for x in (0, 1) for y in (0, 1) for z in (0, 1)]
    faces = [(0, 2, 1), (1, 2, 3), (4, 5, 6), (5, 7, 6), (0, 1, 4), (1, 5, 4), (2, 6, 3), (3, 6, 7), (0, 4, 2), (2, 4, 6), (1, 3, 5), (3, 7, 5)]
    return [(v[a], v[b], v[c]) for a, b, c in faces]


class Volume(unittest.TestCase):
    def test_volume_and_holes(self):
        with tempfile.TemporaryDirectory() as t:
            p = Path(t) / "x.stl"
            write_stl(p, cube(10))
            v, n, size, err = vol.measure(p)
            self.assertAlmostEqual(v, 1000, places=3)
            self.assertEqual((n, size), (12, [10, 10, 10]))
            self.assertLess(err, 0.01)
            write_stl(p, cube(10, (1000, -500, 300)))              # far from the origin
            self.assertAlmostEqual(vol.measure(p)[0], 1000, places=1)
            write_stl(p, cube(10)[:-1])                              # a missing triangle: a hole
            v, _, _, err = vol.measure(p)
            self.assertGreater(err, vol.MAX_ERROR_PCT)
            self.assertGreater(err, abs(v - 1000) / 10)              # the bound is above the real error (8.3 %)

    def test_compare_with_the_printed_weights(self):
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            for n in (443, 444, 445):
                write_stl(d / f"LR-{n}.stl", cube(10))
            (d / "list.csv").write_text("folder,name,weights,stones,gem_count,solitaire,notes\n"
                                        "LR-0443,A Ring,22=17.75,,,,\nLR-0444,B Ring,18=15.5,,,,\nLR-0445,C Ring,?=13.1,,,,\n")
            out = Path(t) / "v.csv"
            import io
            import contextlib
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                vol.main([str(d), "--out", str(out), "--check", str(d / "list.csv")])
            text = buf.getvalue()
            self.assertIn("check 22K: 1 designs", text)
            self.assertIn("median 1.000", text)
            self.assertIn("nearest karat is 14K", text)             # 13.1 g/cm3 is 14K gold


class Brief(unittest.TestCase):
    def test_every_example_in_the_brief_passes_the_checker(self):
        text = (Path(__file__).resolve().parent.parent / "AGENT_BRIEF.md").read_text(encoding="utf-8")
        lines = [l for l in text.splitlines() if l.startswith("LR-06")]
        self.assertGreaterEqual(len(lines), 4)
        with tempfile.TemporaryDirectory() as t:
            p = Path(t) / "x.csv"
            p.write_text(",".join(cc.COLUMNS) + "\n" + "\n".join(lines) + "\n", encoding="utf-8")
            evals = chk.check(chk.read_rows(p))
            for e in evals:
                self.assertEqual(e.problems, [], e.row["folder"])
            self.assertEqual([e.solitaire for e in evals], [False, True, True, False])   # the pear stone is one big stone

    def test_the_header_in_the_brief_is_the_real_one(self):
        text = (Path(__file__).resolve().parent.parent / "AGENT_BRIEF.md").read_text(encoding="utf-8")
        self.assertIn(",".join(cc.COLUMNS), text)


if __name__ == "__main__":
    unittest.main()
