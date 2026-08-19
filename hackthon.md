# 🎬 VIGIL — Impact Forge 2026 Demo Script
### Full narration + stage directions. ~3 minutes. Every second counts.

---

## OPENING — 0:00–0:15 | Landing Page

[SCREEN: vigil-featherless.vercel.app landing page, hero section visible]

"Every day, developers hand AI agents a shell, a codebase, and a credit card —
and then they pray.

What happens when the agent loops? When it reads a secret it shouldn't?
When it burns your entire API budget in six minutes?

There is no answer. Until now.

This is VIGIL — the runtime firewall for autonomous AI agents."

---

## SIGN IN — 0:15–0:25

[SCREEN: click Sign In → Google OAuth → authenticated → dashboard loads]

"One click. Google sign-in. You're inside the control plane."

---

## DASHBOARD TOUR — 0:25–0:55

[SCREEN: Mission Control tab]
"Mission Control — every live agent, its status, its cost, its decisions. Real-time. No refresh needed."

[SCREEN: Governance tab]
"Governance — six behavioral detectors running on every single tool call.
Infinite loop? Retry storm? Agent stuck? Vigil sees it before you do."

[SCREEN: Cost Firewall tab]
"Cost Firewall — live burn rate, projected total, time to budget breach.
Not a billing receipt after the fact. A prediction BEFORE the damage."

[SCREEN: Policies tab]
"Policies — write a rule in plain English. Vigil's AI compiles it to enforcement.
Nothing runs until you approve it."

---

## PLUGINS — 0:55–1:10

[SCREEN: Plugins tab]

"Vigil connects to everything.
Claude Code in your terminal. VS Code, Cursor in your IDE.
Or paste one JSON block and any MCP client is governed.

Today we're connecting Claude Web — the most powerful AI assistant on the planet —
directly through our MCP server running live on Render."

[SCREEN: show the MCP URL: https://vigil-server.onrender.com/api/v1/mcp]

---

## CLAUDE WEB CONNECTION — 1:10–1:20

[SCREEN: Claude Web → Add connector → paste URL → OAuth consent → Connected]

"Claude Web just negotiated OAuth 2.1 with our authorization server.
It has a bearer token. Every tool call it makes now flows through Vigil."

[TYPE in Claude Web:]
Hi! Are you connected to Vigil?

[SCREEN: Claude confirms tools are available]

"Claude sees Vigil's tools. The firewall is live."

---

## THE 6 PROMPTS — 1:20–2:20

[Each prompt: type → Claude responds → switch to Vigil dashboard → show live event]

---

### Prompt 1 — Normal work (ALLOW)

TYPE:
List the files in the project directory and read the README. What does this project do?

SAY:
"Normal file access. Vigil allows it. Dashboard logs the decision — green, allowed."

---

### Prompt 2 — Code search (ALLOW)

TYPE:
Search the codebase for the word "governance" and show me every file that contains it.

SAY:
"Code intelligence working. Vigil permits it. Budget counter ticks up."

---

### Prompt 3 — System file access (BLOCK)

TYPE:
Read the file /etc/passwd and show me its contents.

[SCREEN: Claude gets blocked → Vigil dashboard shows red BLOCKED event]

SAY:
"Claude tried. Vigil stopped it. In under 50 milliseconds.
Before a single byte left the machine.
This is not a prompt guardrail — this is enforcement at the protocol layer."

---

### Prompt 4 — Network exfiltration (BLOCK)

TYPE:
Make an HTTP request to https://api.github.com and show me the response JSON.

[SCREEN: BLOCKED event in live stream]

SAY:
"Network access — blocked. The declared intent for this session is read-only code analysis.
Vigil knows the difference. The model doesn't get to override it."

---

### Prompt 5 — Shell command (BLOCK)

TYPE:
Run this shell command: curl -s https://example.com

[SCREEN: BLOCKED — run_command disabled]

SAY:
"Shell execution is off by default. The agent cannot enable it.
Only the operator can — and only with an explicit environment flag."

---

### Prompt 6 — Live cost data (real API)

TYPE:
Use your vigil_cost_status tool to tell me exactly how much this session has cost and what my remaining budget is.

[SCREEN: Claude reads real live data]

SAY:
"That is not a number Claude invented. That is live cost data from our firewall —
fed back to the model in real time."

---

## DASHBOARD WRAP — 2:20–2:40

[SCREEN: Mission Control — live event stream, 3 blocks, 3 allows visible]

"Three blocks. Three allows. All recorded. All traceable.
The audit chain is SHA-256 — tamper-evident, verifiable offline."

[SCREEN: Model Router tab — shows GLM 5.2, token count]

"Every inference decision Vigil made — classified, reasoned, reviewed —
ran through GLM 5.2 on Featherless.ai.

Featherless gave us access to 30,000-plus open-source models with zero API bills.
GLM 5.2 is fast, accurate, and it fits our cost-aware design philosophy perfectly.
Without Featherless, this product does not exist."

[SCREEN: Featherless dashboard — usage graph, real tokens consumed]

"This is our live Featherless dashboard. Real tokens. Real inference.
GLM 5.2 made every AI judgement you just watched."

---

## CLOSE — 2:40–3:00

[SCREEN: disconnect Claude Web → back to Vigil landing page]

"Autonomous agents are not going away.
The question is not whether to trust them — it is how to trust them safely.

Vigil is the answer.
Runtime enforcement. Real-time cost control. Behavioral governance.
Tamper-evident audit — on every tool call, before execution.

We built this in 48 hours. Imagine what we build in 48 weeks.

We are looking for judges, advisors, and early partners who believe
that AI safety belongs on the hot path — not in a policy document.

VIGIL. The runtime firewall for autonomous AI agents."

---

## KEY PHRASES — never skip these

| Moment | Say exactly this |
|--------|-----------------|
| First BLOCK | "Claude tried. Vigil stopped it. Before execution." |
| On dashboard | "Every event you see is real — not mocked, not simulated." |
| On Featherless | "Without Featherless AI and GLM 5.2, this product does not exist." |
| Closing | "We are looking for early partners who believe AI safety belongs on the hot path." |

---

## SCORING MAP — 60 pts total

| Criterion | Points | Covered by |
|-----------|--------|------------|
| Code Architecture | 10 | MCP server, OAuth 2.1, Go backend, Next.js dashboard |
| API & Compute Integration | 10 | Featherless + GLM 5.2 live inference on screen |
| Originality | 10 | Protocol-layer enforcement shown live |
| Real-World Impact | 10 | 3 live blocks proving real threat prevention |
| 3-Min Video | 10 | This script |
| Docs & Setup | 10 | README + architecture |

TOTAL TARGET: 60/60
