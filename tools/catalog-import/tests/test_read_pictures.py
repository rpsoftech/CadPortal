import base64
import csv
import http.server
import json
import os
import sys
import tempfile
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import catalog_csv as cc  # noqa: E402
import check_design_list as chk  # noqa: E402
import read_pictures as rp  # noqa: E402

ANSWERS = {
    "0598": {"is_ring": True, "name": "Twin Leaf Bypass Ring", "weights": [{"karat": 22, "grams": 1.95}, {"karat": 18, "grams": 1.65}],
             "stones": [{"shape": "Round", "length_mm": "1.18", "width_mm": "1.18", "count": 26, "material": "Diamond"}], "gem_count": 26, "solitaire": None, "notes": None},
    "0599": "```json\n{\"is_ring\": true, \"name\": \"Classic Solitaire Ring\", // the centre stone\n \"weights\": [], \"stones\": [{\"shape\": \"Round\", \"length_mm\": \"1.10\", \"count\": 18, \"material\": \"Diamond\"},"
            " {\"shape\": \"Round\", \"length_mm\": \"6.50\", \"count\": 1, \"material\": \"Diamond\"},], \"gem_count\": 19,}\n```",
    "0600": {"is_ring": False, "what": "chain pendant", "name": None, "weights": [], "stones": [], "gem_count": None},
    "0601": {"is_ring": True, "name": "Pear Crown Ring", "weights": [{"karat": None, "grams": 2.05}],
             "stones": [{"shape": "Round", "length_mm": "1.20", "count": 16, "material": "Diamond"}, {"shape": "Round", "length_mm": "1.30", "count": 2, "material": "Ruby"},
                        {"shape": "Pear", "length_mm": "3.00", "width_mm": "2.00", "count": 1, "material": "Diamond"}], "gem_count": 19, "notes": "label G.W.T-2"},
    "0602": "this is not json at all",
    "0603": {"is_ring": True, "name": "Plain Wave Ring", "weights": [], "stones": [], "gem_count": None, "solitaire": False},
}


class Endpoint(http.server.BaseHTTPRequestHandler):
    calls = []

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        url = body["messages"][0]["content"][1]["image_url"]["url"]
        tag = base64.b64decode(url.split(",", 1)[1]).decode()          # the picture's content names it
        Endpoint.calls.append((tag, "temperature" in body, len(body["messages"][0]["content"]) - 1))
        a = ANSWERS[tag]
        text = a if isinstance(a, str) else json.dumps(a)
        out = json.dumps({"choices": [{"message": {"content": text}}]}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *a):
        pass


class ReadPictures(unittest.TestCase):
    def setUp(self):
        Endpoint.calls = []
        self.server = http.server.HTTPServer(("127.0.0.1", 0), Endpoint)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.tmp = tempfile.TemporaryDirectory()
        self.stage = Path(self.tmp.name)
        (self.stage / "pictures").mkdir()
        for n in ANSWERS:
            (self.stage / "pictures" / f"LR-{n}.jpg").write_text(n)
        (self.stage / "renders" / "LR-0598").mkdir(parents=True)
        (self.stage / "renders" / "LR-0598" / "1-yellow.png").write_text("0598")
        os.environ["CAD_API_KEY"] = "k"
        self.url = f"http://127.0.0.1:{self.server.server_port}/v1"
        rp.time.sleep = lambda s: None   # no waiting between retries in a test

    def tearDown(self):
        self.server.shutdown()
        self.tmp.cleanup()

    def run_read(self, *extra):
        rp.main([str(self.stage), "--model", "m", "--base-url", self.url, *extra])

    def rows(self):
        return {r["folder"]: r for r in csv.DictReader(open(self.stage / "design_list.csv", encoding="utf-8-sig"))}

    def test_writes_the_design_list(self):
        self.run_read()
        rows = self.rows()
        self.assertEqual(sorted(rows), ["LR-0598", "LR-0599", "LR-0601", "LR-0602", "LR-0603"])      # the pendant is not a row
        self.assertEqual((rows["LR-0598"]["weights"], rows["LR-0598"]["stones"], rows["LR-0598"]["gem_count"]), ("22=1.95;18=1.65", "1.18x26", "26"))
        self.assertEqual(rows["LR-0599"]["stones"], "1.10x18; 6.50x1")                               # fences, a comment and trailing commas
        self.assertEqual((rows["LR-0601"]["weights"], rows["LR-0601"]["stones"]), ("?=2.05", "1.20x16; 1.30x2*; pear 3.00*2.00x1"))  # Ruby: *
        self.assertEqual((rows["LR-0603"]["solitaire"], rows["LR-0603"]["stones"]), ("no", ""))
        self.assertIn("could not be read", rows["LR-0602"]["notes"])
        self.assertEqual(list(csv.DictReader(open(self.stage / "not_rings.csv", encoding="utf-8-sig"))), [{"folder": "LR-0600", "what": "chain pendant"}])
        self.assertTrue(all(c[1] for c in Endpoint.calls))                                          # temperature 0 was asked for

    def test_the_checker_takes_it_from_there(self):
        self.run_read()
        evals = {e.row["folder"]: e for e in chk.check(chk.read_rows(self.stage / "design_list.csv"))}
        self.assertEqual(evals["LR-0598"].problems, [])
        self.assertEqual([s.text() for s in evals["LR-0598"].stones], ["1.20x26"])                    # 1.18 rounded
        self.assertTrue(evals["LR-0599"].solitaire)
        self.assertEqual(evals["LR-0599"].problems, [])
        self.assertTrue(any("name is missing" in p for p in evals["LR-0602"].problems))              # unread: flagged, never imported

    def test_a_second_run_asks_nothing_and_refresh_asks_again(self):
        self.run_read()
        first = len(Endpoint.calls)
        self.run_read()
        self.assertEqual(len(Endpoint.calls), first + 3)          # only the unreadable picture is asked again (3 tries); nothing else
        n = len(Endpoint.calls)
        self.run_read("--refresh", "--limit", "1")
        self.assertEqual(len(Endpoint.calls), n + 1)

    def test_offset_and_limit_keep_the_other_rows(self):
        self.run_read("--limit", "2")
        self.assertEqual(sorted(self.rows()), ["LR-0598", "LR-0599"])
        self.run_read("--offset", "2")
        self.assertEqual(sorted(self.rows()), ["LR-0598", "LR-0599", "LR-0601", "LR-0602", "LR-0603"])

    def test_with_render_shows_a_second_picture(self):
        self.run_read("--limit", "1", "--with-render")
        self.assertEqual(Endpoint.calls[0][2], 2)

    def test_conversion(self):
        self.assertEqual(rp.row_from_answer("LR-0001", {"stones": [{"shape": "Round", "length_mm": "1.2", "count": None, "material": None}]})["stones"], "1.20x?")
        self.assertEqual(rp.row_from_answer("LR-0001", {"weights": [{"karat": 18, "grams": "1.5"}, {"karat": 18, "grams": 9}]})["weights"], "18=1.5")
        self.assertEqual(rp.row_from_answer("LR-0001", {"solitaire": True})["solitaire"], "yes")


if __name__ == "__main__":
    unittest.main()
