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

// Contexts returns the deduplicated source sentences behind every triplet —
// the actual extracted-from text, which is where a fact like a confidence
// score or corroborating source lives; the triplet itself only carries the
// canonical (source, predicate, target), not free-form detail.
func (r QueryResult) Contexts() []string {
	seen := map[string]bool{}
	var out []string
	for _, cr := range r.GraphContext.ChunkRelations {
		for _, t := range cr.Triplets {
			if c := t.Relation.Context; c != "" && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// ChunkTexts returns the raw source text of the retrieved chunks — a
// fallback for when a source document doesn't triplet-ize cleanly but its
// raw text still answers the question. Confirmed live: a resolver's
// FACT_UPDATE sentence (long, with parenthetical asides) ranked as the #1
// retrieved chunk for a directly matching query, but never appeared in
// GraphContext's extracted triplets/contexts for that same query — the
// retrieval layer found it, the triplet extractor didn't summarize it into
// the ranked context list. Contexts()/EntityPaths() alone would have
// silently dropped it.
//
// chunk_content's shape differs by ingest type, found live: a "knowledge"
// source (IngestKnowledge) comes back JSON-wrapped as
// {"content":{"text":"..."}, ...}, but a "memory" source (IngestMemory,
// what agent_memory holds) comes back as the raw text directly, no JSON at
// all — unmarshaling that as JSON silently fails and produces an empty
// string, which is why this first tries the wrapped shape and falls back
// to the raw string as-is.
func (r QueryResult) ChunkTexts() []string {
	var out []string
	for _, c := range r.Chunks {
		raw, ok := c["chunk_content"].(string)
		if !ok || raw == "" {
			continue
		}
		var doc struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(raw), &doc) == nil && doc.Content.Text != "" {
			out = append(out, doc.Content.Text)
		} else {
			out = append(out, raw)
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

// abstainStopwords are skipped when pulling keywords out of a question —
// common enough that their presence in retrieved content proves nothing
// about whether the actual subject was found.
var abstainStopwords = map[string]bool{
	"what": true, "when": true, "where": true, "which": true, "who": true,
	"does": true, "have": true, "this": true, "that": true, "with": true,
	"from": true, "about": true, "there": true, "been": true, "seen": true,
	"before": true, "current": true, "value": true, "history": true,
}

func significantWords(s string) []string {
	var out []string
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, ".,?!\"'();:")
		if len(w) >= 4 && !abstainStopwords[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}

// Abstains reports whether an answer to subject should be withheld as
// NOT_IN_HISTORY rather than risk presenting a hallucinated answer.
//
// Two signals were tried; only one holds up. The task's original design —
// "graph_context empty OR confidence < 0.7" — was tested directly against a
// completely fabricated subject ("Zorblaxian the space wizard from planet
// Neptune-9") that has never been ingested anywhere: HydraDB still returned
// 10 chunks scored 0.87-0.90 and 15 graph triplets, indistinguishable by
// score or emptiness from a real, answerable query (confirmed live against
// agent_memory). Both halves of the task's proposed check would have
// answered anyway. This is the same "relevancy_score is a rank-order
// signal, not an absolute gate" finding from Track 01's abstention
// self-check (scripts/ingest_enterprise.py), now confirmed on a second,
// independent collection.
//
// What actually discriminates: whether the subject's own keywords appear
// anywhere in what was retrieved. A fabricated subject's name never
// appears in real content, however confidently the ranker scored its best
// (irrelevant) guesses.
func (r QueryResult) Abstains(subject string) bool {
	if !r.HasGraphSignal() && len(r.Chunks) == 0 {
		return true
	}
	keywords := significantWords(subject)
	if len(keywords) == 0 {
		return false
	}
	var haystack strings.Builder
	for _, c := range r.Contexts() {
		haystack.WriteString(c)
		haystack.WriteByte(' ')
	}
	for _, p := range r.EntityPaths() {
		haystack.WriteString(p)
		haystack.WriteByte(' ')
	}
	if chunkJSON, err := json.Marshal(r.Chunks); err == nil {
		haystack.Write(chunkJSON)
	}
	lower := strings.ToLower(haystack.String())
	hits := 0
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			hits++
		}
	}
	// A single matching keyword is not enough for a multi-word subject —
	// found live: "bedroom wall paint color" (a genuinely unasked-about
	// subject) matched "paint" and "colors" purely by coincidence, against
	// an unrelated real session about flower-painting technique, and a
	// single-keyword gate called that a hit. Requiring a majority is what
	// actually distinguishes "this subject was discussed" from "one common
	// word in it happens to appear somewhere else."
	return hits*2 <= len(keywords)
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

// EntityProfile is what the graph knows about one named entity: its
// resolved aliases (with confidence and provenance — how the resolver
// decided two names were the same person, not just that it did), any
// documents that contradict each other about them, and the trust-scored
// sources those documents came from.
type EntityProfile struct {
	Name                  string
	AliasPaths            []string
	AliasContexts         []string
	PolicyPaths           []string
	PolicyContexts        []string
	ContradictionPaths    []string
	ContradictionContexts []string
	TrustPaths            []string
	TrustContexts         []string
	QueryTimeMS           float64
}

// Aliases returns the merged-alias names this entity resolved to, parsed
// from the real SAME_AS relationships HydraDB extracted from the resolver's
// own output (see scripts/ingest_enterprise.py). Matches both phrasings
// HydraDB's real extraction produces for an identity relationship — "also
// known as" is what it actually emits for alias-type input text; "same
// person" is kept as a fallback in case the extraction normalizes
// differently for other phrasings.
//
// Real bug found by querying the live graph: an alias-shaped question about
// one person retrieves every structurally-similar identity chunk in the
// collection, not just that person's — so a naive "grab every 'also known
// as' triplet in the result" pulled in other people's aliases too (e.g.
// asking about Sam also returned "hana --[also known as]--> hana s.").
// Each triplet's subject or object must actually name this entity before
// its counterpart is treated as one of its aliases.
func (e EntityProfile) Aliases() []string {
	lname := strings.ToLower(e.Name)
	seen := map[string]bool{}
	var out []string
	for _, needle := range []string{"also known as", "same person"} {
		for _, p := range e.AliasPaths {
			if !strings.Contains(p, needle) {
				continue
			}
			srcEnd := strings.Index(p, " --[")
			tgtStart := strings.Index(p, "--> ")
			if srcEnd <= 0 || tgtStart < 0 {
				continue
			}
			source, target := p[:srcEnd], p[tgtStart+4:]
			var alias string
			switch {
			case sameEntity(source, lname):
				alias = target
			case sameEntity(target, lname):
				alias = source
			default:
				continue // neither side is the entity we asked about
			}
			if !seen[alias] {
				seen[alias] = true
				out = append(out, alias)
			}
		}
	}
	return out
}

// sameEntity is a loose case-insensitive match between a graph node name and
// the entity name a caller asked about — either can be a substring of the
// other ("sam" vs "sam ratnaparkhi" style mismatches), since HydraDB
// extracts entity names as free-form text, not against a fixed vocabulary.
func sameEntity(node, lowerQueried string) bool {
	lnode := strings.ToLower(node)
	return strings.Contains(lnode, lowerQueried) || strings.Contains(lowerQueried, lnode)
}

// GetEntityProfile answers "who is this person" — their resolved aliases,
// any contradictory records about them, and the policy question this
// exists for: whether they resolve to a protected entity type (e.g.
// Customer) that a data-sharing policy denies.
func (c *Client) GetEntityProfile(ctx context.Context, name string) (EntityProfile, error) {
	start := time.Now()
	ep := EntityProfile{Name: name}

	// Split into two focused queries rather than one compound one. A real,
	// empirically observed retrieval failure: asking about aliases and
	// policy in the same question let the policy-shaped triplets outrank
	// the identity ones in HydraDB's ranked retrieval, so AliasPaths came
	// back with zero "also known as" edges even though they exist in the
	// graph — confirmed by asking the identical alias-only question in
	// isolation, which reliably surfaces them. Each question now asks for
	// exactly one kind of thing.
	aliases, err := c.Query(ctx, CollectionEnterprise, "knowledge",
		"Who is "+name+"? List every alias or name that is the same person as "+name+".")
	if err != nil {
		return ep, fmt.Errorf("aliases: %w", err)
	}
	ep.AliasPaths = aliases.EntityPaths()
	ep.AliasContexts = aliases.Contexts()

	// Same crowding problem, same fix: "what type is this person, and what
	// does policy say about that type" in one question let the (much more
	// numerous) generic policy-application documents outrank the one
	// triplet that actually names this person's type — confirmed live: a
	// person-only question reliably surfaces "jordan blake --[customer
	// of]--> northwind signal", the compound one never did. Two clean
	// questions, results merged into one field since checkEntityPolicy
	// scans PolicyPaths for both signals together.
	etype, err := c.Query(ctx, CollectionEnterprise, "knowledge",
		name+" — what entity type are they: Employee, Customer, or Vendor?")
	if err != nil {
		return ep, fmt.Errorf("entity type: %w", err)
	}
	polrule, err := c.Query(ctx, CollectionEnterprise, "knowledge",
		"What policy applies to Customer data, and does it deny sharing that data outside the organization?")
	if err != nil {
		return ep, fmt.Errorf("policy: %w", err)
	}
	ep.PolicyPaths = append(etype.EntityPaths(), polrule.EntityPaths()...)
	ep.PolicyContexts = append(etype.Contexts(), polrule.Contexts()...)

	contra, err := c.Query(ctx, CollectionEnterprise, "knowledge",
		"Are there any contradictory documents or records about "+name+"?")
	if err != nil {
		return ep, fmt.Errorf("contradictions: %w", err)
	}
	ep.ContradictionPaths = contra.EntityPaths()
	ep.ContradictionContexts = contra.Contexts()

	trust, err := c.Query(ctx, CollectionEnterprise, "knowledge",
		"What are the trust scores of the sources that mention "+name+"?")
	if err != nil {
		return ep, fmt.Errorf("trust: %w", err)
	}
	ep.TrustPaths = trust.EntityPaths()
	ep.TrustContexts = trust.Contexts()

	ep.QueryTimeMS = float64(time.Since(start).Microseconds()) / 1000
	return ep, nil
}

// GetContradictions lists contradictory document pairs across the whole
// enterprise graph, not scoped to one entity — the ontology page's
// "contradictions detected" view.
func (c *Client) GetContradictions(ctx context.Context) (QueryResult, error) {
	return c.Query(ctx, CollectionEnterprise, "knowledge", "List all documents that contradict each other and what they disagree about.")
}

// TemporalFact is what the agent_memory graph knows about one fact's value
// over time: what it is now, and the history behind that — when it changed,
// and (if a resolver emitted a FACT_UPDATE statement for it, see
// scripts/ingest_memory.py) what it was superseded from.
type TemporalFact struct {
	Subject         string
	CurrentPaths    []string
	CurrentContexts []string
	HistoryPaths    []string
	HistoryContexts []string
	// Abstain is true when the current-value query found nothing that
	// actually mentions Subject — see QueryResult.Abstains. A caller
	// should answer NOT_IN_HISTORY rather than present CurrentPaths/
	// CurrentContexts, which may be HydraDB's best (irrelevant) guess.
	Abstain     bool
	QueryTimeMS float64
}

// HasSignal reports whether the graph had anything at all to say about this
// subject — the first half of the abstention check: a query with zero graph
// signal has nothing to hallucinate an answer from.
func (t TemporalFact) HasSignal() bool {
	return len(t.CurrentPaths) > 0 || len(t.CurrentContexts) > 0
}

// GetTemporalFact answers "what is X now, and what was its history" as two
// separate questions, not one compound one — the same crowding fix as
// GetEntityProfile: a single "what is X and what's its history" question
// let history-shaped text outrank the current-value triplet in testing.
func (c *Client) GetTemporalFact(ctx context.Context, subject string) (TemporalFact, error) {
	start := time.Now()
	tf := TemporalFact{Subject: subject}

	cur, err := c.Query(ctx, CollectionMemory, "memory",
		"What is the current, most up-to-date value for: "+subject+"?")
	if err != nil {
		return tf, fmt.Errorf("current value: %w", err)
	}
	tf.CurrentPaths = cur.EntityPaths()
	tf.CurrentContexts = cur.Contexts()
	tf.Abstain = cur.Abstains(subject)

	hist, err := c.Query(ctx, CollectionMemory, "memory",
		"What is the history of "+subject+"? Was any earlier value superseded, and in which session and when?")
	if err != nil {
		return tf, fmt.Errorf("history: %w", err)
	}
	tf.HistoryPaths = hist.EntityPaths()
	tf.HistoryContexts = hist.Contexts()
	// Fall back to raw chunk text for anything the triplet extractor didn't
	// summarize into a graph context — see ChunkTexts.
	seen := map[string]bool{}
	for _, c := range tf.HistoryContexts {
		seen[c] = true
	}
	for _, c := range hist.ChunkTexts() {
		if !seen[c] {
			seen[c] = true
			tf.HistoryContexts = append(tf.HistoryContexts, c)
		}
	}

	tf.QueryTimeMS = float64(time.Since(start).Microseconds()) / 1000
	return tf, nil
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
