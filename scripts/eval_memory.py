#!/usr/bin/env python3
"""Track 03 evaluation harness — real measurement, not the spec's example scores.

Measures one specific thing: retrieval recall — for each real LongMemEval
question, does HydraDB's response to the question actually contain the
ground-truth answer's key terms? This is not a full LLM-graded QA pipeline
(the actual LongMemEval protocol uses GPT-4 as a judge over a generated
free-form answer) — it measures whether the graph surfaces the fact an
answer would be built from, which is what this product's abstention layer
(hydra.QueryResult.Abstains) and every downstream consumer actually depend
on. That's a real, disclosed scope: it answers "did the retrieval layer do
its job," not "would an LLM reading this get the wording right."

Two things are measured against scripts/data/memory_manifest.json (written
by ingest_memory.py, so this only evaluates what was actually ingested):

1. Per-category recall on answerable_questions — the answer's own keywords
   present in what HydraDB returned for the real question text.
2. Abstention accuracy on held_out_questions — sessions that were
   deliberately never ingested, so a genuine "not in history" is the only
   correct outcome; this uses the exact same content-presence check as
   hydra.QueryResult.Abstains in the Go client, kept in sync by hand since
   this script has no access to the Go build.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time

HYDRA_BASE = os.environ.get("HYDRADB_BASE_URL", "https://api.hydradb.com")
HYDRA_DATABASE = os.environ.get("HYDRADB_DATABASE") or os.environ.get("VIGIL_HYDRADB_DATABASE", "vigil-os")
MANIFEST_PATH = os.path.join(os.path.dirname(__file__), "data", "memory_manifest.json")

STOPWORDS = {
    "what", "when", "where", "which", "who", "does", "have", "this", "that",
    "with", "from", "about", "there", "been", "seen", "before", "current",
    "value", "history", "your", "were", "will", "would", "could", "should",
}


def hydra_query(api_key, q, mode="fast", retries=3):
    body = json.dumps({
        "database": HYDRA_DATABASE, "collection": "agent_memory", "query": q,
        "type": "memory", "mode": mode, "graph_context": True,
    })
    # Python's http.client raised IncompleteRead on this endpoint's larger
    # graph_context responses intermittently and non-deterministically,
    # surviving both a retry loop and a plain urllib rewrite — curl never
    # reproduced it once across dozens of manual runs against the identical
    # endpoint during debugging, so this shells out to curl instead of
    # chasing what looks like an http.client-specific chunked-transfer
    # handling quirk rather than a real server or query problem.
    for attempt in range(retries):
        try:
            out = subprocess.run(
                ["curl", "-sS", "-X", "POST", f"{HYDRA_BASE}/query",
                 "-H", f"Authorization: Bearer {api_key}", "-H", "API-Version: 2",
                 "-H", "Content-Type: application/json", "-d", body],
                capture_output=True, text=True, timeout=30, check=True,
            )
            return json.loads(out.stdout)
        except (subprocess.CalledProcessError, json.JSONDecodeError):
            if attempt == retries - 1:
                raise
            time.sleep(1)


def keywords(text):
    # A handful of real LongMemEval answers are bare counts (int, e.g. 4),
    # not strings — str() them rather than crash; a single digit is too
    # short to pass the length filter anyway, so those are naturally
    # excluded from scoring rather than silently miscounted.
    return [w for w in re.findall(r"[A-Za-z0-9']{4,}", str(text)) if w.lower() not in STOPWORDS]


def response_text(resp):
    data = resp.get("data", {})
    parts = [json.dumps(data.get("chunks", []))]
    for cr in data.get("graph_context", {}).get("chunk_relations", []):
        parts.append(cr.get("combined_context", ""))
        for t in cr.get("triplets", []):
            parts.append(f"{t['source']['name']} {t['relation']['canonical_predicate']} {t['target']['name']}")
    return " ".join(parts).lower()


def found(resp, answer_text):
    kws = keywords(answer_text)
    if not kws:
        return None  # nothing meaningful to check — excluded from scoring
    text = response_text(resp)
    hits = sum(1 for kw in kws if kw.lower() in text)
    return hits / len(kws) >= 0.4  # a plurality of the answer's own terms, not every one


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--interval", type=float, default=1.0)
    args = ap.parse_args()

    api_key = os.environ.get("HYDRADB_API_KEY") or os.environ.get("VIGIL_HYDRADB_API_KEY")
    if not api_key:
        print("HYDRADB_API_KEY not set", file=sys.stderr)
        sys.exit(1)

    if not os.path.exists(MANIFEST_PATH):
        print(f"{MANIFEST_PATH} not found — run scripts/ingest_memory.py first", file=sys.stderr)
        sys.exit(1)
    manifest = json.load(open(MANIFEST_PATH))

    print(f"evaluating {len(manifest['answerable_questions'])} answerable + "
          f"{len(manifest['held_out_questions'])} held-out real LongMemEval questions\n", file=sys.stderr)

    by_cat = {}
    for q in manifest["answerable_questions"]:
        resp = hydra_query(api_key, q["question"])
        ok = found(resp, q["answer"])
        cat = by_cat.setdefault(q["question_type"], {"hit": 0, "total": 0, "skipped": 0})
        if ok is None:
            cat["skipped"] += 1
        else:
            cat["total"] += 1
            cat["hit"] += int(ok)
        time.sleep(args.interval)

    correct_abstentions, abstain_total = 0, 0
    for q in manifest["held_out_questions"]:
        resp = hydra_query(api_key, q["question"])
        # Correct abstention: the answer's terms genuinely do not appear —
        # same "content presence, not confidence score" signal as
        # hydra.QueryResult.Abstains, since the confidence/graph_context
        # checks the task originally proposed were tested directly this
        # session and do not discriminate (see README, Track 03 section).
        ok = found(resp, q["answer"])
        if ok is not None:
            abstain_total += 1
            if not ok:
                correct_abstentions += 1
        time.sleep(args.interval)

    print("=== REAL evaluation summary (retrieval recall, not the spec's example numbers) ===\n")
    print(f"{'category':<28}{'recall':>10}{'  (n, skipped)'}")
    overall_hit, overall_total = 0, 0
    for cat, s in sorted(by_cat.items()):
        pct = 100 * s["hit"] / s["total"] if s["total"] else float("nan")
        print(f"{cat:<28}{pct:>9.2f}%  ({s['total']}, skipped {s['skipped']})")
        overall_hit += s["hit"]
        overall_total += s["total"]
    if overall_total:
        print(f"{'OVERALL':<28}{100*overall_hit/overall_total:>9.2f}%  ({overall_total} scored)")

    if abstain_total:
        print(f"\n{'abstention accuracy':<28}{100*correct_abstentions/abstain_total:>9.2f}%  "
              f"({correct_abstentions}/{abstain_total})")
    else:
        print("\nno held-out questions had scorable answers")


if __name__ == "__main__":
    main()
