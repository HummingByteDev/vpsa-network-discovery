package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/mockadvisor"
)

// TestProviderCatalogAcceptsSlugIdentifiers: a provider_id is opaque. VPS
// Advisor publishes its provider **slug**, because the website's own provider
// key is an autoincrement that is not stable across a restore — so the
// platform must store whatever arrives rather than assume a UUID.
//
// This is the second symptom of the same integration break: the builder aborts
// on `provider sync` before it reaches artifact publication, so a rejected
// catalog means nothing is ever uploaded for workers to download.
func TestProviderCatalogAcceptsSlugIdentifiers(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()

	fixtures := `{
	  "providers": [
	    {"provider_id": "examplehost", "name": "ExampleHost",
	     "asns": [64720], "monitoring_enabled": true, "priority": 10,
	     "updated_at": "2026-07-01T00:00:00Z"},
	    {"provider_id": "another-host-bv", "name": "Another Host BV",
	     "asns": [64721], "monitoring_enabled": true, "priority": 20,
	     "updated_at": "2026-07-01T00:00:00Z"}
	  ]
	}`
	f, err := mockadvisor.LoadFixtures([]byte(fixtures))
	if err != nil {
		t.Fatal(err)
	}
	adv := httptest.NewServer(mockadvisor.NewServer(f, "svc", discard()))
	defer adv.Close()

	client := advisor.New(adv.URL, "svc")
	asnToProvider, err := advisor.SyncProviders(ctx, e.pool, client)
	if err != nil {
		t.Fatalf("slug provider ids rejected: %v", err)
	}
	if got := asnToProvider[64720]; got != "examplehost" {
		t.Fatalf("ASN 64720 → %q, want examplehost", got)
	}
	var stored string
	if err := e.pool.QueryRow(ctx, `select provider_id from routing.provider
		where name = 'ExampleHost'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "examplehost" {
		t.Fatalf("stored provider_id = %q, want the slug verbatim", stored)
	}

	// Delisting compares the same opaque values: dropping one provider from
	// the catalog must delist exactly that one.
	f.Providers = f.Providers[:1]
	if _, err := advisor.SyncProviders(ctx, e.pool, client); err != nil {
		t.Fatal(err)
	}
	// Scoped to this test's two providers: the routing schema is shared across
	// the package's tests and is not truncated between them.
	var delisted, live int
	if err := e.pool.QueryRow(ctx, `select
		count(*) filter (where delisted_at is not null),
		count(*) filter (where delisted_at is null)
		from routing.provider where provider_id in ('examplehost','another-host-bv')`).
		Scan(&delisted, &live); err != nil {
		t.Fatal(err)
	}
	if delisted != 1 || live != 1 {
		t.Fatalf("delisted=%d live=%d, want exactly the dropped provider delisted", delisted, live)
	}
}

// TestAdvisorSyncFlow: an enrollment created on VPS Advisor is provisioned
// locally and its token redeems against the coordinator; a suspend decision
// from the advisor dashboard is applied. VPS Advisor stays the source of
// truth end to end.
func TestAdvisorSyncFlow(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()

	const token = "one-time-token-from-advisor"
	tokenHash := sha256.Sum256([]byte(token))
	workerID := "99999999-9999-9999-9999-999999999999"
	fixtures := fmt.Sprintf(`{
	  "providers": [
	    {"provider_id": "11111111-1111-1111-1111-111111111111", "name": "SyncHost",
	     "asns": [64700], "monitoring_enabled": true, "priority": 10,
	     "updated_at": "2026-07-01T00:00:00Z"}
	  ],
	  "enrollments": [
	    {"enrollment_id": "en-1", "worker_id": %q, "worker_name": "advisor-worker",
	     "operator_id": "44444444-4444-4444-4444-444444444444",
	     "token_hash": %q, "expires_at": %q}
	  ],
	  "decisions": [
	    {"decision_id": "d-1", "worker_id": %q, "state": "suspended",
	     "reason": "advisor admin says so", "decided_at": %q}
	  ]
	}`, workerID, hex.EncodeToString(tokenHash[:]),
		time.Now().Add(time.Hour).Format(time.RFC3339),
		workerID, time.Now().Format(time.RFC3339))

	f, err := mockadvisor.LoadFixtures([]byte(fixtures))
	if err != nil {
		t.Fatal(err)
	}
	adv := httptest.NewServer(mockadvisor.NewServer(f, "svc", discard()))
	defer adv.Close()

	// First pass: enrollments only (the decision is skipped gracefully until
	// the worker exists — it does after ingest, same pass applies it).
	srv := New(Config{AdminToken: adminToken,
		AdvisorClient: advisor.New(adv.URL, "svc")}, e.reg, nil, discard())
	srv.SyncAdvisor(ctx, time.Now().Add(-time.Hour))

	// Provider cache synced.
	var providers int
	if err := e.pool.QueryRow(ctx, `select count(*) from routing.provider
		where name = 'SyncHost' and delisted_at is null`).Scan(&providers); err != nil {
		t.Fatal(err)
	}
	if providers != 1 {
		t.Fatal("provider catalog not synced")
	}
	// Worker provisioned with the advisor's ID and the decision applied.
	var state string
	if err := e.pool.QueryRow(ctx, `select state from registry.worker
		where id = $1`, workerID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "suspended" {
		t.Fatalf("worker state = %s, want suspended (decision applied)", state)
	}
	// Token hash landed and is redeemable (state gate aside).
	var tokens int
	if err := e.pool.QueryRow(ctx, `select count(*) from registry.enrollment_token
		where worker_id = $1 and used_at is null`, workerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 1 {
		t.Fatal("enrollment token not provisioned")
	}
	// Second pass is idempotent.
	srv.SyncAdvisor(ctx, time.Now().Add(-time.Hour))
	var workers int
	if err := e.pool.QueryRow(ctx, `select count(*) from registry.worker
		where id = $1`, workerID).Scan(&workers); err != nil {
		t.Fatal(err)
	}
	if workers != 1 {
		t.Fatalf("idempotency broken: %d workers", workers)
	}
}
