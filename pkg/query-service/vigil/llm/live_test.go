package llm_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
)

// TestLiveVendors exercises the OpenAI-compatible client against real
// endpoints.
//
// Featherless, NVIDIA, and Gemini are shipped product vendors (see vendors
// in chain.go). Groq is not — it is kept here, and only here, as a
// free-tier stand-in for testing the client itself without spending a
// shipped vendor's credit. It must never appear in the vendor table in
// chain.go.
//
// Skipped unless a key is present, so the default `go test ./...` stays
// offline and credential-free.
//
//	VIGIL_GROQ_API_KEY=... go test -run TestLiveVendors -v ./pkg/query-service/vigil/llm/
func TestLiveVendors(t *testing.T) {
	cfgs := []llm.Config{}
	for _, v := range []struct{ name, env, base, model string }{
		{"featherless", "VIGIL_FEATHERLESS_API_KEY", "https://api.featherless.ai/v1", "moonshotai/Kimi-K3"},
		{"nvidia", "VIGIL_NVIDIA_API_KEY", "https://integrate.api.nvidia.com/v1", "meta/llama-3.1-8b-instruct"},
		{"gemini", "VIGIL_GEMINI_API_KEY", "https://generativelanguage.googleapis.com/v1beta/openai", "gemini-3.5-flash-lite"},
		{"groq_test_standin", "VIGIL_GROQ_API_KEY", "https://api.groq.com/openai/v1", "openai/gpt-oss-20b"},
	} {
		key := os.Getenv(v.env)
		if key == "" {
			t.Logf("skipping %s: %s unset", v.name, v.env)
			continue
		}
		cfgs = append(cfgs, llm.Config{
			Name:    v.name,
			APIKey:  key,
			BaseURL: v.base,
			Models:  map[llm.Role]string{llm.RoleFast: v.model},
			Timeout: 20 * time.Second,
			Retries: 1,
		})
	}
	if len(cfgs) == 0 {
		t.Skip("no vendor credentials in the environment")
	}

	// Each vendor is checked on its own, so a chain success cannot hide a
	// vendor that is quietly never reached.
	for _, cfg := range cfgs {
		t.Run(cfg.Name, func(t *testing.T) {
			p, err := llm.NewOpenAICompatible(newTestLogger(t), cfg)
			if err != nil {
				t.Fatalf("construction failed: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := p.Complete(ctx, llm.Request{
				Role:   llm.RoleFast,
				System: "You are a JSON API. Reply with exactly {\"ok\":true} and nothing else.",
				User:   "ping",
				// 150, not 32: a reasoning model spends tokens on hidden
				// chain-of-thought before it ever emits the JSON body, so a tight
				// budget starves the response before it starts — this was found by
				// this exact test failing against a live reasoning-tier model.
				MaxTokens:   150,
				Temperature: 0,
				JSONOnly:    true,
			})
			if err != nil {
				t.Fatalf("live call failed: %v", err)
			}
			if resp.Text == "" {
				t.Error("empty completion")
			}
			// ModelID comes from the response body, not the request, so this
			// asserts the vendor told us what it actually ran.
			if resp.ModelID == "" {
				t.Error("no model ID in the response")
			}
			t.Logf("%s: model=%s latency=%s tokens=%d/%d text=%q",
				cfg.Name, resp.ModelID, resp.Latency.Round(time.Millisecond),
				resp.PromptTokens, resp.CompletionTokens, resp.Text)
		})
	}
}

// TestLiveChainFromEnv exercises the actual production code path —
// ChainFromEnv, the same constructor appserver wires up — rather than a
// hand-built Config, so this proves what the running server would actually
// do: read whichever vendors have a key in the environment, in vendor-table
// order, and serve a real completion from the first one that can. With no
// Featherless key configured but real NVIDIA and Gemini keys present, this
// is the live proof that the product keeps working without waiting on
// Featherless — the chain just runs NVIDIA → Gemini instead.
//
// Skipped unless at least one shipped vendor's key is present.
//
//	go test -run TestLiveChainFromEnv -v ./pkg/query-service/vigil/llm/
func TestLiveChainFromEnv(t *testing.T) {
	if os.Getenv("VIGIL_FEATHERLESS_API_KEY") == "" &&
		os.Getenv("VIGIL_NVIDIA_API_KEY") == "" &&
		os.Getenv("VIGIL_GEMINI_API_KEY") == "" {
		t.Skip("no shipped-vendor credentials in the environment")
	}

	chain := llm.ChainFromEnv(newTestLogger(t))
	if chain == nil {
		t.Fatal("ChainFromEnv returned nil despite a shipped-vendor key being present — check VIGIL_<VENDOR>_MODEL_FAST is also set")
	}
	t.Logf("chain order: %s", chain.Name())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := chain.Complete(ctx, llm.Request{
		Role:        llm.RoleFast,
		User:        "Reply with exactly one word: OK",
		MaxTokens:   50,
		Temperature: 0,
	})
	if err != nil {
		t.Fatalf("chain.Complete failed across every configured vendor: %v", err)
	}
	if resp.Text == "" {
		t.Error("empty completion from the chain")
	}
	t.Logf("served by model=%s latency=%s text=%q", resp.ModelID, resp.Latency.Round(time.Millisecond), resp.Text)
}
