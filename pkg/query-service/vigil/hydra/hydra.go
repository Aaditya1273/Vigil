// Package hydra is a Go-native client for the HydraDB managed graph+memory API.
//
// Built as a pure REST client, not a Python sidecar. HydraDB's own SDK docs
// (and its GitHub org) turned out to disagree with each other on method names
// across three different pages — the API itself, verified empirically with a
// real key, is the only source of truth this client is built against. See
// hydra_test.go for the request/response shapes that were actually observed:
// ingest is multipart/form-data with an app_knowledge or memories JSON field,
// each item's text lives at content.text (a flat "text" field is silently
// accepted but never indexed), ingestion is asynchronous (queued ->
// graph_creation -> completed), and query returns graph_context.chunk_relations
// as real extracted (source, relation, target) triplets — not vector hits
// dressed up as a graph.
//
// A Python sidecar would have added a second runtime, a second deployment
// surface, and an HTTP hop between Go and Python for zero benefit: HydraDB is
// plain REST with a bearer token, which Go's net/http already speaks.
package hydra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
)

// Named collections. Fixed, not configurable — the decision pipeline routes
// to a specific collection for a specific kind of question, so an operator
// renaming one would silently disconnect that traffic from its graph.
const (
	CollectionEnterprise = "enterprise"
	CollectionCodeGraph  = "code_graph"
	CollectionMemory     = "agent_memory"
	CollectionAudit      = "audit"
)

const (
	typeKnowledge = "knowledge"
	typeMemory    = "memory"
)

// Client talks to the HydraDB v2 API.
type Client struct {
	baseURL      string
	apiKey       string
	database     string
	http         *http.Client
	logger       *slog.Logger
	ingestTokens chan struct{}
}

// ingestRPS matches HydraDB's own stated ingestion limit ("ingestion rps rate
// limit exceeded (limit: 5)", observed live). Every memory/audit write the
// firewall makes is fire-and-forget from a goroutine (hydraLogMemory,
// hydraLogAudit in firewall/hydra.go), so without a limiter a burst of tool
// calls — a demo run, a real agent looping — throws all of them at HydraDB
// at once and most come back 429. Throttling here, once, is simpler and more
// honest than making every caller remember to.
const ingestRPS = 3

func newIngestLimiter() chan struct{} {
	tokens := make(chan struct{}, ingestRPS)
	for i := 0; i < ingestRPS; i++ {
		tokens <- struct{}{}
	}
	go func() {
		t := time.NewTicker(time.Second / ingestRPS)
		defer t.Stop()
		for range t.C {
			select {
			case tokens <- struct{}{}:
			default: // bucket full, drop the tick
			}
		}
	}()
	return tokens
}

// New builds a client against an explicit endpoint. Exported mainly so tests
// can point it at an httptest server; production code should use NewFromEnv.
func New(baseURL, apiKey, database string, logger *slog.Logger) *Client {
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		database:     database,
		http:         &http.Client{Timeout: 20 * time.Second},
		logger:       logger,
		ingestTokens: newIngestLimiter(),
	}
}

// NewFromEnv builds a client from VIGIL_HYDRADB_API_KEY / _DATABASE / _BASE_URL.
// Returns nil when no key is set — the caller treats a nil *Client as "no
// graph layer configured" and every method on a nil *Client is a safe no-op
// (see the nil-receiver guards below), so callers never need a separate
// Configured() check before using it.
func NewFromEnv(logger *slog.Logger) *Client {
	key := vigil.Env("HYDRADB_API_KEY")
	if key == "" {
		return nil
	}
	return New(
		vigil.EnvOr("HYDRADB_BASE_URL", "https://api.hydradb.com"),
		key,
		vigil.EnvOr("HYDRADB_DATABASE", "vigil-os"),
		logger,
	)
}

// Configured reports whether a client exists. Safe on a nil receiver.
func (c *Client) Configured() bool { return c != nil }

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("API-Version", "2")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(raw []byte, status int, out any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("hydra: unparseable response (http %d): %w", status, err)
	}
	if !env.Success || env.Error != nil {
		msg := "unknown error"
		if env.Error != nil {
			msg = env.Error.Code + ": " + env.Error.Message
		}
		return fmt.Errorf("hydra: %s (http %d)", msg, status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// EnsureDatabase creates the configured database if it does not already
// exist, then waits for it to report ready_for_ingestion. Called once, from
// a background goroutine at startup (mirrors llm.Chain's warm-up probe) —
// this is provisioning latency, not per-request latency, so it must never
// block the server from starting.
func (c *Client) EnsureDatabase(ctx context.Context) error {
	if c == nil {
		return nil
	}

	raw, status, err := c.do(ctx, http.MethodGet, "/databases", nil, "")
	if err != nil {
		return fmt.Errorf("hydra: list databases: %w", err)
	}
	var list struct {
		Databases []string `json:"databases"`
	}
	if err := decode(raw, status, &list); err != nil {
		return err
	}
	exists := false
	for _, d := range list.Databases {
		if d == c.database {
			exists = true
			break
		}
	}

	if !exists {
		body, _ := json.Marshal(map[string]string{"database": c.database})
		raw, status, err = c.do(ctx, http.MethodPost, "/databases", bytes.NewReader(body), "application/json")
		if err != nil {
			return fmt.Errorf("hydra: create database: %w", err)
		}
		if err := decode(raw, status, nil); err != nil {
			return err
		}
	}

	for attempt := 0; attempt < 30; attempt++ {
		raw, status, err = c.do(ctx, http.MethodGet, "/databases/status?database="+c.database, nil, "")
		if err == nil {
			var st struct {
				Infra struct {
					ReadyForIngestion bool `json:"ready_for_ingestion"`
				} `json:"infra"`
			}
			if decode(raw, status, &st) == nil && st.Infra.ReadyForIngestion {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("hydra: database %s did not become ready in time", c.database)
}

// IngestResult is one accepted source from a POST /context/ingest call.
type IngestResult struct {
	SourceID string
	Status   string // "queued" on success
}

func (c *Client) ingest(ctx context.Context, collection, sourceType, jsonField, jsonValue string) (IngestResult, error) {
	if c == nil {
		return IngestResult{}, fmt.Errorf("hydra: not configured")
	}

	select {
	case <-c.ingestTokens:
	case <-ctx.Done():
		return IngestResult{}, ctx.Err()
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("database", c.database)
	_ = w.WriteField("collection", collection)
	_ = w.WriteField("type", sourceType)
	_ = w.WriteField(jsonField, jsonValue)
	if err := w.Close(); err != nil {
		return IngestResult{}, err
	}

	raw, status, err := c.do(ctx, http.MethodPost, "/context/ingest", &buf, w.FormDataContentType())
	if err != nil {
		return IngestResult{}, fmt.Errorf("hydra: ingest: %w", err)
	}
	var data struct {
		Results []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := decode(raw, status, &data); err != nil {
		return IngestResult{}, err
	}
	if len(data.Results) == 0 {
		return IngestResult{}, fmt.Errorf("hydra: ingest accepted but returned no source")
	}
	r := data.Results[0]
	if r.Error != "" {
		return IngestResult{}, fmt.Errorf("hydra: ingest rejected: %s", r.Error)
	}
	return IngestResult{SourceID: r.ID, Status: r.Status}, nil
}

// IngestKnowledge stores a document into the given collection. Graph
// extraction (entities, relationships) runs asynchronously server-side —
// this call returns once the source is *queued*, not once it is indexed.
func (c *Client) IngestKnowledge(ctx context.Context, collection, sourceID, title, text string) (IngestResult, error) {
	item := []map[string]any{{
		"source_id": sourceID,
		"title":     title,
		"content":   map[string]string{"text": text},
	}}
	body, _ := json.Marshal(item)
	return c.ingest(ctx, collection, typeKnowledge, "app_knowledge", string(body))
}

// IngestMemory stores a conversational/behavioral fact into agent_memory.
func (c *Client) IngestMemory(ctx context.Context, collection, sourceID, text string) (IngestResult, error) {
	item := []map[string]any{{
		"source_id": sourceID,
		"text":      text,
	}}
	body, _ := json.Marshal(item)
	return c.ingest(ctx, collection, typeMemory, "memories", string(body))
}

// Entity is one graph node in an extracted relationship triplet.
type Entity struct {
	ID        string `json:"entity_id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
}

// Relation is the edge between two entities.
type Relation struct {
	Predicate string `json:"canonical_predicate"`
	Context   string `json:"context"`
}

// Triplet is one (source)-[relation]->(target) edge HydraDB extracted from
// ingested text — the actual graph traversal result, not a vector match.
type Triplet struct {
	Source   Entity   `json:"source"`
	Relation Relation `json:"relation"`
	Target   Entity   `json:"target"`
}

// ChunkRelation groups the triplets found in one relevant passage.
type ChunkRelation struct {
	Triplets        []Triplet `json:"triplets"`
	CombinedContext string    `json:"combined_context"`
}

// GraphContext is HydraDB's graph-native answer to a query: the entity paths
// and chunk relations behind the retrieved text, not just the text itself.
type GraphContext struct {
	ChunkRelations []ChunkRelation  `json:"chunk_relations"`
	QueryPaths     []map[string]any `json:"query_paths"`
}

// QueryResult is one graph-enriched answer.
type QueryResult struct {
	Query        string
	Collection   string
	Chunks       []map[string]any
	GraphContext GraphContext
	LatencyMS    float64
}

// EntityPaths flattens every (source -> relation -> target) triplet across
// every chunk relation into a single human-readable list, e.g.
// "policy no-pii-exfil --[applies to]--> customer". This is what the firewall
// logs alongside a decision so the audit record shows the graph reasoning,
// not just its conclusion.
func (r QueryResult) EntityPaths() []string {
	var out []string
	for _, cr := range r.GraphContext.ChunkRelations {
		for _, t := range cr.Triplets {
			out = append(out, fmt.Sprintf("%s --[%s]--> %s", t.Source.Name, t.Relation.Predicate, t.Target.Name))
		}
	}
	return out
}

// HasGraphSignal reports whether the query actually traversed any
// relationships, as opposed to returning bare text matches. The firewall
// uses this to decide whether HydraDB gave it something to reason about.
func (r QueryResult) HasGraphSignal() bool {
	for _, cr := range r.GraphContext.ChunkRelations {
		if len(cr.Triplets) > 0 {
			return true
		}
	}
	return false
}

// Query asks a natural-language question against one collection, in "fast"
// mode. queryType is "knowledge", "memory", or "all".
//
// "fast" over "thinking": measured live, thinking mode ran 2.5-5s per call
// (it runs a deeper LLM reasoning pass server-side); fast mode returned the
// same chunk_relations — the actual extracted graph — in 500-650ms, 5-8x
// faster, for every structural "what relationships exist" question this
// codebase asks. Thinking mode would only earn its cost on a question that
// needs synthesis across many relations, which none of these do.
func (c *Client) Query(ctx context.Context, collection, queryType, query string) (QueryResult, error) {
	return c.QueryMode(ctx, collection, queryType, "fast", query)
}

// QueryMode is Query with an explicit mode ("fast" or "thinking"), for the
// rare caller that actually wants the slower reasoning pass.
func (c *Client) QueryMode(ctx context.Context, collection, queryType, mode, query string) (QueryResult, error) {
	if c == nil {
		return QueryResult{}, fmt.Errorf("hydra: not configured")
	}
	start := time.Now()
	body, _ := json.Marshal(map[string]any{
		"database":      c.database,
		"collection":    collection,
		"query":         query,
		"type":          queryType,
		"mode":          mode,
		"graph_context": true,
	})
	raw, status, err := c.do(ctx, http.MethodPost, "/query", bytes.NewReader(body), "application/json")
	if err != nil {
		return QueryResult{}, fmt.Errorf("hydra: query: %w", err)
	}
	var data struct {
		Chunks       []map[string]any `json:"chunks"`
		GraphContext GraphContext     `json:"graph_context"`
	}
	if err := decode(raw, status, &data); err != nil {
		return QueryResult{}, err
	}
	return QueryResult{
		Query:        query,
		Collection:   collection,
		Chunks:       data.Chunks,
		GraphContext: data.GraphContext,
		LatencyMS:    float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

// BlastRadius is a package's real exposure: what depends on it, who shares
// its maintainers, and whether its name is a probable typosquat — the three
// questions a supply-chain incident response actually needs answered, each a
// real graph query, not derived from one another.
type BlastRadius struct {
	Package         string
	CompromisedAt   string
	DependentPaths  []string
	MaintainerPaths []string
	TyposquatPaths  []string
	QueryTimeMS     float64 // wall time for all three queries, sequential
}

// ExposedServices, SharedMaintainers, and Typosquats extract the left-hand
// entity name out of every "X --[relation containing needle]--> Y" path,
// deduplicated — the plain-English names an incident response actually
// needs, parsed from the client's own EntityPaths() output rather than a
// second source of truth.
func (b BlastRadius) ExposedServices() []string { return extractSubjects(b.DependentPaths, "depends") }
func (b BlastRadius) SharedMaintainers() []string {
	return extractSubjects(b.MaintainerPaths, "maintains")
}
func (b BlastRadius) Typosquats() []string { return extractSubjects(b.TyposquatPaths, "typosquat") }

func extractSubjects(paths []string, needle string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if !strings.Contains(p, needle) {
			continue
		}
		idx := strings.Index(p, " --[")
		if idx <= 0 {
			continue
		}
		name := p[:idx]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// GetBlastRadius answers "which services transitively depend on this
// package, and were any exposed during the compromise window" — the core
// supply-chain question. compromisedAt is free text (a timestamp, "the last
// 6 minutes", etc.) folded into the question; HydraDB reasons over whatever
// publish/consumption timestamps it extracted from ingested text, there is
// no separate time-range query parameter to set.
func (c *Client) GetBlastRadius(ctx context.Context, pkg, compromisedAt string) (QueryResult, error) {
	q := fmt.Sprintf(
		"Which services transitively depend on %s? Which of them resolved a version during the compromise window %s?",
		pkg, compromisedAt,
	)
	return c.Query(ctx, CollectionCodeGraph, "knowledge", q)
}

// GetMaintainerGraph answers "what else could this package's maintainer
// compromise" — an account takeover exposes every package that maintainer
// touches, not just the one that got attention first.
func (c *Client) GetMaintainerGraph(ctx context.Context, pkg string) (QueryResult, error) {
	q := "Which packages share a maintainer with " + pkg + "?"
	return c.Query(ctx, CollectionCodeGraph, "knowledge", q)
}

// GetTyposquats answers "what's impersonating this package" from whatever
// typosquat relationships have already been extracted into the graph (see
// scripts/ingest_npm.py, which computes real Levenshtein distance against a
// popular-package shortlist and ingests any close match as plain text for
// HydraDB to extract the relationship from).
func (c *Client) GetTyposquats(ctx context.Context, pkg string) (QueryResult, error) {
	q := "What packages are typosquats of " + pkg + "?"
	return c.Query(ctx, CollectionCodeGraph, "knowledge", q)
}

// GetFullBlastRadius runs all three queries and returns the combined,
// honestly-timed result the /blast-radius API and firewall block both use.
func (c *Client) GetFullBlastRadius(ctx context.Context, pkg, compromisedAt string) (BlastRadius, error) {
	start := time.Now()
	br := BlastRadius{Package: pkg, CompromisedAt: compromisedAt}

	dep, err := c.GetBlastRadius(ctx, pkg, compromisedAt)
	if err != nil {
		return br, fmt.Errorf("dependents: %w", err)
	}
	br.DependentPaths = dep.EntityPaths()

	maint, err := c.GetMaintainerGraph(ctx, pkg)
	if err != nil {
		return br, fmt.Errorf("maintainers: %w", err)
	}
	br.MaintainerPaths = maint.EntityPaths()

	typo, err := c.GetTyposquats(ctx, pkg)
	if err != nil {
		return br, fmt.Errorf("typosquats: %w", err)
	}
	br.TyposquatPaths = typo.EntityPaths()

	br.QueryTimeMS = float64(time.Since(start).Microseconds()) / 1000
	return br, nil
}

// SourceStatus is one ingested document's indexing progress.
type SourceStatus struct {
	ID             string
	IndexingStatus string // queued | graph_creation | completed | errored
	Error          string
}

// Status polls one ingested source's indexing progress.
func (c *Client) Status(ctx context.Context, collection, sourceID string) (SourceStatus, error) {
	if c == nil {
		return SourceStatus{}, fmt.Errorf("hydra: not configured")
	}
	path := fmt.Sprintf("/context/status?database=%s&collection=%s&id=%s", c.database, collection, sourceID)
	raw, status, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return SourceStatus{}, err
	}
	var data struct {
		Statuses []struct {
			ID             string `json:"id"`
			IndexingStatus string `json:"indexing_status"`
			ErrorMessage   string `json:"error_message"`
		} `json:"statuses"`
	}
	if err := decode(raw, status, &data); err != nil {
		return SourceStatus{}, err
	}
	if len(data.Statuses) == 0 {
		return SourceStatus{}, fmt.Errorf("hydra: no status for source %s", sourceID)
	}
	s := data.Statuses[0]
	return SourceStatus{ID: s.ID, IndexingStatus: s.IndexingStatus, Error: s.ErrorMessage}, nil
}

// WaitIndexed polls until a source finishes indexing or the context expires.
// Used by the seed script (synchronous, run once at setup) — never called on
// the request path, where ingest is fire-and-forget.
func (c *Client) WaitIndexed(ctx context.Context, collection, sourceID string) error {
	for {
		st, err := c.Status(ctx, collection, sourceID)
		if err != nil {
			return err
		}
		switch st.IndexingStatus {
		case "completed":
			return nil
		case "errored":
			return fmt.Errorf("hydra: source %s errored: %s", sourceID, st.Error)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// ListedSource is one document HydraDB has ingested.
type ListedSource struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// List returns every ingested source in a collection.
func (c *Client) List(ctx context.Context, collection string) ([]ListedSource, error) {
	if c == nil {
		return nil, fmt.Errorf("hydra: not configured")
	}
	body, _ := json.Marshal(map[string]string{"database": c.database, "collection": collection})
	raw, status, err := c.do(ctx, http.MethodPost, "/context/list", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, fmt.Errorf("hydra: list: %w", err)
	}
	var data struct {
		Sources []ListedSource `json:"sources"`
	}
	if err := decode(raw, status, &data); err != nil {
		return nil, err
	}
	return data.Sources, nil
}

// Delete removes one ingested source (and its extracted graph data) from a
// collection. Used by the seed script's --reset flag, and by operators who
// want to clear a demo/test database without deleting the whole database.
func (c *Client) Delete(ctx context.Context, collection, sourceID string) error {
	if c == nil {
		return fmt.Errorf("hydra: not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"database": c.database, "collection": collection, "ids": []string{sourceID},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/context", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("API-Version", "2")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hydra: delete: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return decode(raw, resp.StatusCode, nil)
}

// slug turns free text into a short, stable, URL/ID-safe source_id.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
