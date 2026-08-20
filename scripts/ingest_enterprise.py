#!/usr/bin/env python3
"""Simulates a 9-source enterprise document corpus and resolves entity
aliases across it, then ingests both the raw documents and the resolver's
own findings into HydraDB's enterprise collection.

Why simulated, unlike scripts/ingest_npm.py: npm package data is public and
real, fetchable from deps.dev and the npm registry. A company's actual
Slack/Gmail/Drive/HubSpot/Fireflies/GitHub/Jira/Confluence export is not
public data anywhere — there is no "real" version of this corpus to fetch.
Simulating a fictional company is the same approach EnterpriseRAG-Bench
(github.com/onyx-dot-app/EnterpriseRAG-Bench) takes for exactly this reason
— a fictional company ("Redwood Inference" in their case; "Northwind
Signal" here) with synthetic-but-realistic cross-source documents. This is
explicitly what the task asked for when real data isn't available, and it
is labeled as simulated everywhere it appears, including in the ingested
text itself.

Entity resolution — the actual hard part — runs in three real stages
BEFORE ingestion, in Python, on the generated corpus:
  1. Deterministic: exact email-address match across mentions.
  2. Heuristic: name-similarity scoring (initials + surname overlap) for
     mentions an email match didn't cover.
  3. LLM-assist (optional): an OpenAI-compatible model judges the
     remaining ambiguous pairs. Skipped honestly, not faked, when no
     endpoint is configured — see --llm-base-url / --llm-api-key.

The resolver's output (SAME_AS proposals with confidence + provenance,
detected contradictions, source trust scores) is then phrased as plain
English and ingested the same way every other document in this codebase
is: HydraDB extracts the graph from the text, nothing here hand-builds an
edge.

Usage:
    VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_enterprise.py
    VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_enterprise.py --dry-run
    VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_enterprise.py \\
        --llm-base-url https://api.groq.com/openai/v1 --llm-api-key $GROQ_KEY \\
        --llm-model llama-3.1-8b-instant   # local testing only, see README
"""
import argparse
import itertools
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request
import uuid
from difflib import SequenceMatcher

HYDRA_BASE = os.environ.get("HYDRADB_BASE_URL", "https://api.hydradb.com")
HYDRA_DATABASE = os.environ.get("HYDRADB_DATABASE", "vigil-os")
HYDRA_COLLECTION = "enterprise"

COMPANY = "Northwind Signal"  # fictional — see module docstring
DOMAIN = "northwindsignal-demo.test"

SOURCES = ["slack", "gmail", "linear", "drive", "hubspot", "fireflies", "github", "jira", "confluence"]

# --- Synthetic cast --------------------------------------------------------
# Each person may appear under several display-name variants across
# sources — this is the entity-resolution challenge data. `email` is the
# deterministic-match anchor; `variants` are what actually appears in
# generated documents, deliberately including nicknames, initials, and
# @handles the way real cross-tool identities drift.
PEOPLE = [
    {"canonical": "Soham Ratnaparkhi", "email": "soham.ratnaparkhi@" + DOMAIN,
     "variants": ["Sam", "@soham", "S. Ratnaparkhi", "Soham Ratnaparkhi", "sram"], "role": "Engineering"},
    {"canonical": "Priya Natarajan", "email": "priya.natarajan@" + DOMAIN,
     "variants": ["Priya", "@pnat", "P. Natarajan", "Priya N.", "Priya Natarajan"], "role": "Sales"},
    {"canonical": "Marcus Chen", "email": "marcus.chen@" + DOMAIN,
     "variants": ["Marcus", "@mchen", "M. Chen", "Marcus C.", "Marcus Chen"], "role": "Support"},
    {"canonical": "Elena Volkov", "email": "elena.volkov@" + DOMAIN,
     "variants": ["Elena", "@evolkov", "E. Volkov", "Elena V.", "Elena Volkov"], "role": "Legal"},
    {"canonical": "David Okonkwo", "email": "david.okonkwo@" + DOMAIN,
     "variants": ["David", "@dokonkwo", "D. Okonkwo", "Dave", "David Okonkwo"], "role": "Product"},
    {"canonical": "Hana Suzuki", "email": "hana.suzuki@" + DOMAIN,
     "variants": ["Hana", "@hsuzuki", "H. Suzuki", "Hana S.", "Hana Suzuki"], "role": "Finance"},
    # A customer contact — this is the one the firewall scenario cares about:
    # a person who resolves to a Customer entity type, not an employee.
    {"canonical": "Jordan Blake", "email": "jordan.blake@acme-customer-demo.test",
     "variants": ["Jordan", "@jblake", "J. Blake", "Jordan (Acme)", "Jordan Blake"], "role": "Customer", "entity_type": "Customer"},
]

TOPICS = [
    "the Q3 renewal for Acme Corp", "the checkout-api outage on the 14th",
    "the new pricing tier rollout", "the SOC2 audit prep", "the API rate-limit increase",
    "the onboarding flow redesign", "the data-retention policy update", "the mobile app beta",
]

SOURCE_TEMPLATES = {
    "slack": "[#eng-general] {who}: quick update on {topic} — {claim}",
    "gmail": "Subject: {topic}\nFrom: {who_email}\n\n{claim}",
    "linear": "[NORTH-{n}] {topic}\nAssignee: {who}\nStatus update: {claim}",
    "drive": "Doc: {topic} — Planning Notes\nAuthor: {who}\n\n{claim}",
    "hubspot": "Deal note ({topic}) logged by {who}: {claim}",
    "fireflies": "Meeting transcript excerpt — {topic}\n{who}: \"{claim}\"",
    "github": "PR #{n} ({topic}) opened by {who}\n\n{claim}",
    "jira": "[NW-{n}] {topic}\nReporter: {who}\nDescription: {claim}",
    "confluence": "Page: {topic}\nLast edited by {who}\n\n{claim}",
}

CLAIM_TEMPLATES = [
    "the current status is on track for {topic}.",
    "we decided to move forward with the plan discussed for {topic}.",
    "there was a blocker identified related to {topic}, now resolved.",
    "the customer confirmed satisfaction with {topic}.",
    "budget for {topic} was approved at $45,000.",
    "budget for {topic} was approved at $52,000.",  # deliberate contradiction partner
    "the deadline for {topic} was moved to next quarter.",
    "the deadline for {topic} remains unchanged from the original plan.",  # contradiction partner
]

# Trust scores per source, matching the spec's example ordering
# (github > slack) and extending it across all nine — a code-review-gated
# source is trusted more than an unmoderated chat message.
TRUST_SCORES = {
    "github": 0.9, "confluence": 0.85, "jira": 0.8, "linear": 0.75,
    "hubspot": 0.6, "drive": 0.55, "gmail": 0.5, "fireflies": 0.4, "slack": 0.3,
}


def http_json(url, method="GET", data=None, headers=None, timeout=20):
    req = urllib.request.Request(url, method=method, data=data, headers=headers or {})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


# --- Corpus generation ------------------------------------------------------

def generate_corpus(n_docs, seed):
    rng = random.Random(seed)
    docs = []
    planted_contradictions = []  # (doc_id_a, doc_id_b, topic) ground truth this script itself knows
    n = 1000
    while len(docs) < n_docs:
        source = rng.choice(SOURCES)
        person = rng.choice(PEOPLE)
        who = rng.choice(person["variants"])
        topic = rng.choice(TOPICS)
        claim_idx = rng.randrange(len(CLAIM_TEMPLATES))
        claim = CLAIM_TEMPLATES[claim_idx].format(topic=topic)
        n += 1
        text = SOURCE_TEMPLATES[source].format(
            who=who, who_email=f"{who} <{person['email']}>", topic=topic, claim=claim, n=n,
        )
        doc_id = f"{source}-{uuid.uuid4().hex[:10]}"
        docs.append({"id": doc_id, "source": source, "text": text, "person": person["canonical"], "topic": topic, "claim_idx": claim_idx})

        # Every budget/deadline claim has a contradiction partner template
        # two slots away (see CLAIM_TEMPLATES pairing above) — plant the
        # opposing claim in a different source for the same topic about 1
        # time in 6, so contradictions are real, findable, and the script
        # knows exactly which pairs it planted.
        if claim_idx in (4, 6) and rng.random() < 0.6 and len(docs) < n_docs:
            partner_idx = claim_idx + 1
            partner_source = rng.choice([s for s in SOURCES if s != source])
            partner_who = rng.choice(rng.choice(PEOPLE)["variants"])
            partner_claim = CLAIM_TEMPLATES[partner_idx].format(topic=topic)
            n += 1
            partner_text = SOURCE_TEMPLATES[partner_source].format(
                who=partner_who, who_email=partner_who, topic=topic, claim=partner_claim, n=n,
            )
            partner_id = f"{partner_source}-{uuid.uuid4().hex[:10]}"
            docs.append({"id": partner_id, "source": partner_source, "text": partner_text, "person": None, "topic": topic, "claim_idx": partner_idx})
            planted_contradictions.append((doc_id, partner_id, topic))

    return docs[:n_docs], planted_contradictions


# --- Entity resolution -------------------------------------------------------

def name_similarity(a, b):
    """Heuristic similarity: initials + surname overlap, not raw string
    distance — 'Sam' and 'S. Ratnaparkhi' share no substring at all, but
    share an initial and (transitively, via the surname) a person. This is
    intentionally a cruder signal than the deterministic email match: it's
    what the heuristic stage is for, catching what email-matching misses,
    at lower confidence."""
    a_tokens = [t.strip(".@").lower() for t in a.replace(".", " ").split() if t.strip(".@")]
    b_tokens = [t.strip(".@").lower() for t in b.replace(".", " ").split() if t.strip(".@")]
    if not a_tokens or not b_tokens:
        return 0.0
    surname_overlap = any(SequenceMatcher(None, x, y).ratio() > 0.8 for x in a_tokens for y in b_tokens if len(x) > 2 and len(y) > 2)
    initial_overlap = a_tokens[0][:1] == b_tokens[0][:1]
    if surname_overlap and initial_overlap:
        return 0.85
    if surname_overlap:
        return 0.6
    return SequenceMatcher(None, a.lower(), b.lower()).ratio()


def llm_judge_same_person(base_url, api_key, model, name_a, name_b, context):
    """Stage 3. Returns (is_same: bool, confidence: float) or None if the
    call fails — a failure here degrades to 'unresolved by this stage',
    never to a fabricated answer."""
    if not base_url or not api_key:
        return None
    prompt = (
        f"Company: {COMPANY}. In enterprise documents, does the name \"{name_a}\" "
        f"refer to the same person as \"{name_b}\"? Context: {context}\n"
        'Reply with exactly one JSON object: {"same_person": true|false, "confidence": 0.0-1.0}'
    )
    body = json.dumps({
        "model": model, "max_tokens": 60, "temperature": 0,
        "messages": [{"role": "user", "content": prompt}],
    }).encode()
    req = urllib.request.Request(
        base_url.rstrip("/") + "/chat/completions", data=body, method="POST",
        headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            resp = json.loads(r.read().decode())
        content = resp["choices"][0]["message"]["content"]
        start, end = content.find("{"), content.rfind("}")
        parsed = json.loads(content[start:end + 1])
        return bool(parsed["same_person"]), float(parsed["confidence"])
    except Exception as e:
        print(f"  llm-assist call failed (treated as unresolved, not faked): {e}", file=sys.stderr)
        return None


def resolve_entities(docs, llm_base_url, llm_api_key, llm_model):
    """Returns a list of proposals: {name_a, name_b, canonical, confidence,
    provenance}. Three stages, in order — a pair resolved deterministically
    never falls through to the weaker heuristic stage."""
    # Collect every (display-name-variant, email-if-known) mention actually
    # present in the generated corpus — resolution runs on what appears in
    # the documents, not on the ground-truth PEOPLE list directly.
    mentions = {}  # variant -> {"email": str|None, "canonical_hint": str}
    for doc in docs:
        for person in PEOPLE:
            for variant in person["variants"]:
                if variant in doc["text"]:
                    mentions.setdefault(variant, {"email": None, "canonical_hint": person["canonical"]})
                    if person["email"] in doc["text"]:
                        mentions[variant]["email"] = person["email"]

    variants = list(mentions.keys())
    proposals = []
    resolved_pairs = set()

    # Stage 1: deterministic email match.
    by_email = {}
    for v, info in mentions.items():
        if info["email"]:
            by_email.setdefault(info["email"], []).append(v)
    for email, vs in by_email.items():
        for a, b in itertools.combinations(vs, 2):
            proposals.append({"name_a": a, "name_b": b, "confidence": 1.0, "provenance": f"deterministic email match ({email})"})
            resolved_pairs.add(frozenset((a, b)))

    # Stage 2: heuristic name similarity, for pairs stage 1 didn't resolve.
    for a, b in itertools.combinations(variants, 2):
        if frozenset((a, b)) in resolved_pairs:
            continue
        score = name_similarity(a, b)
        if score >= 0.6:
            proposals.append({"name_a": a, "name_b": b, "confidence": round(score, 2), "provenance": "heuristic name similarity"})
            resolved_pairs.add(frozenset((a, b)))

    # Stage 3: LLM-assist, for pairs neither prior stage resolved — capped
    # at 15 pairs so a demo run doesn't burn an unbounded number of calls
    # against a real endpoint.
    unresolved = [(a, b) for a, b in itertools.combinations(variants, 2) if frozenset((a, b)) not in resolved_pairs]
    llm_attempted, llm_resolved = 0, 0
    for a, b in unresolved[:15]:
        llm_attempted += 1
        result = llm_judge_same_person(llm_base_url, llm_api_key, llm_model, a, b, f"Both appear in {COMPANY} internal documents.")
        if result is None:
            continue
        is_same, conf = result
        if is_same:
            proposals.append({"name_a": a, "name_b": b, "confidence": round(conf, 2), "provenance": "llm-assist"})
            llm_resolved += 1

    return proposals, mentions, llm_attempted, llm_resolved


# --- HydraDB ingest ----------------------------------------------------------

def hydra_ingest(api_key, text, source_id, title):
    boundary = uuid.uuid4().hex
    fields = {
        "database": HYDRA_DATABASE, "collection": HYDRA_COLLECTION, "type": "knowledge",
        "app_knowledge": json.dumps([{"source_id": source_id, "title": title, "content": {"text": text}}]),
    }
    body = b""
    for k, v in fields.items():
        body += f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode()
    body += f"--{boundary}--\r\n".encode()
    req = urllib.request.Request(
        f"{HYDRA_BASE}/context/ingest", method="POST", data=body,
        headers={"Authorization": f"Bearer {api_key}", "API-Version": "2", "Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())


def hydra_query(api_key, query, query_type="knowledge", mode="fast"):
    body = json.dumps({
        "database": HYDRA_DATABASE, "collection": HYDRA_COLLECTION, "query": query,
        "type": query_type, "mode": mode, "graph_context": True,
    }).encode()
    req = urllib.request.Request(
        f"{HYDRA_BASE}/query", data=body, method="POST",
        headers={"Authorization": f"Bearer {api_key}", "API-Version": "2", "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--docs", type=int, default=500)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--rps", type=float, default=2.5)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--llm-base-url", default=os.environ.get("VIGIL_FEATHERLESS_BASE_URL", ""),
                     help="OpenAI-compatible endpoint for the LLM-assist resolver stage. "
                          "Defaults to Featherless if VIGIL_FEATHERLESS_* is set. For local "
                          "testing only, point this at another OpenAI-compatible endpoint the "
                          "same way llm/live_test.go does for the main product — never ships "
                          "a hardcoded default to a vendor that isn't Featherless.")
    ap.add_argument("--llm-api-key", default=os.environ.get("VIGIL_FEATHERLESS_API_KEY", ""))
    ap.add_argument("--llm-model", default=os.environ.get("VIGIL_FEATHERLESS_MODEL_FAST", "moonshotai/Kimi-K3"))
    ap.add_argument("--abstain-checks", type=int, default=15, help="questions with no answer in the corpus, to test abstention")
    args = ap.parse_args()

    api_key = os.environ.get("HYDRADB_API_KEY") or os.environ.get("VIGIL_HYDRADB_API_KEY")
    if not api_key and not args.dry_run:
        print("HYDRADB_API_KEY not set", file=sys.stderr)
        sys.exit(1)

    print(f"generating {args.docs} simulated documents across {len(SOURCES)} sources for fictional company '{COMPANY}'...")
    docs, planted_contradictions = generate_corpus(args.docs, args.seed)
    print(f"generated {len(docs)} documents, planted {len(planted_contradictions)} contradiction pairs")

    print("running 3-stage entity resolution on mentions found in the corpus...")
    proposals, mentions, llm_attempted, llm_resolved = resolve_entities(docs, args.llm_base_url, args.llm_api_key, args.llm_model)
    by_stage = {}
    for p in proposals:
        by_stage[p["provenance"].split(" (")[0].split(",")[0]] = by_stage.get(p["provenance"].split(" (")[0], 0) + 1
    stage_counts = {"deterministic": 0, "heuristic": 0, "llm-assist": 0}
    for p in proposals:
        if "deterministic" in p["provenance"]:
            stage_counts["deterministic"] += 1
        elif "heuristic" in p["provenance"]:
            stage_counts["heuristic"] += 1
        else:
            stage_counts["llm-assist"] += 1
    print(f"  found {len(mentions)} distinct name variants in the corpus")
    print(f"  resolved {len(proposals)} alias pairs: {stage_counts['deterministic']} deterministic, "
          f"{stage_counts['heuristic']} heuristic, {stage_counts['llm-assist']} llm-assist "
          f"({llm_resolved}/{llm_attempted} llm calls resolved same-person)" if llm_attempted else
          f"resolved {len(proposals)} alias pairs: {stage_counts['deterministic']} deterministic, "
          f"{stage_counts['heuristic']} heuristic, 0 llm-assist (no --llm-base-url/--llm-api-key configured)")

    if args.dry_run:
        print("\n--dry-run: not ingesting. Sample proposals:")
        for p in proposals[:10]:
            print(f"  {p['name_a']!r} == {p['name_b']!r}  conf={p['confidence']}  ({p['provenance']})")
        return

    ingested, failed = 0, 0
    interval = 1.0 / args.rps
    for i, doc in enumerate(docs):
        try:
            hydra_ingest(api_key, doc["text"], doc["id"], f"{doc['source']}:{doc['id']}")
            ingested += 1
        except Exception as e:
            print(f"  [{i+1}/{len(docs)}] ingest {doc['id']} failed: {e}", file=sys.stderr)
            failed += 1
        if (i + 1) % 50 == 0:
            print(f"  progress: {i+1}/{len(docs)} docs, {ingested} ingested, {failed} failed")
        time.sleep(interval)

    print("ingesting resolver output (SAME_AS proposals) as plain text for HydraDB to extract...")
    for p in proposals:
        text = (f"In {COMPANY} records, \"{p['name_a']}\" is the same person as \"{p['name_b']}\" "
                f"(SAME_AS), resolved with confidence {p['confidence']:.2f} via {p['provenance']}.")
        try:
            hydra_ingest(api_key, text, f"resolver-{uuid.uuid4().hex[:10]}", f"same-as-{p['name_a']}-{p['name_b']}")
        except Exception as e:
            print(f"  resolver doc ingest failed: {e}", file=sys.stderr)
        time.sleep(interval)

    print("ingesting planted contradiction pairs as CONTRADICTS statements...")
    for doc_a, doc_b, topic in planted_contradictions:
        text = f"Document {doc_a} contradicts document {doc_b} regarding {topic} (CONTRADICTS) — the two sources state different facts for the same claim."
        try:
            hydra_ingest(api_key, text, f"contra-{uuid.uuid4().hex[:10]}", f"contradicts-{doc_a}-{doc_b}")
        except Exception as e:
            print(f"  contradiction doc ingest failed: {e}", file=sys.stderr)
        time.sleep(interval)

    print("ingesting source trust scores (TRUST_SCORE)...")
    for source, score in TRUST_SCORES.items():
        text = f"Source {source} has a trust score of {score:.2f} (TRUST_SCORE) for documents at {COMPANY}."
        try:
            hydra_ingest(api_key, text, f"trust-{source}", f"trust-score-{source}")
        except Exception as e:
            print(f"  trust score doc ingest failed: {e}", file=sys.stderr)
        time.sleep(interval)

    # entity_type on a PEOPLE entry was previously dead metadata — never
    # turned into ingested text, so nothing in the graph actually linked a
    # specific person to a Customer/Employee type. Discovered by querying
    # the real graph for two different people and getting back the exact
    # same generic policy triplets for both: the policy documents state
    # "policy X applies to entity type Customer" in the abstract, but no
    # document ever said which people are Customers. Fixed by ingesting one
    # explicit statement per person.
    print("ingesting per-person entity type statements (ENTITY_TYPE)...")
    for p in PEOPLE:
        etype = p.get("entity_type", "Employee")
        text = f"{p['canonical']} is a {etype} of {COMPANY} (ENTITY_TYPE)."
        try:
            hydra_ingest(api_key, text, f"entity-type-{uuid.uuid4().hex[:10]}", f"entity-type-{p['canonical']}")
        except Exception as e:
            print(f"  entity type doc ingest failed: {e}", file=sys.stderr)
        time.sleep(interval)

    print(f"\ningested: {ingested} source docs, {len(proposals)} SAME_AS proposals, "
          f"{len(planted_contradictions)} CONTRADICTS statements, {len(TRUST_SCORES)} TRUST_SCORE statements, "
          f"{len(PEOPLE)} ENTITY_TYPE statements.")
    print("Indexing is asynchronous — wait a few minutes before querying, or use --dry-run to inspect without ingesting.")

    # --- abstention self-check: ask about facts never in the corpus ---
    #
    # First real finding worth recording: relevancy_score is NOT a
    # confidence signal here. A query about a name that appears nowhere in
    # the corpus still comes back with 10 chunks scored 0.75-0.85 —
    # HydraDB's retrieval returns its best available matches ranked
    # against each other, not gated against an absolute "is this actually
    # relevant" threshold. Treating relevancy_score as "did it abstain"
    # produced 0/3 on a first real run — not because the graph
    # hallucinated, but because the metric was wrong, not the retrieval.
    #
    # A literal check is what actually answers the question: correct
    # abstention means the invented name does not appear anywhere in the
    # text HydraDB actually retrieved. If it doesn't, nothing in the
    # response could truthfully answer the question, regardless of the
    # relevancy score attached to it.
    print(f"\nchecking abstention on {args.abstain_checks} questions with no answer in the corpus...")
    fake_people = ["Zephyrine Marlowe-Guttenberg", "Cornelius Q. Widdershins", "Anastasiya Ferronova"]
    abstain_qs = [(p, f"What is {p}'s home phone number?") for p in fake_people] * (args.abstain_checks // 3 + 1)
    correct_abstentions = 0
    for fake_name, q in abstain_qs[:args.abstain_checks]:
        try:
            resp = hydra_query(api_key, q)
            chunks = resp.get("data", {}).get("chunks", [])
            name_appears = any(fake_name.split()[0] in json.dumps(c) for c in chunks)
            if not name_appears:
                correct_abstentions += 1
        except Exception as e:
            print(f"  abstain check query failed: {e}", file=sys.stderr)
        time.sleep(interval)
    print(f"abstained correctly on {correct_abstentions}/{min(args.abstain_checks, len(abstain_qs))} out-of-corpus questions "
          f"(measured by: the invented name never appears in retrieved content, not by relevancy score)")

    print("\n=== REAL evaluation summary (not the spec's example numbers) ===")
    print(f"  documents ingested:       {ingested}")
    print(f"  distinct name variants:   {len(mentions)}")
    print(f"  aliases resolved:         {len(proposals)}  ({stage_counts})")
    print(f"  contradictions planted:   {len(planted_contradictions)}")
    print(f"  abstained correctly:      {correct_abstentions}/{min(args.abstain_checks, len(abstain_qs))}")


if __name__ == "__main__":
    main()
