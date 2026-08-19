package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/mcp"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/gorilla/mux"
)

// mcpAdapter bridges firewall.Result onto what MCP can express.
//
// MCP's tools/call has two outcomes — a result or an error result — so PAUSE
// and BLOCK both surface as an error result. They differ in whether the session
// is latched closed: a PAUSE requires a human to release it via the existing
// session-approve endpoint, a BLOCK refuses this one call.
func (st *stack) mcpAdapter() (mcp.FirewallFn, mcp.CommitFn) {
	check := func(ctx context.Context, in mcp.FirewallInput) mcp.FirewallVerdict {
		res := st.fw.Check(ctx, firewall.Call{
			SessionID:   in.SessionID,
			Tool:        in.Tool,
			Args:        in.Args,
			ToolCost:    in.ToolCost,
			SessionCost: in.SessionCost,
			Budget:      in.Budget,
		})
		return mcp.FirewallVerdict{
			Allow:        res.Decision == firewall.Allow,
			BlockSession: res.Decision == firewall.Pause,
			Decision:     string(res.Decision),
			Reason:       res.Reason,
			Message:      res.Message,
		}
	}
	commit := func(sessionID, tool string, cost float64, dur time.Duration, ok bool) {
		st.fw.Commit(sessionID, tool, cost, dur, ok)
	}
	return check, commit
}

// registerRoutes mounts the Vigil 2.0 API.
func (st *stack) registerRoutes(api *mux.Router, mcpServer *mcp.MCPServer, budgetLimit float64) {
	// --- Governance rules ----------------------------------------------------
	// Derived from what is actually registered. The previous implementation
	// returned a hardcoded list advertising nine rules as enabled, which was
	// untrue: none of them ran, and three of them cannot run on MCP data at all.
	api.HandleFunc("/vigil/governance/rules", func(w http.ResponseWriter, r *http.Request) {
		names := st.gov.Plugins()
		rules := make([]map[string]any, 0, len(names))
		for _, n := range names {
			rules = append(rules, map[string]any{"name": n, "enabled": true, "source": "registered"})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"rules": rules,
			"count": len(rules),
			"note":  "Only detectors that can evaluate MCP tool-call data are registered. Token, prompt-repetition, and prompt-recursion detectors require LLM-turn spans, which tool-call interception does not carry.",
		})
	}).Methods("GET", "OPTIONS")

	// --- Session intent / policy ---------------------------------------------
	api.HandleFunc("/vigil/sessions/{id}/policy", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		budget := sessionBudget(mcpServer, id, budgetLimit)
		p := st.policies.GetOrDefault(id, budget)
		writeJSON(w, http.StatusOK, map[string]any{
			"policy":     p,
			"is_default": p.IsDefault(),
			"enforcing":  !p.IsDefault(),
		})
	}).Methods("GET", "OPTIONS")

	api.HandleFunc("/vigil/sessions/{id}/intent", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		var req policy.Policy
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid policy body")
			return
		}
		req.SessionID = id // server-assigned; never taken from the request
		req.Normalize()
		if err := req.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		st.policies.Set(&req)
		writeJSON(w, http.StatusOK, map[string]any{"status": "enforcing", "policy": st.policies.Get(id)})
	}).Methods("POST", "OPTIONS")

	// --- AI policy generator -------------------------------------------------
	api.HandleFunc("/vigil/policy/draft", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			writeErr(w, http.StatusBadRequest, "text is required")
			return
		}
		draft, err := policy.Generate(r.Context(), st.router, req.SessionID, req.Text)
		if err != nil {
			// 503 rather than 500: with no credentials configured this endpoint
			// is unavailable, not broken.
			writeErr(w, http.StatusServiceUnavailable, "policy generation unavailable: "+err.Error())
			return
		}
		st.policies.PutDraft(draft)
		// Explicitly inert. The client must call confirm.
		writeJSON(w, http.StatusOK, map[string]any{
			"draft":  draft,
			"active": false,
			"note":   "This draft is not enforced. POST /vigil/policy/draft/{id}/confirm to activate it.",
		})
	}).Methods("POST", "OPTIONS")

	api.HandleFunc("/vigil/policy/draft/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		p, err := st.policies.Confirm(mux.Vars(r)["id"])
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "enforcing", "policy": p})
	}).Methods("POST", "OPTIONS")

	api.HandleFunc("/vigil/policy/draft/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !st.policies.DiscardDraft(mux.Vars(r)["id"]) {
			writeErr(w, http.StatusNotFound, "draft not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "discarded"})
	}).Methods("DELETE", "OPTIONS")

	// --- Predictive cost -----------------------------------------------------
	api.HandleFunc("/vigil/sessions/{id}/forecast", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		writeJSON(w, http.StatusOK, st.fw.Forecast(id, sessionBudget(mcpServer, id, budgetLimit)))
	}).Methods("GET", "OPTIONS")

	// Fleet-wide forecast for the dashboard header, summing live sessions.
	api.HandleFunc("/vigil/forecast", func(w http.ResponseWriter, r *http.Request) {
		if id := r.URL.Query().Get("session"); id != "" {
			writeJSON(w, http.StatusOK, st.fw.Forecast(id, sessionBudget(mcpServer, id, budgetLimit)))
			return
		}
		// No session named: report the busiest one, which is what an operator
		// watching a single agent demo actually wants to see.
		var worst firewall.Snapshot
		for _, s := range mcpServer.Sessions() {
			snap := st.fw.Forecast(s.ID, s.BudgetLimit)
			if snap.ProjectedTotal > worst.ProjectedTotal {
				worst = snap
			}
		}
		writeJSON(w, http.StatusOK, worst)
	}).Methods("GET", "OPTIONS")

	// --- Live decision stream ------------------------------------------------
	api.HandleFunc("/vigil/decisions", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		decisions := st.fw.Recent(r.URL.Query().Get("session"), limit)
		writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions, "count": len(decisions)})
	}).Methods("GET", "OPTIONS")

	// --- Model router status -------------------------------------------------
	api.HandleFunc("/vigil/models", func(w http.ResponseWriter, r *http.Request) {
		// Never the key, and never a base URL that might carry credentials.
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":   st.router.Provider(),
			"configured": st.router.Available(),
			"roles":      st.router.ConfiguredRoles(),
			"models":     st.router.Stats(),
			"vendors":    st.router.Vendors(),
		})
	}).Methods("GET", "OPTIONS")

	// --- Audit ---------------------------------------------------------------
	api.HandleFunc("/vigil/audit/verify", func(w http.ResponseWriter, r *http.Request) {
		rep, err := audit.VerifyFile(vigil.EnvOr("AUDIT_PATH", audit.DefaultPath), r.URL.Query().Get("session"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}).Methods("GET", "OPTIONS")

	api.HandleFunc("/vigil/audit", func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		events, err := audit.Read(vigil.EnvOr("AUDIT_PATH", audit.DefaultPath), r.URL.Query().Get("session"), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events, "count": len(events)})
	}).Methods("GET", "OPTIONS")
}

// sessionBudget resolves a session's approved budget, falling back to the
// deployment ceiling for sessions the MCP server has not seen.
func sessionBudget(s *mcp.MCPServer, id string, fallback float64) float64 {
	if sess, ok := s.Session(id); ok && sess.BudgetLimit > 0 {
		return sess.BudgetLimit
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
