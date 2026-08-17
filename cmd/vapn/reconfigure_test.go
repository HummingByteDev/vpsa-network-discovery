package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reconfiguration must be safe to run repeatedly on a live worker: it may not
// duplicate settings, invent credentials, reveal secrets, or lose anything the
// operator put in the file by hand.

func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VAPN_HOME", dir)
	return dir
}

func TestMaskShowsEnoughToRecogniseAndNoMore(t *testing.T) {
	secret := "cwtSECRETMIDDLEZ8YZwsa4"
	got := mask(secret)
	if strings.Contains(got, "SECRETMIDDLE") {
		t.Fatalf("mask leaked the secret body: %q", got)
	}
	if !strings.HasPrefix(got, "cwtSE") || !strings.HasSuffix(got, "Zwsa4") {
		t.Fatalf("mask = %q, want recognisable head and tail", got)
	}
	if mask("") != "" {
		t.Fatalf("mask of an unset value = %q, want empty", mask(""))
	}
	// A short value is not partially revealed.
	if strings.ContainsAny(mask("short"), "short") {
		t.Fatalf("short secret leaked: %q", mask("short"))
	}
}

// Answering every prompt with Enter must leave every value exactly as it was —
// the operator should never have to know a credential to change something else.
func TestPromptConfigKeepsExistingValuesOnEmptyAnswers(t *testing.T) {
	withHome(t)
	before := conf{
		"VAPN_COORDINATOR_URL":     "https://probes.example.com",
		"VAPN_WORKER_NAME":         "helsinki-1",
		"VAPN_SNAPSHOT_PUBLIC_KEY": "TbP5tsomethinglongla/rw=",
		"VAPN_WORKER_IMAGE":        "ghcr.io/hummingbytedev/vapn-worker:v1",
		"VAPN_ENROLLMENT_TOKEN":    "cwtTOKENVALUEZ8YZwsa4",
	}
	after := conf{}
	for k, v := range before {
		after[k] = v
	}
	promptConfig(bufio.NewReader(strings.NewReader("\n\n\n\n")), after, true)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed to %q, want %q", k, after[k], v)
		}
	}
}

func TestPromptConfigAcceptsReplacements(t *testing.T) {
	withHome(t)
	c := conf{
		"VAPN_COORDINATOR_URL":     "https://old.example.com",
		"VAPN_WORKER_NAME":         "old-name",
		"VAPN_SNAPSHOT_PUBLIC_KEY": "oldkeyoldkeyoldkey=",
		"VAPN_ENROLLMENT_TOKEN":    "old-token",
	}
	answers := "https://new.example.com\nnew-name\nnewkeynewkeynewkey=\nnew-token\n"
	promptConfig(bufio.NewReader(strings.NewReader(answers)), c, true)
	want := conf{
		"VAPN_COORDINATOR_URL":     "https://new.example.com",
		"VAPN_WORKER_NAME":         "new-name",
		"VAPN_SNAPSHOT_PUBLIC_KEY": "newkeynewkeynewkey=",
		"VAPN_ENROLLMENT_TOKEN":    "new-token",
	}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %q, want %q", k, c[k], v)
		}
	}
}

// A worker that already has an identity is never asked for an enrollment token
// again: the token is one-time, and re-enrolling would throw away the identity
// and its trust history.
func TestRegisteredWorkerIsNotAskedForAToken(t *testing.T) {
	dir := withHome(t)
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if registered() {
		t.Fatal("reported registered before any identity exists")
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "worker.id"),
		[]byte("9f30"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !registered() {
		t.Fatal("did not notice the stored worker identity")
	}

	// With askToken=false the remaining answers must not slide onto the token
	// prompt — three answers, three settings.
	c := conf{"VAPN_ENROLLMENT_TOKEN": "already-spent"}
	promptConfig(bufio.NewReader(strings.NewReader("https://a\nb\nc=\n")), c, false)
	if c["VAPN_COORDINATOR_URL"] != "https://a" || c["VAPN_WORKER_NAME"] != "b" ||
		c["VAPN_SNAPSHOT_PUBLIC_KEY"] != "c=" {
		t.Fatalf("prompts misaligned: %+v", c)
	}
}

// Running reconfigure repeatedly must produce a byte-identical file: no
// appended duplicates, no reordering, no regenerated values.
func TestSaveIsIdempotent(t *testing.T) {
	withHome(t)
	c := conf{
		"VAPN_COORDINATOR_URL":     "https://probes.example.com",
		"VAPN_WORKER_NAME":         "helsinki-1",
		"VAPN_SNAPSHOT_PUBLIC_KEY": "TbP5tsomethinglongla/rw=",
		"VAPN_WORKER_IMAGE":        "ghcr.io/hummingbytedev/vapn-worker:v1",
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		reloaded := loadConfig(true)
		if err := reloaded.save(); err != nil {
			t.Fatal(err)
		}
	}
	second, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("config drifted across saves:\n--- first ---\n%s\n--- later ---\n%s", first, second)
	}
	for _, line := range strings.Split(string(second), "\n") {
		if strings.Count(string(second), strings.SplitN(line, "=", 2)[0]+"=") > 1 && line != "" {
			t.Fatalf("duplicate setting in config: %q", line)
		}
	}
}

// Settings an operator added by hand survive a reconfiguration.
func TestSavePreservesUnmanagedSettings(t *testing.T) {
	dir := withHome(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.env"), []byte(
		"VAPN_COORDINATOR_URL=https://probes.example.com\nVAPN_LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := loadConfig(true)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	got := loadConfig(true)
	if got["VAPN_LOG_LEVEL"] != "debug" {
		t.Fatalf("hand-added setting lost: %+v", got)
	}
}

// A spent enrollment token is removed from disk, not left behind as a stale
// secret; the file stays valid without it.
func TestSpentTokenIsDroppedCleanly(t *testing.T) {
	withHome(t)
	c := conf{
		"VAPN_COORDINATOR_URL":  "https://probes.example.com",
		"VAPN_ENROLLMENT_TOKEN": "one-time",
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	delete(c, "VAPN_ENROLLMENT_TOKEN")
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "one-time") {
		t.Fatalf("spent token still on disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), "VAPN_COORDINATOR_URL=") {
		t.Fatalf("dropping the token damaged the file:\n%s", raw)
	}
}
