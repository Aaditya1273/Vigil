package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Config describes how to reach one OpenAI-compatible inference vendor.
//
// Featherless, NVIDIA, and Gemini all ship real support in the chain (see
// vendors in chain.go); the client is generic — any endpoint speaking the
// same /chat/completions contract works — which is also what makes it
// possible to test this exact code path against a free-tier stand-in (see
// llm_test.go, live_test.go) without spending a shipped vendor's credit.
type Config struct {
	// Name identifies the vendor in logs, telemetry, and the dashboard. It is
	// also what appears in the audit record, so it must be the vendor that
	// actually served the request.
	Name    string
	APIKey  string
	BaseURL string
	// Models maps each role to a model ID. There are deliberately no defaults:
	// model catalogues change, and a hardcoded ID that has since been retired
	// fails at runtime in production rather than at startup in review. An
	// operator who wants a role active must name the model.
	Models  map[Role]string
	Timeout time.Duration
	Retries int
}

// String redacts the key so an accidental %v or structured log of the whole
// config cannot leak it.
func (c Config) String() string {
	return fmt.Sprintf("llm.Config{BaseURL:%s, Models:%v, Timeout:%s, Retries:%d, APIKey:<redacted len=%d>}",
		c.BaseURL, c.Models, c.Timeout, c.Retries, len(c.APIKey))
}

// OpenAICompatible is a chat-completions client for any vendor speaking the
// OpenAI wire format.
type OpenAICompatible struct {
	cfg    Config
	logger *slog.Logger
	http   *http.Client
}

// NewOpenAICompatible builds a client, or reports why it cannot.
//
// Missing credentials are an ordinary condition, not a failure of the
// deployment: the caller logs it and moves to the next vendor in the chain.
func NewOpenAICompatible(logger *slog.Logger, cfg Config) (*OpenAICompatible, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("llm: no API key configured for %s", cfg.Name)
	}
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("llm: no model IDs configured for %s", cfg.Name)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("llm: no base URL configured for %s", cfg.Name)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	return &OpenAICompatible{
		cfg:    cfg,
		logger: logger,
		// A dedicated client, never http.DefaultClient: DefaultClient has no
		// timeout, so a hung provider would pin a tool call open indefinitely.
		http: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

func (f *OpenAICompatible) Name() string { return f.cfg.Name }

func (f *OpenAICompatible) Configured(role Role) bool { return f.cfg.Models[role] != "" }

// ConfiguredRoles lists the roles that have a model, for the status endpoint.
func (f *OpenAICompatible) ConfiguredRoles() []Role {
	out := make([]Role, 0, len(Roles))
	for _, r := range Roles {
		if f.Configured(r) {
			out = append(out, r)
		}
	}
	return out
}

// fallbackRole returns the next cheaper role to try, and whether one exists.
//
// Escalation is downward only. A reviewer outage falling back to the reasoner
// is a graceful degradation; the reverse would let a cheap model's transient
// failure silently invoke the most expensive one, turning an outage into a
// bill.
func fallbackRole(r Role) (Role, bool) {
	switch r {
	case RoleReviewer:
		return RoleReasoner, true
	case RoleReasoner:
		return RoleFast, true
	default:
		return "", false
	}
}

// Complete runs a request, retrying transient failures and falling back to a
// cheaper role's model if the requested one stays unavailable.
func (f *OpenAICompatible) Complete(ctx context.Context, req Request) (*Response, error) {
	role := req.Role
	for {
		model := f.cfg.Models[role]
		if model == "" {
			next, ok := fallbackRole(role)
			if !ok {
				return nil, ErrNoModel
			}
			role = next
			continue
		}

		resp, err := f.complete(ctx, req, role, model)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		next, ok := fallbackRole(role)
		if !ok {
			return nil, err
		}
		f.logger.WarnContext(ctx, "llm: falling back to cheaper model role",
			slog.String("from_role", string(role)),
			slog.String("to_role", string(next)),
			slog.String("error", err.Error()),
		)
		role = next
	}
}

// complete performs the request against one model, with bounded retries.
func (f *OpenAICompatible) complete(ctx context.Context, req Request, role Role, model string) (*Response, error) {
	const (
		baseDelay = 250 * time.Millisecond
		maxDelay  = 2 * time.Second
	)

	var lastErr error
	for attempt := 0; attempt <= f.cfg.Retries; attempt++ {
		if attempt > 0 {
			delay := baseDelay << (attempt - 1)
			if delay > maxDelay {
				delay = maxDelay
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		resp, retryable, err := f.attempt(ctx, req, role, model)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable {
			// 4xx other than 429 will not become correct on a retry; a 401
			// retried three times is three log lines saying the key is wrong.
			return nil, err
		}
	}
	return nil, lastErr
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// attempt is one HTTP round trip. The bool reports whether a retry could help.
func (f *OpenAICompatible) attempt(ctx context.Context, req Request, role Role, model string) (*Response, bool, error) {
	body := chatRequest{
		Model:       model,
		Messages:    []chatMessage{},
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if req.System != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: req.System})
	}
	body.Messages = append(body.Messages, chatMessage{Role: "user", Content: req.User})
	if req.JSONOnly {
		body.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}

	attemptCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, f.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+f.cfg.APIKey)

	start := time.Now()
	httpResp, err := f.http.Do(httpReq)
	if err != nil {
		return nil, true, err // network errors are worth one more try
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, true, err
	}
	latency := time.Since(start)

	if httpResp.StatusCode != http.StatusOK {
		// Quota exhaustion and auth failure are not transient: retrying burns
		// time on a vendor that will keep saying no. They are reported as
		// ErrExhausted so the chain moves to the next vendor immediately,
		// which is the whole point of having one.
		switch {
		case httpResp.StatusCode == http.StatusPaymentRequired,
			httpResp.StatusCode == http.StatusUnauthorized,
			httpResp.StatusCode == http.StatusForbidden:
			return nil, false, fmt.Errorf("%w: %s returned %d", ErrExhausted, f.cfg.Name, httpResp.StatusCode)
		case httpResp.StatusCode == http.StatusTooManyRequests && isQuotaExhausted(raw):
			// A 429 is normally backpressure, but these vendors also use it for
			// "you are out of credits". Only the latter should fail over.
			return nil, false, fmt.Errorf("%w: %s is out of quota", ErrExhausted, f.cfg.Name)
		}
		retryable := httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode >= 500
		// Never include the response body verbatim in the error: provider error
		// payloads sometimes echo request headers.
		return nil, retryable, fmt.Errorf("llm: %s returned %d", f.cfg.Name, httpResp.StatusCode)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, false, fmt.Errorf("llm: unparseable provider response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, false, errors.New("llm: provider returned no choices")
	}

	served := parsed.Model
	if served == "" {
		served = model
	}
	return &Response{
		Text:             parsed.Choices[0].Message.Content,
		ModelID:          served,
		RequestID:        httpResp.Header.Get("x-request-id"),
		Latency:          latency,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		Role:             role,
	}, false, nil
}
