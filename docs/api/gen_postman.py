#!/usr/bin/env python3
"""Builds the Postman collection from openapi.yaml (the single source of truth).

    python3 docs/api/gen_postman.py

Writes docs/api/CadPortal.postman_collection.json. Needs PyYAML.
Import it into Postman together with docs/api/CadPortal.postman_environment.json.
"""
import json
import pathlib
import re
import yaml

HERE = pathlib.Path(__file__).parent
spec = yaml.safe_load((HERE / "openapi.yaml").read_text())
METHODS = ("get", "post", "put", "delete")

# Path parameters get readable collection variables instead of raw {id}.
VAR_FOR = {
    "/api/v1/users/{id}": "userId", "/api/v1/users/{id}/password": "userId", "/api/v1/users/{id}/reset-link": "userId",
    "/api/v1/masters/categories/{id}": "categoryId", "/api/v1/masters/tags/{id}": "tagId",
    "/api/v1/masters/metals/{id}": "metalId", "/api/v1/masters/stone-codes/{id}": "stoneCodeId",
    "/api/v1/designs/{id}": "designId", "/api/v1/designs/{id}/duplicate": "designId",
    "/api/v1/designs/{id}/variations": "designId", "/api/v1/designs/{id}/restore": "designId",
    "/api/v1/designs/{id}/media": "designId",
    "/api/v1/media/{id}": "mediaId", "/api/v1/media/{id}/cover": "mediaId",
    "/api/v1/trash/files/{id}/restore": "trashedFileId",
    "/api/v1/costing-sheets/{id}": "sheetId", "/api/v1/costing-sheets/{id}/csv": "sheetId",
    "/api/v1/costing-sheets/{id}/recost": "sheetId",
}


def var_name(path):
    if path in VAR_FOR:
        return VAR_FOR[path]
    return "variationId" if "/variations/" in path else "id"


def resolve(node):
    """Follow a local $ref."""
    while isinstance(node, dict) and "$ref" in node:
        cur = spec
        for part in node["$ref"][2:].split("/"):
            cur = cur[part]
        node = cur
    return node


def example_of(schema, depth=0):
    """A small example value built from a schema (used when no example is written)."""
    schema = resolve(schema)
    if depth > 6 or not isinstance(schema, dict):
        return None
    if "example" in schema:
        return schema["example"]
    for key in ("oneOf", "anyOf"):
        if key in schema:
            return example_of(schema[key][0], depth + 1)
    if "allOf" in schema:
        out = {}
        for part in schema["allOf"]:
            v = example_of(part, depth + 1)
            if isinstance(v, dict):
                out.update(v)
        return out
    t = schema.get("type")
    if t == "object" or "properties" in schema:
        return {k: example_of(v, depth + 1) for k, v in schema.get("properties", {}).items()}
    if t == "array":
        return [example_of(schema.get("items", {}), depth + 1)]
    if t == "integer":
        return 1
    if t == "number":
        return 0
    if t == "boolean":
        return False
    if "enum" in schema:
        return schema["enum"][0]
    return ""


def description(op):
    parts = [op.get("description", "").strip()]
    resp = [f"{code}: {resolve(r).get('description', '')}" for code, r in op.get("responses", {}).items()]
    parts.append("Responses: " + "; ".join(resp))
    return "\n\n".join(p for p in parts if p)


def build_item(path, method, op, global_security):
    public = op.get("security") == [] or (op.get("security") is None and not global_security)
    parts = [p for p in path.strip("/").split("/")]
    url_path = []
    for p in parts:
        m = re.fullmatch(r"\{(\w+)\}", p)
        if m and m.group(1) == "id":
            url_path.append("{{%s}}" % var_name(path))
        elif m:
            url_path.append(("{{%s}}" % ("designCode" if m.group(1) == "code" else m.group(1))))
        else:
            url_path.append(p)
    query = []
    for prm in op.get("parameters", []):
        prm = resolve(prm)
        if prm["in"] != "query":
            continue
        ex = prm.get("example", prm.get("schema", {}).get("example", ""))
        query.append({"key": prm["name"], "value": "" if ex is None else str(ex),
                      "disabled": not prm.get("required", False),
                      "description": prm.get("description", "")})
    header = []
    req = {
        "method": method.upper(),
        "header": header,
        "url": {"raw": "{{baseUrl}}/" + "/".join(url_path) + (
            "?" + "&".join(f"{q['key']}={q['value']}" for q in query if not q["disabled"]) if any(not q["disabled"] for q in query) else ""),
                "host": ["{{baseUrl}}"], "path": url_path, "query": query},
        "description": description(op),
    }
    if public:
        req["auth"] = {"type": "noauth"}
    body = op.get("requestBody")
    if body:
        content = resolve(body)["content"]
        if "application/json" in content:
            media = content["application/json"]
            ex = media.get("example")
            if ex is None and "examples" in media:
                ex = next(iter(media["examples"].values()))["value"]
            if ex is None:
                ex = example_of(media.get("schema", {}))
            header.append({"key": "Content-Type", "value": "application/json"})
            req["body"] = {"mode": "raw", "raw": json.dumps(ex, indent=2),
                           "options": {"raw": {"language": "json"}}}
        elif "multipart/form-data" in content:
            props = resolve(content["multipart/form-data"]["schema"]).get("properties", {})
            form = []
            for name, sch in props.items():  # order matters: variation_id before file
                if sch.get("format") == "binary":
                    form.append({"key": name, "type": "file", "src": []})
                else:
                    form.append({"key": name, "type": "text", "value": "", "disabled": True})
            req["body"] = {"mode": "formdata", "formdata": form}
    item = {"name": op["summary"], "request": req, "response": []}
    events = []
    if path == "/api/v1/auth/login" or path == "/api/v1/auth/change-password":
        events.append({"listen": "test", "script": {"type": "text/javascript", "exec": [
            "// Keep the bearer token for the other requests.",
            "if (pm.response.code === 200) {",
            "  pm.collectionVariables.set('token', pm.response.json().token);",
            "}"]}})
    if events:
        item["event"] = events
    return item


def main():
    global_security = bool(spec.get("security"))
    folders = {t["name"]: [] for t in spec["tags"]}
    for path, ops in spec["paths"].items():
        for method in METHODS:
            if method in ops:
                op = ops[method]
                folders[op["tags"][0]].append(build_item(path, method, op, global_security))
    coll = {
        "info": {
            "name": spec["info"]["title"],
            "description": spec["info"]["description"],
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}", "type": "string"}]},
        "variable": [
            {"key": "baseUrl", "value": "http://localhost:8080"},
            {"key": "token", "value": ""},
            {"key": "designCode", "value": "LR0001"},
        ] + [{"key": k, "value": "1"} for k in sorted(set(VAR_FOR.values()) | {"variationId", "id"})],
        "item": [{"name": name, "item": items} for name, items in folders.items() if items],
    }
    (HERE / "CadPortal.postman_collection.json").write_text(json.dumps(coll, indent=2) + "\n")
    env = {
        "name": "CadPortal local",
        "values": [
            {"key": "baseUrl", "value": "http://localhost:8080", "enabled": True},
            {"key": "token", "value": "", "type": "secret", "enabled": True},
        ],
        "_postman_variable_scope": "environment",
    }
    (HERE / "CadPortal.postman_environment.json").write_text(json.dumps(env, indent=2) + "\n")
    n = sum(len(f["item"]) for f in coll["item"])
    print(f"{n} requests in {len(coll['item'])} folders")


main()
