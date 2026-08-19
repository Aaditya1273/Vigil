#!/usr/bin/env bash
#
# Vigil 2.0 — one-command demonstration.
#
#   ./demo/run_demo.sh            run all seven scenes
#   ./demo/run_demo.sh --scene 4  run one scene (for debugging)
#
# Adopts an already-running server on :8080 if there is one, otherwise builds
# and starts its own and tears it down on exit. It only ever kills what it
# started, so running this against a dev server you already have open is safe.
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${VIGIL_PORT:-8080}"
BASE="http://localhost:${PORT}"
TMP="$(mktemp -d)"
OWNED=0
SRV_PID=""

cleanup() {
  if [[ "$OWNED" == "1" && -n "$SRV_PID" ]]; then
    kill "$SRV_PID" 2>/dev/null || true
    wait "$SRV_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "──────────────────────────────────────────────"
echo "  Vigil 2.0 — runtime firewall demonstration"
echo "──────────────────────────────────────────────"

# --- Server: adopt or start -------------------------------------------------
if curl -sf "${BASE}/api/v1/health" >/dev/null 2>&1; then
  echo "  server       : adopted, already running on :${PORT}"
else
  echo "  server       : building…"
  go build -o "${TMP}/vigil-server" ./cmd/vigil-server

  export VIGIL_AUDIT_PATH="${VIGIL_AUDIT_PATH:-${TMP}/vigil-audit.jsonl}"
  export VIGIL_BUDGET_LIMIT="${VIGIL_BUDGET_LIMIT:-2.00}"
  export VIGIL_ORG_ID="${VIGIL_ORG_ID:-demo}"
  # Scene 4 needs the shell tool reachable so the firewall has something
  # dangerous to refuse. It is off by default in normal deployments.
  export VIGIL_ALLOW_EXEC="${VIGIL_ALLOW_EXEC:-true}"

  "${TMP}/vigil-server" > "${TMP}/server.log" 2>&1 &
  SRV_PID=$!
  OWNED=1

  for _ in $(seq 1 60); do
    curl -sf "${BASE}/api/v1/health" >/dev/null 2>&1 && break
    sleep 0.5
  done
  if ! curl -sf "${BASE}/api/v1/health" >/dev/null 2>&1; then
    echo "  server       : FAILED to start — see ${TMP}/server.log"
    tail -20 "${TMP}/server.log"
    exit 1
  fi
  echo "  server       : started (pid ${SRV_PID})"
fi

# --- Inference: ask the server, never imply a key we do not have -------------
# The running server is the only honest source. Reading the shell environment
# would report "no key" whenever the server loaded one from .env.local, and
# would report a key present even if it were rejected on the first call.
curl -sf "${BASE}/api/v1/vigil/models" 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("  inference    : unknown (status endpoint unreachable)"); sys.exit()
if not d.get("configured"):
    print("  inference    : deterministic-only (no vendor configured)"); sys.exit()
chain = " → ".join(
    v["vendor"] + ("" if v["live"] else " [retired]") for v in d.get("vendors") or []
) or d.get("provider", "?")
print(f"  inference    : {chain}")
'

echo "  budget       : \$${VIGIL_BUDGET_LIMIT:-2.00}"
echo "  log          : ${TMP}/server.log"
echo

VIGIL_BASE="${BASE}" VIGIL_LOG="${TMP}/server.log" python3 demo/scenes.py "$@"
