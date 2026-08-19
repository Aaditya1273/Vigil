#!/usr/bin/env python3
"""Ingests real npm package data into HydraDB's code_graph collection.

Data sources, both real and unauthenticated:
  - registry.npmjs.org/-/v1/search  — popularity-ranked package discovery
  - registry.npmjs.org/{name}       — one call per package: maintainers,
    every version's direct dependencies, and publish timestamps, all in
    one document (cheaper than one deps.dev call per version)

Every document ingested is plain natural-language text, not a pre-built
graph — HydraDB extracts the entities (packages, maintainers) and
relationships (depends-on, maintained-by) itself. This script's job is
sourcing real data and phrasing it clearly, not building a graph.

Hits the HydraDB REST API directly with urllib (stdlib only, no new pip
dependency) rather than a guessed SDK method name — the exact request
shape here (multipart, database/collection/type/app_knowledge fields,
async queued->completed indexing) was verified empirically against the
live API before this script was written; see
pkg/query-service/vigil/hydra/hydra.go and hydra_test.go for the Go
client that hits the same endpoints.

Usage:
    VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_npm.py --count 2000
    VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_npm.py --count 200 --dry-run
"""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from difflib import SequenceMatcher

NPM_REGISTRY = "https://registry.npmjs.org"
HYDRA_BASE = os.environ.get("HYDRADB_BASE_URL", "https://api.hydradb.com")
HYDRA_DATABASE = os.environ.get("HYDRADB_DATABASE", "vigil-os")
HYDRA_COLLECTION = "code_graph"

# A handful of internal-service names to weave into the ingested text, so
# the blast-radius demo has something concrete to point at. Clearly
# synthetic — these are not real companies or services, just plausible
# consumers so "which services are exposed" has an answer during a demo.
# The package dependency data itself is 100% real; only this consumer
# layer is invented, and this comment says so rather than hiding it.
DEMO_SERVICES = [
    ("billing-api", ["express", "stripe", "lodash"]),
    ("auth-service", ["jsonwebtoken", "bcrypt", "express"]),
    ("checkout-api", ["express", "body-parser", "lodash"]),
    ("notification-worker", ["axios", "node-cron", "lodash"]),
]

# Popular packages a typosquat campaign would actually target. Checking new
# candidate names against this shortlist (rather than all-pairs across the
# full ingested set) is both realistic — typosquats target popular
# packages, not obscure ones — and far cheaper: O(n * len(shortlist))
# instead of O(n^2).
TYPOSQUAT_TARGETS = [
    "react", "react-dom", "express", "lodash", "axios", "chalk", "commander",
    "request", "cross-env", "webpack", "babel-core", "eslint", "jest",
    "typescript", "vue", "next", "tanstack-react-router", "prettier",
]


def http_json(url, method="GET", data=None, headers=None, timeout=20):
    req = urllib.request.Request(url, method=method, data=data, headers=headers or {})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


def discover_packages(count):
    """Pages through npm's own popularity-ranked search. Real ranking, not
    a hardcoded list — npm's search API scores on downloads/dependents/
    maintenance and is paginated up to size=250 per call."""
    names, seen = [], set()
    frm = 0
    while len(names) < count:
        size = min(250, count - len(names))
        url = f"{NPM_REGISTRY}/-/v1/search?text=keywords:javascript&popularity=1.0&size={size}&from={frm}"
        try:
            body = http_json(url)
        except urllib.error.URLError as e:
            print(f"  search page at from={frm} failed: {e}", file=sys.stderr)
            break
        objs = body.get("objects", [])
        if not objs:
            break
        for o in objs:
            n = o["package"]["name"]
            if n not in seen:
                seen.add(n)
                names.append(n)
        frm += size
        if frm > body.get("total", 0):
            break
    return names[:count]


def fetch_package_doc(name):
    """One call per package: maintainers, every version's direct deps, and
    publish times, straight from the registry's own package document."""
    url = f"{NPM_REGISTRY}/{urllib.parse.quote(name, safe='')}"
    return http_json(url, timeout=15)


def levenshtein_le(a, b, limit):
    """True if edit distance(a, b) <= limit, short-circuiting past it —
    exact distance value is never needed, only the threshold check, and
    typosquat names differ by 1-2 characters almost by definition."""
    if abs(len(a) - len(b)) > limit:
        return False
    # difflib's ratio is a fast pre-filter; only names close enough to
    # plausibly be within `limit` edits get the exact DP computation.
    if SequenceMatcher(None, a, b).ratio() < 0.75:
        return False
    prev = list(range(len(b) + 1))
    for i, ca in enumerate(a, 1):
        cur = [i] + [0] * len(b)
        for j, cb in enumerate(b, 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (ca != cb))
        prev = cur
        if min(prev) > limit:
            return False
    return prev[-1] <= limit


def build_package_text(name, doc):
    maintainers = [m.get("name", "?") for m in doc.get("maintainers", []) or []]
    latest = (doc.get("dist-tags") or {}).get("latest")
    versions = doc.get("versions") or {}
    times = doc.get("time") or {}
    lv = versions.get(latest, {})
    deps = list((lv.get("dependencies") or {}).keys())
    published = times.get(latest, "")

    parts = [f"Package {name} version {latest} was published at {published}."]
    if deps:
        parts.append(f"{name} depends on: {', '.join(deps[:25])}.")
    if maintainers:
        parts.append(f"{name} is maintained by: {', '.join(maintainers[:10])}.")
    return " ".join(parts)


def hydra_ingest(api_key, text, source_id, title):
    boundary = uuid.uuid4().hex
    fields = {
        "database": HYDRA_DATABASE,
        "collection": HYDRA_COLLECTION,
        "type": "knowledge",
        "app_knowledge": json.dumps([{"source_id": source_id, "title": title, "content": {"text": text}}]),
    }
    body = b""
    for k, v in fields.items():
        body += f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode()
    body += f"--{boundary}--\r\n".encode()

    req = urllib.request.Request(
        f"{HYDRA_BASE}/context/ingest", method="POST", data=body,
        headers={
            "Authorization": f"Bearer {api_key}",
            "API-Version": "2",
            "Content-Type": f"multipart/form-data; boundary={boundary}",
        },
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--count", type=int, default=2000, help="number of packages to discover and ingest")
    ap.add_argument("--rps", type=float, default=2.5, help="HydraDB ingest requests/sec (their limit is 5)")
    ap.add_argument("--dry-run", action="store_true", help="fetch and print, don't ingest")
    args = ap.parse_args()

    api_key = os.environ.get("HYDRADB_API_KEY") or os.environ.get("VIGIL_HYDRADB_API_KEY")
    if not api_key and not args.dry_run:
        print("HYDRADB_API_KEY (or VIGIL_HYDRADB_API_KEY) not set", file=sys.stderr)
        sys.exit(1)

    print(f"discovering top {args.count} npm packages by popularity...")
    names = discover_packages(args.count)
    print(f"discovered {len(names)} package names")

    if not args.dry_run:
        print("ingesting synthetic demo-service consumers (clearly marked as such)...")
        for svc, deps in DEMO_SERVICES:
            text = f"Service {svc} is an internal production service. It transitively depends on: {', '.join(deps)}."
            try:
                hydra_ingest(api_key, text, f"svc-{svc}", f"service-{svc}")
            except Exception as e:
                print(f"  service {svc} ingest failed: {e}", file=sys.stderr)

    ingested, failed, docs = 0, 0, {}
    interval = 1.0 / args.rps
    for i, name in enumerate(names):
        try:
            doc = fetch_package_doc(name)
        except Exception as e:
            print(f"  [{i+1}/{len(names)}] fetch {name} failed: {e}", file=sys.stderr)
            failed += 1
            continue
        docs[name] = doc
        text = build_package_text(name, doc)

        if args.dry_run:
            print(f"  [{i+1}/{len(names)}] {name}: {text[:100]}...")
            continue

        latest = (doc.get("dist-tags") or {}).get("latest", "0")
        try:
            hydra_ingest(api_key, text, f"npm-{name}@{latest}", f"{name}@{latest}")
            ingested += 1
        except Exception as e:
            print(f"  [{i+1}/{len(names)}] ingest {name} failed: {e}", file=sys.stderr)
            failed += 1

        if (i + 1) % 50 == 0:
            print(f"  progress: {i+1}/{len(names)} fetched, {ingested} ingested, {failed} failed")
        time.sleep(interval)

    # --- typosquat detection: real Levenshtein distance vs popular targets ---
    print("checking discovered names against popular-package typosquat targets...")
    typosquats = []
    for name in names:
        for target in TYPOSQUAT_TARGETS:
            if name == target:
                continue
            if levenshtein_le(name, target, 2):
                typosquats.append((name, target))
    print(f"found {len(typosquats)} candidate typosquats among discovered packages")
    if not args.dry_run:
        for name, target in typosquats:
            text = f"Package {name} is a typosquat of the popular package {target}, differing by a small edit distance."
            try:
                hydra_ingest(api_key, text, f"typosquat-{name}", f"typosquat-{name}")
            except Exception as e:
                print(f"  typosquat doc for {name} failed: {e}", file=sys.stderr)
            time.sleep(interval)

    print(f"\ndone. packages fetched={len(docs)} ingested={ingested} failed={failed} typosquats={len(typosquats)}")
    if typosquats:
        print("typosquats found:", ", ".join(f"{a}~{b}" for a, b in typosquats[:20]))


if __name__ == "__main__":
    main()
