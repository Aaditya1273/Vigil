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
// Featherless is the shipped product vendor (models: moonshotai/Kimi-K3,
// zai-org/GLM-5.2). Groq is not a product vendor — it is kept here, and only
// here, as a free-tier stand-in: proving this client against any real
// OpenAI-compatible endpoint is proof the identical code path works against
// Featherless once a key exists, since both speak the same wire format. It
// must never appear in the vendor table in chain.go.
//
// Skipped unless a key is present, so the default `go test ./...` stays
// offline and credential-free.
//
//	VIGIL_GROQ_API_KEY=... go test -run TestLiveVendors -v ./pkg/query-service/vigil/llm/
func TestLiveVendors(t *testing.T) {
	cfgs := []llm.Config{}
	for _, v := range []struct{ name, env, base, model string }{
		{"featherless", "VIGIL_FEATHERLESS_API_KEY", "https://api.featherless.ai/v1", "moonshotai/Kimi-K3"},
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
