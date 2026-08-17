package artifact

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
)

// envSource builds a config.Source over an explicit environment, so these
// tests read the same VAPN_ARTIFACT_* names an operator writes into .env.
func envSource(t *testing.T, kv map[string]string) *config.Source {
	t.Helper()
	for k, v := range kv {
		t.Setenv("VAPN_"+k, v)
	}
	return config.New()
}

// TestStoreFromConfigSelectsBackend covers the branch an operator lands in by
// setting, or forgetting, one variable. "Nothing configured" is the quiet one:
// it returns no store and no error, which for the builder means a snapshot is
// built and never uploaded — exactly the "empty bucket" symptom.
func TestStoreFromConfigSelectsBackend(t *testing.T) {
	t.Run("nothing configured yields no store and no error", func(t *testing.T) {
		store, err := StoreFromConfig(envSource(t, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store != nil {
			t.Fatalf("got a store from an empty configuration: %T", store)
		}
	})

	t.Run("s3 endpoint yields an S3 store", func(t *testing.T) {
		store, err := StoreFromConfig(envSource(t, map[string]string{
			"ARTIFACT_S3_ENDPOINT":   "s3.us-east-005.backblazeb2.com",
			"ARTIFACT_S3_REGION":     "us-east-005",
			"ARTIFACT_S3_BUCKET":     "vapn-artifacts",
			"ARTIFACT_S3_ACCESS_KEY": "keyid",
			"ARTIFACT_S3_SECRET_KEY": "secret",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		s3, ok := store.(*S3Store)
		if !ok {
			t.Fatalf("got %T, want *S3Store", store)
		}
		if s3.bucket != "vapn-artifacts" {
			t.Fatalf("bucket = %q", s3.bucket)
		}
	})

	t.Run("dir yields a filesystem store", func(t *testing.T) {
		store, err := StoreFromConfig(envSource(t, map[string]string{
			"ARTIFACT_DIR": t.TempDir(),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := store.(FSStore); !ok {
			t.Fatalf("got %T, want FSStore", store)
		}
	})

	t.Run("both backends is a startup error", func(t *testing.T) {
		_, err := StoreFromConfig(envSource(t, map[string]string{
			"ARTIFACT_S3_ENDPOINT": "s3.us-east-005.backblazeb2.com",
			"ARTIFACT_DIR":         t.TempDir(),
		}))
		if err == nil {
			t.Fatal("configuring both S3 and a directory was accepted")
		}
	})
}

// TestS3EndpointAcceptsASchemeByMistake: every example documents a bare host,
// but pasting the console URL with https:// is the obvious slip. minio-go
// treats a scheme in the endpoint as part of the hostname and fails at request
// time, deep inside a build; the store strips it instead.
func TestS3EndpointAcceptsASchemeByMistake(t *testing.T) {
	for _, ep := range []string{
		"s3.us-east-005.backblazeb2.com",
		"https://s3.us-east-005.backblazeb2.com",
		"http://s3.us-east-005.backblazeb2.com",
	} {
		if _, err := NewS3Store(S3Config{
			Endpoint: ep, AccessKey: "k", SecretKey: "s",
			Bucket: "vapn-artifacts", Region: "us-east-005", UseSSL: true,
		}); err != nil {
			t.Errorf("NewS3Store(%q): %v", ep, err)
		}
	}
}

// TestS3StoreRoundTrip exercises the real upload path against an S3-compatible
// server, using the configuration shape production uses (bare host endpoint,
// explicit region, credentials as access/secret). Gated on a live endpoint:
//
//	docker run -d -p 9010:9000 -e MINIO_ROOT_USER=testkey \
//	  -e MINIO_ROOT_PASSWORD=testsecret123 minio/minio server /data
//	VAPN_TEST_S3_ENDPOINT=127.0.0.1:9010 VAPN_TEST_S3_ACCESS_KEY=testkey \
//	  VAPN_TEST_S3_SECRET_KEY=testsecret123 go test ./internal/artifact/
func TestS3StoreRoundTrip(t *testing.T) {
	endpoint := os.Getenv("VAPN_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("VAPN_TEST_S3_ENDPOINT not set; skipping S3 integration test")
	}
	bucket := os.Getenv("VAPN_TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "vapn-artifacts"
	}
	store, err := NewS3Store(S3Config{
		Endpoint:  endpoint,
		AccessKey: os.Getenv("VAPN_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("VAPN_TEST_S3_SECRET_KEY"),
		Bucket:    bucket,
		Region:    os.Getenv("VAPN_TEST_S3_REGION"),
		UseSSL:    os.Getenv("VAPN_TEST_S3_USE_SSL") == "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := ObjectKeyManifest("20260817T0000Z-test")
	body := []byte(`{"version":"20260817T0000Z-test"}`)

	if err := store.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "application/json"); err != nil {
		t.Fatalf("put: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })

	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("read back %q, want %q", got, body)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// A missing object must surface as an error, not an empty read: the
	// coordinator's snapshot handler distinguishes the two.
	if _, err := store.Get(ctx, key); err == nil {
		t.Fatal("reading a deleted object succeeded")
	} else if !strings.Contains(strings.ToLower(err.Error()), "not exist") &&
		!strings.Contains(strings.ToLower(err.Error()), "no such key") {
		t.Logf("delete-then-get error (accepted): %v", err)
	}
}
