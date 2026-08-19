package hydra_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
)

// TestLiveRoundTrip exercises the real HydraDB API: create/wait for the
// database, ingest a document, wait for graph extraction, then query it back
// and assert the response contains an actual extracted (source, relation,
// target) triplet — not just retrieved text. That distinction is the whole
// point of the product, so it's the one thing this test insists on.
//
// Skipped unless VIGIL_HYDRADB_API_KEY is set.
//
//	VIGIL_HYDRADB_API_KEY=... go test -run TestLiveRoundTrip -v ./pkg/query-service/vigil/hydra/
func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("VIGIL_HYDRADB_API_KEY") == "" {
		t.Skip("VIGIL_HYDRADB_API_KEY not set")
	}
	os.Setenv("HYDRADB_API_KEY", os.Getenv("VIGIL_HYDRADB_API_KEY"))
	if os.Getenv("VIGIL_HYDRADB_DATABASE") != "" {
		os.Setenv("HYDRADB_DATABASE", os.Getenv("VIGIL_HYDRADB_DATABASE"))
	}
	logger := slog.New(slog.DiscardHandler)
	client := hydra.NewFromEnv(logger)
	if client == nil {
		t.Fatal("expected a configured client")
	}

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer dbCancel()
	if err := client.EnsureDatabase(dbCtx); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}

	ingestCtx, ingestCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ingestCancel()
	res, err := client.IngestKnowledge(ingestCtx, hydra.CollectionEnterprise,
		"vigil-test-source", "vigil-live-test",
		"Policy no-live-test-exfil applies to entity type TestSubject.")
	if err != nil {
		t.Fatalf("IngestKnowledge: %v", err)
	}
	if res.Status != "queued" {
		t.Errorf("expected status queued, got %q", res.Status)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer waitCancel()
	if err := client.WaitIndexed(waitCtx, hydra.CollectionEnterprise, res.SourceID); err != nil {
		t.Fatalf("WaitIndexed: %v", err)
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer queryCancel()
	qr, err := client.Query(queryCtx, hydra.CollectionEnterprise, "knowledge",
		"What entity type does policy no-live-test-exfil apply to?")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	t.Logf("latency=%.0fms chunks=%d entity_paths=%v", qr.LatencyMS, len(qr.Chunks), qr.EntityPaths())

	if !qr.HasGraphSignal() {
		t.Error("expected at least one extracted (source, relation, target) triplet — got bare text retrieval only")
	}
}
