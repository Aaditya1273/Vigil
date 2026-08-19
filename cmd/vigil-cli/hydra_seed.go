package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/spf13/cobra"
)

// hydraSeedCmd populates the three demo collections with real example data,
// via real ingest calls against the real HydraDB API — the same client and
// the same code path the running firewall uses. Without this, the /ontology,
// /blast-radius, and /memory-timeline dashboard pages have nothing to show on
// a fresh database, and a demo audience sees three empty screens.
//
// Every document below is deliberately written as plain natural-language
// text, not hand-built graph structure: HydraDB's whole premise is that it
// extracts the entities and relationships itself. Seeding by writing Cypher
// or pre-built triplets would be demonstrating a different product.
func hydraSeedCmd() *cobra.Command {
	var wait, reset bool

	cmd := &cobra.Command{
		Use:   "hydra-seed",
		Short: "Seed HydraDB's enterprise, code_graph, and agent_memory collections with demo data",
		Long: `Ingests example documents into each collection so the graph pages have
real, non-empty data to show. Requires VIGIL_HYDRADB_API_KEY.

Uses the exact same hydra.Client the running firewall uses — this is
real graph extraction, not fixture data pre-shaped as a graph.

Every real decision the firewall makes also writes to agent_memory
(hydraLogMemory) and audit (hydraLogAudit). Across enough test runs that
traffic outnumbers and outranks the handful of seed documents in a query's
top results — real behavior for an operational memory graph, but noisy
right before a demo. --reset clears agent_memory and audit first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHydraSeed(wait, reset)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for each document to finish indexing before continuing (slower, useful right before a demo)")
	cmd.Flags().BoolVar(&reset, "reset", false, "delete existing agent_memory and audit documents first (does not touch enterprise or code_graph)")
	return cmd
}

type seedDoc struct {
	collection string
	sourceID   string
	title      string
	text       string
}

var seedDocs = []seedDoc{
	// --- enterprise: identity resolution, contradiction, trust, policy ------
	{hydra.CollectionEnterprise, "ent-identity-1", "identity-sam",
		"Sam is the same person as Soham Ratnaparkhi. This identity match has confidence 0.97 and is corroborated by two sources: Slack and Gmail."},
	{hydra.CollectionEnterprise, "ent-contradiction-1", "doc-contradiction",
		"Document Q3-Revenue-Report-Draft states Q3 revenue was $4.2M. Document Q3-Revenue-Report-Final states Q3 revenue was $3.8M. These two documents contradict each other on the same figure."},
	{hydra.CollectionEnterprise, "ent-trust-1", "source-trust-github",
		"Documents sourced from the github.com/vigil-platform organization have a trust score of 0.9, the highest trust tier, because they are code-reviewed before merge."},
	{hydra.CollectionEnterprise, "ent-trust-2", "source-trust-forum",
		"Documents sourced from an anonymous public forum have a trust score of 0.2, the lowest trust tier, because authorship and review cannot be verified."},
	{hydra.CollectionEnterprise, "ent-policy-1", "policy-no-pii-exfil",
		"Policy no-pii-exfil applies to entity type Customer. It denies any tool call that would export customer personal data outside the organization's network boundary."},
	{hydra.CollectionEnterprise, "ent-policy-2", "policy-read-only-ops",
		"Policy read-only-ops applies to entity type Codebase. It permits read_file, list_directory, search_code, and analyze_codebase for any agent operating under a code-review declared intent."},
	{hydra.CollectionEnterprise, "ent-policy-3", "policy-no-exec",
		"Policy no-exec applies to entity type Session. It denies run_command for any session that has not explicitly declared an operations or deployment intent."},

	// --- code_graph: dependencies, maintainers, typosquats ------------------
	{hydra.CollectionCodeGraph, "cg-1", "pkg-leftpad",
		"Package left-pad version 1.3.0 was published 2016-03-23 and has 2.6 million weekly downloads. It is maintained by the person azer. No other package depends on left-pad in this graph."},
	{hydra.CollectionCodeGraph, "cg-2", "pkg-express",
		"Package express version 4.19.2 depends on package body-parser with a semver range of ^1.20.2, and depends on package cookie with a semver range of ^0.6.0. Express is maintained by the person wesleytodd."},
	{hydra.CollectionCodeGraph, "cg-3", "pkg-body-parser",
		"Package body-parser version 1.20.2 depends on package bytes with a semver range of ^3.1.2. body-parser is maintained by the person wesleytodd, the same maintainer as express."},
	{hydra.CollectionCodeGraph, "cg-4", "pkg-vigil-service",
		"Service checkout-api transitively depends on package express version 4.19.2 and package body-parser version 1.20.2. checkout-api is a production service with 40,000 requests per minute."},
	{hydra.CollectionCodeGraph, "cg-5", "pkg-typosquat-1",
		"Package reqeusts is a typosquat of the popular package requests, with a Levenshtein edit distance of 1. Package reqeusts was published 4 days ago, has 12 total downloads, and has no other packages depending on it."},
	{hydra.CollectionCodeGraph, "cg-6", "pkg-typosquat-2",
		"Package cross-env-2 is a typosquat of the popular package cross-env, with a Levenshtein edit distance of 2. Package cross-env-2 was published 9 days ago by a maintainer with no other published packages."},
	{hydra.CollectionCodeGraph, "cg-7", "pkg-lodash",
		"Package lodash version 4.17.21 is maintained by the person jdalton and has 140 million weekly downloads. Service checkout-api transitively depends on lodash version 4.17.21."},

	// --- agent_memory: sessions, facts, supersession, behavior --------------
	{hydra.CollectionMemory, "mem-1", "session-4-fact",
		"In session 4, the agent identified the on-call engineer as Bruno. This fact was recorded as valid starting in session 4."},
	{hydra.CollectionMemory, "mem-2", "session-22-supersede",
		"In session 22, the agent identified the on-call engineer as Max, superseding the earlier fact from session 4 that named Bruno as on-call engineer."},
	{hydra.CollectionMemory, "mem-3", "behavior-normal-1",
		"Agent vigil-demo normally calls read_file between 3 and 8 times per session, followed by search_code 1 to 2 times. This is the observed baseline pattern across the last 20 sessions."},
	{hydra.CollectionMemory, "mem-4", "behavior-anomaly-1",
		"In one session, agent vigil-demo called read_file 19 times consecutively followed by network access 3 times, which is far outside its normal behavioral pattern of 3 to 8 read_file calls."},
	{hydra.CollectionMemory, "mem-5", "session-history-1",
		"Session demo-1 called read_file, then list_directory, then search_code, all permitted by declared intent, with no policy violations."},
}

func runHydraSeed(wait, reset bool) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := hydra.NewFromEnv(logger)
	if client == nil {
		return fmt.Errorf("VIGIL_HYDRADB_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fmt.Println("ensuring database is ready...")
	if err := client.EnsureDatabase(ctx); err != nil {
		return fmt.Errorf("database not ready: %w", err)
	}

	if reset {
		for _, collection := range []string{hydra.CollectionMemory, hydra.CollectionAudit} {
			lctx, lcancel := context.WithTimeout(context.Background(), 30*time.Second)
			sources, err := client.List(lctx, collection)
			lcancel()
			if err != nil {
				fmt.Printf("list %s failed: %v\n", collection, err)
				continue
			}
			fmt.Printf("deleting %d existing documents from %s...\n", len(sources), collection)
			for _, s := range sources {
				dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
				err := client.Delete(dctx, collection, s.ID)
				dcancel()
				if err != nil {
					fmt.Printf("  delete %s failed: %v\n", s.ID, err)
				}
			}
		}
	}

	for _, d := range seedDocs {
		ictx, icancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := client.IngestKnowledge(ictx, d.collection, d.sourceID, d.title, d.text)
		icancel()
		if err != nil {
			fmt.Printf("FAIL  %-14s %-24s %v\n", d.collection, d.title, err)
			continue
		}
		fmt.Printf("queued %-14s %-24s id=%s\n", d.collection, d.title, res.SourceID)

		if wait {
			wctx, wcancel := context.WithTimeout(context.Background(), 180*time.Second)
			err := client.WaitIndexed(wctx, d.collection, res.SourceID)
			wcancel()
			if err != nil {
				fmt.Printf("  indexing: %v\n", err)
			} else {
				fmt.Printf("  indexed\n")
			}
		}
	}

	fmt.Printf("\nseeded %d documents across %s, %s, %s.\n",
		len(seedDocs), hydra.CollectionEnterprise, hydra.CollectionCodeGraph, hydra.CollectionMemory)
	if !wait {
		fmt.Println("Graph extraction runs asynchronously — give it a minute or two before querying, or rerun with --wait.")
	}
	return nil
}
