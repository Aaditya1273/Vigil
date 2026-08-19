package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
)

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

// okBody is a minimal OpenAI-compatible success payload.
func okBody(model, content string) string {
	b, _ := json.Marshal(map[string]any{
		"model":   model,
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	return string(b)
}

// fakeProvider stands up an OpenAI-compatible endpoint and counts requests.
// This is what lets the whole client be exercised with no real credential.
func fakeProvider(t *testing.T, handler http.HandlerFunc) (base string, calls *atomic.Int32) {
	t.Helper()
	calls = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Expected /chat/completions, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("Expected a Bearer authorization header, got %q", got)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func cfg(base string, models map[llm.Role]string) llm.Config {
	return llm.Config{
		APIKey:  "test-key",
		BaseURL: base,
		Models:  models,
		Timeout: 2 * time.Second,
		Retries: 2,
	}
}

func TestCompleteSuccess(t *testing.T) {
	base, calls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("served/model-x", `{"ok":true}`)))
	})
	f, err := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{llm.RoleFast: "requested/model-a"}))
	if err != nil {
		t.Fatalf("NewOpenAICompatible failed: %v", err)
	}

	resp, err := f.Complete(context.Background(), llm.Request{Role: llm.RoleFast, User: "hi"})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Expected 1 request, got %d", calls.Load())
	}
	// The model that served must be reported, not the one requested — after a
	// fallback these differ and the audit record must name the real one.
	if resp.ModelID != "served/model-x" {
		t.Fatalf("Expected the served model id, got %q", resp.ModelID)
	}
	if resp.PromptTokens != 11 || resp.CompletionTokens != 7 {
		t.Fatalf("Expected token usage to be captured, got %d/%d", resp.PromptTokens, resp.CompletionTokens)
	}
	if resp.Latency <= 0 {
		t.Fatal("Expected latency to be measured")
	}
}

func TestRetriesOn503(t *testing.T) {
	base, calls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{llm.RoleFast: "m"}))

	if _, err := f.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err == nil {
		t.Fatal("Expected an error after exhausting retries")
	}
	// 1 initial attempt + 2 retries.
	if calls.Load() != 3 {
		t.Fatalf("Expected 3 attempts on a 5xx, got %d", calls.Load())
	}
}

func TestRetriesOn429(t *testing.T) {
	base, calls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{llm.RoleFast: "m"}))
	f.Complete(context.Background(), llm.Request{Role: llm.RoleFast})

	if calls.Load() != 3 {
		t.Fatalf("Expected rate limiting to be retried, got %d attempts", calls.Load())
	}
}

// TestDoesNotRetryOn401: a bad key will not become a good key. Retrying it just
// produces three identical log lines.
func TestDoesNotRetryOn401(t *testing.T) {
	base, calls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{llm.RoleFast: "m"}))

	if _, err := f.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err == nil {
		t.Fatal("Expected a 401 to surface as an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("Expected exactly 1 attempt on a 401, got %d", calls.Load())
	}
}

func TestTimeout(t *testing.T) {
	base, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(okBody("m", "late")))
	})
	c := cfg(base, map[llm.Role]string{llm.RoleFast: "m"})
	c.Timeout = 50 * time.Millisecond
	c.Retries = 0
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), c)

	if _, err := f.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err == nil {
		t.Fatal("Expected a timeout to surface as an error, not a hang or a fabricated response")
	}
}

// TestFallsBackDownwardOnly: a reviewer outage may degrade to the reasoner, but
// a cheap-model failure must never escalate to an expensive one.
func TestFallsBackDownwardOnly(t *testing.T) {
	base, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "expensive" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(okBody(body.Model, "ok")))
	})
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{
		llm.RoleFast:     "cheap",
		llm.RoleReasoner: "mid",
		llm.RoleReviewer: "expensive",
	}))

	resp, err := f.Complete(context.Background(), llm.Request{Role: llm.RoleReviewer})
	if err != nil {
		t.Fatalf("Expected a fallback to succeed, got %v", err)
	}
	if resp.ModelID != "mid" {
		t.Fatalf("Expected fallback to the reasoner's model, got %q", resp.ModelID)
	}

	// Now break the cheap model: there is nothing below it, so this must fail
	// rather than climb to "mid" or "expensive".
	base2, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	f2, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base2, map[llm.Role]string{
		llm.RoleFast:     "cheap",
		llm.RoleReasoner: "mid",
	}))
	if _, err := f2.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err == nil {
		t.Fatal("Expected a fast-model failure to fail outright, not escalate upward")
	}
}

func TestRequiresCredentialAndModels(t *testing.T) {
	if _, err := llm.NewOpenAICompatible(newTestLogger(t), llm.Config{Models: map[llm.Role]string{llm.RoleFast: "m"}}); err == nil {
		t.Fatal("Expected construction to fail without an API key")
	}
	if _, err := llm.NewOpenAICompatible(newTestLogger(t), llm.Config{APIKey: "k"}); err == nil {
		t.Fatal("Expected construction to fail with no model IDs configured")
	}
}

// TestConfigRedactsKey: an accidental %v of the config must not print the key.
func TestConfigRedactsKey(t *testing.T) {
	c := llm.Config{APIKey: "sk-super-secret-value", BaseURL: "https://x"}
	if s := fmt.Sprintf("%v", c); strings.Contains(s, "sk-super-secret-value") {
		t.Fatalf("Config stringification leaked the API key: %s", s)
	}
}

// TestDeterministicProviderRefusesRatherThanInvents is the honesty invariant:
// with no credential the provider errors, it does not synthesize a verdict.
func TestDeterministicProviderRefusesRatherThanInvents(t *testing.T) {
	var p llm.Provider = llm.DeterministicProvider{}
	resp, err := p.Complete(context.Background(), llm.Request{Role: llm.RoleFast})
	if err == nil {
		t.Fatal("Expected ErrNoModel; a fabricated verdict would poison the audit chain")
	}
	if resp != nil {
		t.Fatal("Expected no response from the deterministic provider")
	}
	if p.Configured(llm.RoleFast) {
		t.Fatal("Expected the deterministic provider to report itself unconfigured")
	}
}

// TestNormalTierMakesNoModelCall pins the performance and cost invariant
// structurally: the happy path has no role to consult.
func TestNormalTierMakesNoModelCall(t *testing.T) {
	r := llm.NewRouter(newTestLogger(t), llm.DeterministicProvider{})
	if roles := r.RolesFor(llm.TierNormal); len(roles) != 0 {
		t.Fatalf("Expected no model roles for a normal-tier call, got %v", roles)
	}
	if got := len(r.RolesFor(llm.TierSuspicious)); got != 1 {
		t.Fatalf("Expected 1 role for suspicious, got %d", got)
	}
	if got := len(r.RolesFor(llm.TierHighRisk)); got != 2 {
		t.Fatalf("Expected reasoner+reviewer for high risk, got %d", got)
	}
}

func TestRouterUnavailableWithoutCredentials(t *testing.T) {
	r := llm.NewRouter(newTestLogger(t), llm.DeterministicProvider{})
	if r.Available() {
		t.Fatal("Expected the router to report unavailable with no configured models")
	}
	if r.Provider() != "deterministic" {
		t.Fatalf("Expected provider name 'deterministic', got %q", r.Provider())
	}
	if len(r.ConfiguredRoles()) != 0 {
		t.Fatal("Expected no configured roles")
	}
}

func TestRouterRecordsFallbackStats(t *testing.T) {
	base, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "expensive" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(okBody(body.Model, "ok")))
	})
	f, _ := llm.NewOpenAICompatible(newTestLogger(t), cfg(base, map[llm.Role]string{
		llm.RoleFast:     "cheap",
		llm.RoleReasoner: "mid",
		llm.RoleReviewer: "expensive",
	}))
	r := llm.NewRouter(newTestLogger(t), f)

	if _, err := r.Complete(context.Background(), llm.RoleReviewer, llm.Request{User: "x"}); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	stats := r.Stats()
	if len(stats) != 1 {
		t.Fatalf("Expected 1 model row, got %d", len(stats))
	}
	if stats[0].Fallbacks != 1 {
		t.Fatalf("Expected the fallback to be counted, got %d", stats[0].Fallbacks)
	}
	if stats[0].TotalTokens != 18 {
		t.Fatalf("Expected 18 total tokens, got %d", stats[0].TotalTokens)
	}
}
