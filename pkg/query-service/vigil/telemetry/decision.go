package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span is re-exported so callers in the decision path do not each have to
// import the OTel trace package for one type.
type Span = trace.Span

// StartDecision opens the span covering one governance decision.
//
// Unlike the older helpers in this package, which start and end a span
// internally, this returns the context and span so model calls nest underneath
// it. Without that the trace is a flat list and you cannot see which decision
// caused which inference.
func StartDecision(ctx context.Context, sessionID, tool string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "vigil.decision",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("vigil.session_id", sessionID),
			attribute.String("vigil.tool", tool),
			attribute.String("vigil.component", "firewall"),
		),
	)
}

// RecordDecision annotates the decision span with its outcome.
//
// A non-ALLOW decision sets the span status to Error so blocks surface as
// errors in SigNoz rather than being buried among successful calls.
func RecordDecision(span trace.Span, decision, stage, reason, ruleName string, riskScore int, modelUsed string) {
	if span == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("vigil.decision", decision),
		attribute.String("vigil.stage", stage),
		attribute.String("vigil.reason", reason),
		attribute.Bool("vigil.model_consulted", modelUsed != ""),
	}
	if ruleName != "" {
		attrs = append(attrs, attribute.String("vigil.rule", ruleName))
	}
	if modelUsed != "" {
		attrs = append(attrs, attribute.String("vigil.model", modelUsed))
	}
	// -1 means no model was consulted; recording it as 0 would be
	// indistinguishable from a model reporting no risk.
	if riskScore >= 0 {
		attrs = append(attrs, attribute.Int("vigil.risk_score", riskScore))
	}
	span.SetAttributes(attrs...)

	if decision != "ALLOW" {
		span.SetStatus(codes.Error, reason)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// StartModelCall opens a child span for one inference request.
func StartModelCall(ctx context.Context, role string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "vigil.model.call",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("vigil.model.role", role),
			attribute.String("vigil.component", "model-router"),
		),
	)
}

// ModelCall is the outcome of one inference request.
//
// Explicit primitives rather than an llm.Response: telemetry has no business
// importing the inference layer to read four numbers, and passing `any` would
// mean reflecting over it at runtime.
type ModelCall struct {
	ModelID          string
	RequestID        string
	Latency          time.Duration
	PromptTokens     int
	CompletionTokens int
}

// RecordModelCall annotates a model-call span. Pass a zero ModelCall when the
// call failed; err takes precedence.
func RecordModelCall(span trace.Span, mc ModelCall, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.Bool("vigil.model.failed", true))
		return
	}
	span.SetAttributes(
		attribute.String("vigil.model.id", mc.ModelID),
		attribute.Int64("vigil.model.latency_ms", mc.Latency.Milliseconds()),
		attribute.Int("vigil.model.prompt_tokens", mc.PromptTokens),
		attribute.Int("vigil.model.completion_tokens", mc.CompletionTokens),
	)
	if mc.RequestID != "" {
		span.SetAttributes(attribute.String("vigil.model.request_id", mc.RequestID))
	}
	span.SetStatus(codes.Ok, "")
}

// RecordForecast attaches a cost projection to a span.
func RecordForecast(span trace.Span, state string, burnRate, projected float64, ttbSeconds float64) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String("vigil.forecast.state", state),
		attribute.Float64("vigil.forecast.burn_rate_per_min", burnRate),
		attribute.Float64("vigil.forecast.projected_total", projected),
		attribute.Float64("vigil.forecast.time_to_breach_seconds", ttbSeconds),
	)
}

// RecordAuditAppend notes that a decision was sealed into the audit chain.
func RecordAuditAppend(span trace.Span, eventID, hash string) {
	if span == nil {
		return
	}
	span.AddEvent("vigil.audit.append", trace.WithAttributes(
		attribute.String("vigil.audit.event_id", eventID),
		attribute.String("vigil.audit.hash", hash),
	))
}
