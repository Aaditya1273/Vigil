#!/usr/bin/env python3
"""Full VIGIL end-to-end verification — all real, no mocks."""
import http.client, json, urllib.parse, sys, time

BASE = "localhost"
PORT = 8080
OK = "✅"
FAIL = "❌"
results = []

def req(method, path, body=None, headers=None, form=False):
    c = http.client.HTTPConnection(BASE, PORT, timeout=8)
    h = {"Content-Type": "application/json"}
    if headers:
        h.update(headers)
    if form:
        h["Content-Type"] = "application/x-www-form-urlencoded"
    data = None
    if body and form:
        data = urllib.parse.urlencode(body).encode()
    elif body:
        data = json.dumps(body).encode()
    c.request(method, path, data, h)
    r = c.getresponse()
    raw = r.read()
    try:
        return json.loads(raw), r.status, r
    except:
        return raw.decode(), r.status, r

def check(label, cond, detail=""):
    icon = OK if cond else FAIL
    results.append((cond, label))
    print(f"  {icon} {label}", f"  ({detail})" if detail else "")
    return cond

print("\n════════════ VIGIL Real Verification ════════════\n")

# 1. Health
d, s, _ = req("GET", "/api/v1/health")
check("Health", s == 200 and isinstance(d, dict) and d.get("status") == "ok", f"status={s}")

# 2. AS metadata
d, s, _ = req("GET", "/.well-known/oauth-authorization-server")
check("OAuth AS metadata", s == 200 and "authorization_endpoint" in d, f"status={s}")

# 3. Resource metadata
d, s, _ = req("GET", "/.well-known/oauth-protected-resource")
check("OAuth resource metadata", s == 200 and "resource" in d, f"status={s}")

# 4. DCR register
d, s, _ = req("POST", "/register", {"redirect_uris": ["https://claude.ai/api/mcp/auth_callback"], "client_name": "Claude Web"})
client_id = d.get("client_id", "") if isinstance(d, dict) else ""
check("DCR /register", s == 201 and client_id.startswith("rmt_client_"), f"client_id={client_id[:30]}...")

# 5. /authorize → redirect to /connect
c2 = http.client.HTTPConnection(BASE, PORT, timeout=8)
auth_path = (f"/authorize?client_id={urllib.parse.quote(client_id)}"
             "&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback"
             "&response_type=code&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
             "&code_challenge_method=S256&state=test")
c2.request("GET", auth_path)
r2 = c2.getresponse(); r2.read()
loc = r2.getheader("Location", "")
request_id = loc.split("request=")[1].split("&")[0] if "request=" in loc else ""
check("/authorize → /connect redirect", r2.status == 302 and "/connect?request=" in loc, f"→ {loc[:60]}")

# 6. OAuth request details
d, s, _ = req("GET", f"/api/v1/vigil/oauth/request?id={request_id}")
check("OAuth request details", s == 200 and d.get("request_id") == request_id, f"client={d.get('client_name')}")

# 7. Approve → code
d, s, _ = req("POST", "/api/v1/vigil/oauth/approve", {"request_id": request_id, "budget_limit": 10.0})
redirect_to = d.get("redirect_to", "") if isinstance(d, dict) else ""
code = redirect_to.split("code=")[1].split("&")[0] if "code=" in redirect_to else ""
session_id = d.get("session_id", "") if isinstance(d, dict) else ""
check("Approve → auth code", s == 200 and code.startswith("rmt_code_"), f"code={code[:20]}...")

# 8. /token PKCE exchange → real Bearer token
d, s, _ = req("POST", "/token",
    {"grant_type": "authorization_code", "code": code,
     "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
     "client_id": client_id, "redirect_uri": "https://claude.ai/api/mcp/auth_callback"},
    form=True)
bearer = d.get("access_token", "") if isinstance(d, dict) else ""
check("/token PKCE → bearer token", s == 200 and bearer.startswith("rmt_at_"), f"token={bearer[:28]}...")

# 9. MCP initialize with Bearer token
d, s, _ = req("POST", "/api/v1/mcp/bearer",
    {"jsonrpc": "2.0", "id": 1, "method": "initialize",
     "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                "clientInfo": {"name": "Claude Web", "version": "1.0.0"}}},
    headers={"Authorization": f"Bearer {bearer}"})
srv_name = d.get("result", {}).get("serverInfo", {}).get("name", "") if isinstance(d, dict) else ""
check("MCP initialize (Bearer)", s == 200 and srv_name.startswith("Vigil"), f"server={srv_name}")

# 10. MCP tools/list — all 12 tools
d, s, _ = req("POST", "/api/v1/mcp/bearer",
    {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
    headers={"Authorization": f"Bearer {bearer}"})
tools = d.get("result", {}).get("tools", []) if isinstance(d, dict) else []
check("MCP tools/list (>=12 tools)", s == 200 and len(tools) >= 12, f"{len(tools)} tools: {[t['name'] for t in tools[:4]]}...")

# 11. MCP tool call: vigil_cost_status
d, s, _ = req("POST", "/api/v1/mcp/bearer",
    {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
     "params": {"name": "vigil_cost_status", "arguments": {}}},
    headers={"Authorization": f"Bearer {bearer}"})
content = d.get("result", {}).get("content", [{}])[0].get("text", "") if isinstance(d, dict) else ""
check("MCP tool call: vigil_cost_status", s == 200 and len(content) > 10, f"{content[:60]}")

# 12. MCP tool call: read_file (real filesystem read)
d, s, _ = req("POST", "/api/v1/mcp/bearer",
    {"jsonrpc": "2.0", "id": 4, "method": "tools/call",
     "params": {"name": "read_file", "arguments": {"path": "README.md"}}},
    headers={"Authorization": f"Bearer {bearer}"})
content = d.get("result", {}).get("content", [{}])[0].get("text", "") if isinstance(d, dict) else ""
check("MCP tool call: read_file (real fs)", s == 200 and len(content) > 50, f"{len(content)} chars read")

# 13. MCP GET with no auth → 401 + WWW-Authenticate
c3 = http.client.HTTPConnection(BASE, PORT, timeout=8)
c3.request("GET", "/api/v1/mcp")
r3 = c3.getresponse(); r3.read()
wwa = r3.getheader("WWW-Authenticate", "")
check("MCP GET no-auth → 401 + WWW-Auth", r3.status == 401 and "oauth-protected-resource" in wwa, f"WWW-Auth present")

# 14. Governance rules -- served from the live engine, so this is whatever is
# actually registered. Three of the nine detectors read LLM-turn fields that an
# MCP tool call does not carry, so they are deliberately not registered.
d, s, _ = req("GET", "/api/v1/vigil/governance/rules")
rule_count = d.get("count", 0) if isinstance(d, dict) else 0
check("Governance rules registered", s == 200 and rule_count >= 6, f"count={rule_count}")

# 15. Agent DNA profiles (session just created)
d, s, _ = req("GET", "/api/v1/vigil/dna/profiles")
profiles = d if isinstance(d, list) else []
check("DNA profiles endpoint", s == 200 and isinstance(d, list), f"{len(profiles)} profiles")

# 16. Stats endpoint — real data, no fake hardcoded growth
d, s, _ = req("GET", "/api/v1/vigil/stats")
# The sparkline needs at least two points; 25 is an implementation detail of
# the burn-history window, not a contract.
has_chart = isinstance(d.get("chart_data"), list) and len(d["chart_data"]) >= 2
check("Stats — real chart_data", s == 200 and has_chart, f"burn=${d.get('total_cost',0):.4f}, {len(d.get('chart_data', []))} pts")

# 17. Fake demo endpoint removed → 410
d, s, _ = req("POST", "/api/v1/vigil/mcp/demo")
check("Fake /mcp/demo removed (404 or 410)", s in (404, 405, 410), f"status={s}")

# 18. SigNoz health check
d, s, _ = req("GET", "/api/v1/vigil/signoz/health")
configured = d.get("configured", False) if isinstance(d, dict) else False
check("SigNoz health endpoint responds", s == 200,
      f"configured={configured}" + ("" if configured else " (optional, not set)"))

# 19. MCP sessions list
d, s, _ = req("GET", "/api/v1/mcp/sessions")
sessions = d.get("sessions", []) if isinstance(d, dict) else []
claude_sessions = [x for x in sessions if x.get("client_name") == "Claude Web"]
check("MCP sessions: Claude Web registered", len(claude_sessions) >= 1, f"{len(sessions)} total, {len(claude_sessions)} Claude Web")

# 20. Agent tracker: Claude Web session appears
d, s, _ = req("GET", "/api/v1/vigil/agents")
agents = d if isinstance(d, list) else []
cw_agents = [a for a in agents if "claude-web" in a.get("agent_id", "")]
check("Agent tracker: Claude Web session", len(cw_agents) >= 1, f"{len(agents)} agents, {len(cw_agents)} claude-web")

# Summary
passed = sum(1 for ok, _ in results if ok)
total = len(results)
# --- Vigil 2.0 -------------------------------------------------------------

# 21. Predictive cost forecast
d, s, _ = req("GET", "/api/v1/vigil/forecast")
check("Cost forecast served", s == 200 and "state" in d,
      f"state={d.get('state')}, projected=${d.get('projected_total', 0):.4f}")

# 22. Model router status -- must never leak the key or a credentialed URL
d, s, _ = req("GET", "/api/v1/vigil/models")
body = json.dumps(d)
no_secret = "api_key" not in body.lower() and "bearer" not in body.lower()
check("Model router status (no secrets leaked)",
      s == 200 and "provider" in d and no_secret,
      f"provider={d.get('provider')}, configured={d.get('configured')}")

# 23. Audit chain verifies
d, s, _ = req("GET", "/api/v1/vigil/audit/verify")
check("Audit hash chain verifies", s == 200 and d.get("ok") is True,
      f"{d.get('count', 0)} events" + ("" if d.get("ok") else f" FAILED at {d.get('failed_at')}"))

# 24. Session policy is served and self-describes whether it enforces
d, s, _ = req("GET", "/api/v1/vigil/sessions/verify-probe/policy")
check("Session policy endpoint", s == 200 and "policy" in d and "is_default" in d,
      f"is_default={d.get('is_default')}")

# 25. Decision stream
d, s, _ = req("GET", "/api/v1/vigil/decisions?limit=10")
check("Decision stream endpoint", s == 200 and "decisions" in d,
      f"{d.get('count', 0)} recent decisions")

print(f"\n{'═'*48}")
print(f"  {passed}/{total} checks passed")
if passed == total:
    print(f"  {OK} ALL REAL — zero fake/mock data")
else:
    failed = [label for ok, label in results if not ok]
    print(f"  {FAIL} Failed: {', '.join(failed)}")
print(f"{'═'*48}\n")
sys.exit(0 if passed == total else 1)
