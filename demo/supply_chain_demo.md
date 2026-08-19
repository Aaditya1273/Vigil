# Supply-Chain Blast Radius — 3-Minute Demo Script

Scenario framing: a compromise pattern like the described "TanStack worm" —
dozens of malicious package versions published across a short window. The
specific numbers (84 artifacts, 42 packages, 6 minutes) are given scenario
parameters for this demo's narrative, not a claim this codebase independently
verified about a real historical incident — say so if asked.

Everything below runs against the real HydraDB API and the real npm-derived
`code_graph` collection populated by `scripts/ingest_npm.py`. Nothing in this
script is simulated; the query time shown on screen is whatever HydraDB
actually returns at that moment, not a fixed number.

---

## Before recording

```bash
# 1. Confirm the graph has real data (not empty)
VIGIL_HYDRADB_API_KEY=... vigil-cli hydra-seed --wait   # if not already seeded
VIGIL_HYDRADB_API_KEY=... python3 scripts/ingest_npm.py --count 500   # real npm data

# 2. Declare the incident: the compromised package(s), by name or name@version
export VIGIL_COMPROMISED_PACKAGES="evil-tanstack-plugin,left-pad@9.9.9"

# 3. Start the server with that env var set
./vigil-server
```

## 0:00–0:30 — The compromise

**Say:**
> "At 09:00:00, a maintainer account is compromised. Over the next six
> minutes, malicious versions are published across dozens of packages —
> the pattern behind incidents like the TanStack worm. Most agent
> runtimes have no way to answer 'am I exposed?' faster than someone
> manually checking package-lock files."

**Show:** terminal, `echo $VIGIL_COMPROMISED_PACKAGES` — the operator has
already pushed the incident list into the running process (`VIGIL_COMPROMISED_PACKAGES`,
no restart required if pushed via the env-reload path).

## 0:30–1:00 — Agent tries the install

**Say:**
> "An agent — doing exactly what agents do, keeping dependencies current —
> tries to install one of the compromised packages."

**Show:** the MCP tool call (via `demo/scenes.py`-style script or a direct
`tools/call`):
```json
{"name": "run_command", "arguments": {"command": "npm install evil-tanstack-plugin"}}
```

## 1:00–1:30 — Vigil intercepts and queries the real graph

**Say:**
> "Vigil recognizes the package is on the incident list. It doesn't wait
> for a typosquat heuristic to notice something's off — it already knows.
> But knowing 'block' isn't enough for an incident responder. It queries
> HydraDB's code_graph collection for the actual blast radius."

**Show:** the decision result / server log — point at the real numbers:
```
BLOCK — package evil-tanstack-plugin is on the compromised-package list
  — N service(s) exposed, N shared maintainer(s), N typosquat(s) found in NNNms
```

**Say, pointing at the real millisecond number on screen — not a scripted one:**
> "That's the real query time HydraDB just reported, not a number we
> typed into a slide."

## 1:30–2:00 — The graph, visually

**Show:** `/blast-radius` dashboard page, same package.

**Say:**
> "Three separate real queries, three real graphs: what depends on this
> package, who shares its maintainer — an account takeover exposes
> everything that maintainer touches, not just the package that got
> attention first — and what's impersonating it."

## 2:00–2:30 — Block confirmed, audit logged

**Show:** the audit trail / HydraDB audit collection entry for this decision.

**Say:**
> "The block, the exposed-services list, and the graph paths behind it are
> all in the tamper-evident audit chain — and mirrored into HydraDB's own
> audit collection, so 'why was this blocked' is itself a graph-queryable
> question six months from now, not a line in a log file nobody greps."

## 2:30–3:00 — Close

**Say:**
> "A vector database would have told you these packages are semantically
> similar. It couldn't tell you which services actually depend on one, or
> that its maintainer also owns three other packages in your dependency
> tree. That's the difference between a similarity search and a real
> graph traversal — and it's why this uses HydraDB instead of an
> embedding index."

---

## Honesty notes for whoever presents this

- **Query time**: real, measured, shown live. In testing this session it
  ran 500ms (isolated, `mode=fast`) to several seconds (under concurrent
  ingestion load on the same account). Do not claim a fixed number in
  the pitch; read whatever the screen shows.
- **Node count**: `scripts/ingest_npm.py` ingests real npm packages via
  the npm registry and deps.dev. Ingestion is rate-limited (HydraDB caps
  at 5 req/s) and asynchronous server-side, so reaching a large node
  count is a real multi-hour operation, not something that completes
  during a demo recording — run it well in advance.
- **The compromised list is operator-declared**, not a live threat feed.
  This is the honest, correct model for incident response (security
  publishes a confirmed list; the firewall enforces it immediately) —
  it is not a claim that Vigil independently discovers new attacks.
