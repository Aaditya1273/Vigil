# Vigil — 2-Minute Demo Video Script

Goal: prove Vigil actually intercepts and governs a real agent's tool calls in
real time — not a mockup. Every screen below is a real running page/endpoint,
not a slide. Record at 1920x1080, screen + mic, no edits needed if you follow
the beats.

Total: **2:00**. Timings are targets, not laws — stay close.

---

## Before you hit record

1. `./demo/run_demo.sh` once, so the audit ledger and dashboard have real
   events in it (an empty dashboard looks fake even when it isn't).
2. Backend running on `:8080`, frontend on `:3000`, both tabs pre-opened.
3. A terminal tab pre-opened at the repo root, font size bumped up.
4. `VIGIL_FEATHERLESS_API_KEY` set in `.env.local` if you have one — that's
   what makes scene 3 show a real model verdict instead of "no model
   consulted." Featherless is the only vendor Vigil ships against now; if you
   don't have a key yet, that's fine — deterministic-only is a real,
   honest state to demo, not a fallback to apologize for.
5. Close Slack/notifications. Judges watch the whole screen.

---

## 0:00–0:15 — Hook (landing page)

**Show:** `localhost:3000` landing page, scroll once through the hero.

**Say:**
> "Autonomous agents call tools with no budget ceiling and no behavioral
> baseline. One prompt-injected loop can burn your API budget or exfiltrate
> data before anyone notices. Vigil is a runtime firewall that sits between
> the agent and its tools — it decides allow, pause, or block *before* the
> tool executes, not after."

Don't linger on the landing page — 15 seconds, then move. Judges care about
the product working, not the marketing site.

---

## 0:15–0:30 — Connect a real agent (OAuth + MCP)

**Show:** terminal, run:
```bash
./demo/run_demo.sh
```
Let it print the banner (adopted/started, inference vendor, budget) — this is
real OAuth 2.1 + PKCE happening, not a fake handshake.

**Say:**
> "This is a real MCP client authenticating through Vigil's OAuth 2.1 server
> — the same flow Claude Desktop or Claude Web would use to connect to this
> firewall as an MCP proxy in front of their tools."

Let scene 1 output start scrolling as you talk — don't wait for it to finish
before narrating.

---

## 0:30–1:30 — The firewall deciding, live (the core 60 seconds)

This is the section that proves the product. Narrate over the scrolling
terminal output; don't stop and explain each line — point at it as it happens.

**Scene 1 (normal ops, ~5s):**
> "Three benign tool calls — read a file, list a directory, search code. All
> three ALLOW. No model was consulted — deterministic checks handle the happy
> path, so inference cost is zero when nothing is wrong."

**Scene 2 (suspicious loop, ~5s):**
> "Now the agent repeats the same call five times in a row — a classic
> stuck-loop failure mode. The behavioral detector fires and kills the
> session before the sixth call executes."

**Scene 3 (AI judgement, ~15s) — the money shot:**
> "This call falls outside the agent's declared intent — deterministic rules
> can't resolve it alone, so it escalates to a real language model as a
> security judge."

Point at the `risk NN/100 via moonshotai/Kimi-K3` line.

> "That's a live model call to Featherless, returning a structured risk
> score that gets validated before it can influence the decision. If no
> model were configured, this falls back to deterministic rules instead of
> guessing — it never fakes a verdict."

**Scene 4 (runtime block, ~10s):**
> "Here the agent tries to curl an external URL and read a `.env` file — both
> explicitly forbidden by its declared intent. Blocked before execution,
> not logged after the fact."

**Scene 5 (predictive cost, ~10s):**
> "Vigil also forecasts spend — burn rate, projected cost, time-to-breach —
> so a session gets flagged before it blows the budget, not after."

**Scene 6 + 7 (routing + audit, ~10s):**
> "And every decision — allow or block — is written to a tamper-evident
> SHA-256 hash chain. If anyone edits a past record, verification catches it
> at the exact event that was altered."

Let the final `7/7 scenes passed` line land on screen for a beat.

---

## 1:30–1:50 — The dashboard (visual proof, not just terminal)

**Show:** switch to `localhost:3000`, already logged in.

Click through fast — 3-4 seconds each, don't dwell:

1. **Mission Control** — live event stream, the calls you just watched happen
   in the terminal, now as rows with decision/risk/cost/model.
2. **Cost Firewall** — the burn rate + projected cost from scene 5, as a
   chart, not a number.
3. **Model Router** — which model actually served the judgement (Kimi-K3 or
   GLM-5.2, via Featherless), with request/latency counts. Say: *"this is
   Featherless — the fast triage model and the deep security reviewer are
   two different models, so the expensive one is only ever called on a call
   that's already been judged high risk."*

**Say (over the flips):**
> "Everything you just watched in the terminal is the same data driving this
> dashboard in real time — same decisions, same audit trail."

---

## 1:50–2:00 — Close

**Show:** back to terminal or a static frame of the dashboard.

**Say:**
> "Deterministic checks run on every call and are free. A model is only
> consulted when they're genuinely uncertain, and it can only make a
> decision *stricter*, never looser. That's Vigil — a runtime firewall for
> autonomous agents. Thanks for watching."

Cut.

---

## Recording notes

- **Don't pause the terminal to explain** — let `run_demo.sh` run continuously
  in the background of the whole middle section; your voice is the pacing,
  not the scrollback.
- **If Featherless is rate-limited that day**, re-run `run_demo.sh` once
  before recording — scene 3 needs a real verdict on screen, not a retry.
- **If something errors on camera**, don't restart mid-recording — cut there,
  fix it, re-record from that scene only, splice later. A visible real bug
  handled calmly is fine; a long silent stall is not.
- Keep the mouse still when not clicking — nervous cursor movement reads as
  uncertainty on camera.
