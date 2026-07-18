package builder

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/packet/mrt"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/mockadvisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
)

// End-to-end pipeline test against a real database. Gated on VAPN_TEST_DB_DSN
// (CI sets it; locally: postgres://vapn:vapn-dev@localhost:5433/vapn).
// The test uses its own schemas-per-run? No — it truncates the routing schema,
// so never point it at a database whose routing data you care about.

const fixtureJSON = `{
  "providers": [
    {"provider_id": "11111111-1111-1111-1111-111111111111", "name": "TestHost A",
     "asns": [64500], "monitoring_enabled": true, "priority": 10,
     "updated_at": "2026-07-01T00:00:00Z"},
    {"provider_id": "22222222-2222-2222-2222-222222222222", "name": "TestHost B",
     "asns": [64501], "monitoring_enabled": true, "priority": 20,
     "updated_at": "2026-07-01T00:00:00Z"}
  ]
}`

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("VAPN_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("VAPN_TEST_DB_DSN not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.Apply(context.Background(), pool, migrationsDir(t), log); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(),
		`truncate routing.probe_target, routing.prefix, routing.snapshot,
		         routing.asn, routing.provider cascade`)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeBview(t *testing.T) string {
	t.Helper()
	var out []byte
	add := func(subtype mrt.MRTSubTypeTableDumpv2, body mrt.Body) {
		msg, err := mrt.NewMRTMessage(1752800000, mrt.TABLE_DUMPv2, subtype, body)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := msg.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw...)
	}
	entry := func(peer uint16, path ...uint32) *mrt.RibEntry {
		attrs := []bgp.PathAttributeInterface{
			bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
				bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, path),
			}),
		}
		return mrt.NewRibEntry(peer, 1752800000, 0, attrs, false)
	}

	add(mrt.PEER_INDEX_TABLE, mrt.NewPeerIndexTable("10.0.0.0", "test",
		[]*mrt.Peer{mrt.NewPeer("10.0.0.1", "10.0.0.1", 65001, true)}))
	// Provider A: clean /16, a MOAS /24, and a bogon that must be dropped.
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(1, bgp.NewIPAddrPrefix(16, "185.0.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500)}))
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(2, bgp.NewIPAddrPrefix(24, "185.1.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500), entry(0, 65001, 64999)}))
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(3, bgp.NewIPAddrPrefix(16, "10.5.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500)}))
	// Provider B: one v4, one v6, and a too-long /25 (flagged, no target).
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(4, bgp.NewIPAddrPrefix(22, "185.2.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64501)}))
	add(mrt.RIB_IPV6_UNICAST, mrt.NewRib(5, bgp.NewIPv6AddrPrefix(32, "2001:41d0::"),
		[]*mrt.RibEntry{entry(0, 65001, 64501)}))
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(6, bgp.NewIPAddrPrefix(25, "185.3.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64501)}))
	// Unmonitored provider prefix: ignored entirely.
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(7, bgp.NewIPAddrPrefix(24, "8.8.8.0"),
		[]*mrt.RibEntry{entry(0, 65001, 15169)}))
	// More-specific overlapping Provider A's /16: shares the first usable
	// address 185.0.0.1 — target derivation must deduplicate (regression:
	// real bview data violated the (snapshot_id, address) constraint).
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(8, bgp.NewIPAddrPrefix(17, "185.0.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500)}))

	path := filepath.Join(t.TempDir(), "bview.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write(out); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type testEnv struct {
	b         *Builder
	pub       *artifact.Publisher
	storeRoot string
	pubKey    ed25519.PublicKey
}

func newBuilder(t *testing.T, pool *pgxpool.Pool, bview string) *testEnv {
	t.Helper()
	fixtures, err := mockadvisor.LoadFixtures([]byte(fixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(mockadvisor.NewServer(fixtures, "t", log))
	t.Cleanup(srv.Close)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := t.TempDir()
	pub := &artifact.Publisher{
		Pool: pool, Store: artifact.FSStore{Root: storeRoot},
		Key: priv, MinWorkerVersion: "0.1.0", Log: log,
	}
	cfg := Config{
		BviewPath:             bview,
		MaxTargetsPerProvider: 100,
		SanityMaxDelta:        0.5,
		RetainSnapshots:       2,
	}
	return &testEnv{
		b:         New(cfg, pool, advisor.New(srv.URL, "t"), pub, log),
		pub:       pub,
		storeRoot: storeRoot,
		pubKey:    priv.Public().(ed25519.PublicKey),
	}
}

func TestPipelineEndToEnd(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	env := newBuilder(t, pool, writeBview(t))

	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	var v4, v6, asnCount int
	if err := pool.QueryRow(ctx, `select status, prefix_count_v4, prefix_count_v6, asn_count
		from routing.snapshot order by id desc limit 1`).Scan(&status, &v4, &v6, &asnCount); err != nil {
		t.Fatal(err)
	}
	if status != "published" {
		t.Fatalf("snapshot status = %s, want published", status)
	}
	// 185.0.0.0/16, 185.0.0.0/17, 185.1.0.0/24 (MOAS), 185.2.0.0/22,
	// 185.3.0.0/25 → v4=5 (10.5.0.0/16 bogon dropped, 8.8.8.0/24
	// unmonitored); v6=1.
	if v4 != 5 || v6 != 1 || asnCount != 2 {
		t.Fatalf("counts: v4=%d v6=%d asns=%d, want 5/1/2", v4, v6, asnCount)
	}

	var moas bool
	if err := pool.QueryRow(ctx, `select (flags->>'moas')::bool from routing.prefix
		where prefix = '185.1.0.0/24'`).Scan(&moas); err != nil || !moas {
		t.Fatalf("MOAS flag missing on 185.1.0.0/24 (err=%v)", err)
	}
	var bogons int
	if err := pool.QueryRow(ctx,
		`select count(*) from routing.prefix where prefix <<= '10.0.0.0/8'`).Scan(&bogons); err != nil {
		t.Fatal(err)
	}
	if bogons != 0 {
		t.Fatal("bogon prefix reached the database")
	}

	// Targets: first usable address, deduplicated across the overlapping
	// /16 and /17 (least-specific wins), none for the flagged /25.
	var addr, viaPrefix string
	if err := pool.QueryRow(ctx, `select host(t.address), p.prefix::text
		from routing.probe_target t
		join routing.prefix p on p.id = t.prefix_id
		where t.address = '185.0.0.1'`).Scan(&addr, &viaPrefix); err != nil {
		t.Fatal(err) // pgx.ErrNoRows or multiple rows both mean the dedupe broke
	}
	if viaPrefix != "185.0.0.0/16" {
		t.Fatalf("target 185.0.0.1 derived from %s, want the covering /16", viaPrefix)
	}
	var longTargets int
	if err := pool.QueryRow(ctx, `select count(*) from routing.probe_target t
		join routing.prefix p on p.id = t.prefix_id
		where p.prefix = '185.3.0.0/25'`).Scan(&longTargets); err != nil {
		t.Fatal(err)
	}
	if longTargets != 0 {
		t.Fatal("flagged long prefix got a probe target")
	}
}

func TestSecondRunSupersedesFirst(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	bview := writeBview(t)

	env := newBuilder(t, pool, bview)
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	var published, superseded int
	if err := pool.QueryRow(ctx, `select
		count(*) filter (where status = 'published'),
		count(*) filter (where status = 'superseded')
		from routing.snapshot`).Scan(&published, &superseded); err != nil {
		t.Fatal(err)
	}
	if published != 1 || superseded != 1 {
		t.Fatalf("published=%d superseded=%d, want 1/1", published, superseded)
	}
}
