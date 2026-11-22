"""A small client for the portal's API (standard library only)."""
import json
import re
import urllib.error
import urllib.request
import uuid
from pathlib import Path


class Portal:
    def __init__(self, base):
        self.base, self.token = base.rstrip("/"), None

    def call(self, method, path, body=None, raw=None, ctype=None):
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = ctype or "application/json"
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(self.base + path, data, headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                text = r.read().decode() or "null"
                return r.status, json.loads(text), r.headers
        except urllib.error.HTTPError as e:
            text = e.read().decode()
            try:
                return e.code, json.loads(text), e.headers
            except ValueError:
                return e.code, {"error": text[:300]}, e.headers

    def ok(self, method, path, body=None, **kw):
        status, js, headers = self.call(method, path, body, **kw)
        if status >= 300:
            raise RuntimeError(f"{method} {path} -> {status} {json.dumps(js)[:300]}")
        return js, headers

    def login(self, email, password):
        js, _ = self.ok("POST", "/api/v1/auth/login", {"email": email, "password": password})
        self.token = js["token"]

    def existing_codes(self):
        codes, offset = set(), 0
        while True:
            js, headers = self.ok("GET", f"/api/v1/designs?limit=200&offset={offset}")
            codes.update(d["code"] for d in js)
            offset += 200
            if offset >= int(headers.get("X-Total-Count") or 0) or not js:
                return codes

    def deleted_codes(self):
        """Designs in the trash: their codes stay reserved."""
        js, _ = self.ok("GET", "/api/v1/trash")
        return {d["code"] for d in (js or {}).get("designs") or []}

    def next_number(self, prefix="LR"):
        """The highest number used under a prefix (the trash counts) plus one."""
        top = 0
        for code in self.existing_codes() | self.deleted_codes():
            m = re.match(rf"^{re.escape(prefix)}(\d+)$", code)
            if m:
                top = max(top, int(m.group(1)))
        return top + 1

    def tag_ids(self):
        """'group:name' (lower case) -> id."""
        js, _ = self.ok("GET", "/api/v1/masters/tags")
        out = {}
        for t in js or []:
            group = t.get("tag_group")
            group = group.get("name") if isinstance(group, dict) else group
            out[f"{group}:{t['name']}".lower()] = t["id"]
        return out

    def metal_codes(self):
        js, _ = self.ok("GET", "/api/v1/masters/metals")
        return {m["code"] for m in js or [] if m.get("active", True)}

    def matrix_pairs(self):
        js, _ = self.ok("GET", "/api/v1/masters/stone-matrix")
        return {(f"{float(r['size_mm']):.2f}", r["stone_code"]) for r in js or []}


def multipart(path: Path, name=None, ctype=None):
    boundary = uuid.uuid4().hex
    if not ctype:
        ctype = {".png": "image/png", ".webp": "image/webp", ".jpg": "image/jpeg", ".jpeg": "image/jpeg"}.get(path.suffix.lower(), "application/octet-stream")
    head = f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name or path.name}"\r\nContent-Type: {ctype}\r\n\r\n'.encode()
    return head + path.read_bytes() + f"\r\n--{boundary}--\r\n".encode(), f"multipart/form-data; boundary={boundary}"
