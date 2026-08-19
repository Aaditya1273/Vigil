package replay

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
)

// TraceStore defines the interface for storing and retrieving trace contexts.
type TraceStore interface {
	GetTrace(ctx context.Context, traceID string) (*TraceContext, error)
	SaveTrace(ctx context.Context, trace *TraceContext) error
}

// MemoryTraceStore is an in-memory implementation of TraceStore.
type MemoryTraceStore struct {
	mu     sync.RWMutex
	traces map[string]*TraceContext
}

func NewMemoryTraceStore() *MemoryTraceStore {
	return &MemoryTraceStore{traces: make(map[string]*TraceContext)}
}

func (s *MemoryTraceStore) GetTrace(_ context.Context, traceID string) (*TraceContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trace, ok := s.traces[traceID]
	if !ok {
		return nil, nil
	}
	return trace, nil
}

func (s *MemoryTraceStore) SaveTrace(_ context.Context, trace *TraceContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[trace.TraceID] = trace
	return nil
}

// LLMClient sends a prompt to an LLM and returns the response.
type LLMClient interface {
	Complete(ctx context.Context, prompt string, model string) (string, error)
}

// chainClient runs replays through the same vendor chain as the firewall's
// judge.
//
// This used to be a second, worse HTTP client: OpenAI-only, its own
// VIGIL_LLM_API_KEY, a hardcoded gpt-4o-mini, and http.DefaultClient with no
// timeout — so a hung provider pinned a replay open forever. There is no reason
// for two inference paths in one binary, and the chain already has the retries,
// the failover, and the redaction.
type chainClient struct{ chain *llm.Chain }

// NewLLMClient returns a replay client backed by the configured vendor chain,
// or a noop when no vendor is configured.
func NewLLMClient() LLMClient {
	chain := llm.ChainFromEnv(slog.Default())
	if chain == nil {
		slog.Default().Warn("vigil replay: no inference vendor configured, using noop client")
		return &NoopLLMClient{}
	}
	slog.Default().Info("vigil replay: inference configured", slog.String("chain", chain.Name()))
	return &chainClient{chain: chain}
}

// Complete ignores the caller's model hint: the chain picks the model for the
// role, and which vendor serves is a failover decision the caller cannot make.
// Replay is an analysis task, so it uses the reasoner.
func (c *chainClient) Complete(ctx context.Context, prompt string, _ string) (string, error) {
	resp, err := c.chain.Complete(ctx, llm.Request{
		Role:      llm.RoleReasoner,
		User:      prompt,
		MaxTokens: 1024,
	})
	if err != nil {
		return "", fmt.Errorf("replay inference failed: %w", err)
	}
	return resp.Text, nil
}

// NoopLLMClient is the fallback when no vendor is configured.
//
// It returns a message saying so rather than a plausible-looking replay. A
// fabricated "what would have happened" is worse than no answer.
type NoopLLMClient struct{}

func (c *NoopLLMClient) Complete(_ context.Context, _ string, _ string) (string, error) {
	return "No inference vendor configured — set VIGIL_FEATHERLESS_API_KEY " +
		"(plus the matching model IDs) to enable real replay.", nil
}

// ReplayEngine handles trace reconstruction and prompt replay execution.
type ReplayEngine struct {
	logger    *slog.Logger
	store     TraceStore
	llmClient LLMClient
}

// NewReplayEngine initializes the engine with the given store and LLM client.
func NewReplayEngine(store TraceStore, llmClient LLMClient) *ReplayEngine {
	return &ReplayEngine{
		logger:    slog.Default(),
		store:     store,
		llmClient: llmClient,
	}
}

// ReconstructTrace retrieves the full execution context from the trace store.
func (e *ReplayEngine) ReconstructTrace(ctx context.Context, traceID string) (*TraceContext, error) {
	trace, err := e.store.GetTrace(ctx, traceID)
	if err != nil {
		return nil, err
	}
	if trace == nil {
		e.logger.WarnContext(ctx, "replay: trace not found", slog.String("trace_id", traceID))
		return nil, nil
	}
	return trace, nil
}

// Execute runs the new prompt through the configured LLM client and records the result.
func (e *ReplayEngine) Execute(ctx context.Context, req *ReplayRequest, original *TraceContext) *ReplayResult {
	if original == nil {
		return &ReplayResult{NewResponse: "Error: Original trace not found for replay."}
	}

	model := req.Model
	if model == "" {
		model = original.Model
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	prompt := req.NewPrompt
	if prompt == "" {
		prompt = original.OriginalPrompt
	}

	e.logger.InfoContext(ctx, "replay: executing prompt replay",
		slog.String("trace_id", req.TraceID),
		slog.String("model", model),
	)

	start := time.Now()
	newResponse, err := e.llmClient.Complete(ctx, prompt, model)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		e.logger.ErrorContext(ctx, "replay: LLM call failed", slog.String("error", err.Error()))
		return &ReplayResult{NewResponse: "Error: " + err.Error(), LatencyMs: latencyMs}
	}

	// cost estimate: ~$0.00003 per token (GPT-4o-mini pricing)
	estimatedCost := float64(len([]rune(prompt))+len([]rune(newResponse))) * 0.00003

	return &ReplayResult{
		NewResponse: newResponse,
		LatencyMs:   latencyMs,
		Cost:        estimatedCost,
	}
}
