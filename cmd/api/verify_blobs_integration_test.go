//go:build integration

// End-to-end proof of the `verify-blobs` subcommand against a live PostgreSQL
// and a live S3-compatible store (MinIO via the compose "storage" profile).
// Self-skips when DATABASE_URL or S3_TEST_ENDPOINT is unset. Run:
//
//	docker compose --profile core --profile storage up -d postgres migrate minio
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	S3_TEST_ENDPOINT=localhost:9000 \
//	go test -count=1 -race -tags=integration ./cmd/api/...
//
// The exit code IS the contract deploy/restore.sh reads, and an exit code is
// not something a unit test can assert about a function — so this runs the
// built binary, in a throwaway database on a throwaway bucket, and reads what
// the process actually returned. The throwaway pair is what makes absolute
// counts assertable: this check enumerates the WHOLE database, so leftovers
// from another suite would otherwise show up as missing objects.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/blobverify"
	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/mediahash"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// verifyThrowawayDSN creates a fresh database on the DATABASE_URL server,
// migrates it to head, and drops it when the test ends.
func verifyThrowawayDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping verify-blobs integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatalf("rand: %v", err)
	}
	name := "vidra_verifyblobs_" + hex.EncodeToString(rnd)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		if _, err := admin.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("cleanup: drop temp db %s: %v", name, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	dsn := u.String()
	if err := dbmigrate.Up(dsn, nil); err != nil {
		t.Fatalf("migrate the throwaway database: %v", err)
	}
	return dsn
}

// verifyBucket is a bucket no other test uses, emptied and removed afterwards.
type verifyBucket struct {
	backend *storage.S3
	cfg     storage.S3Config
}

func newVerifyBucket(t *testing.T) verifyBucket {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping verify-blobs integration test")
	}
	useSSL := false
	if v := os.Getenv("S3_TEST_USE_SSL"); v != "" {
		useSSL, _ = strconv.ParseBool(v)
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	cfg := storage.S3Config{
		Endpoint:       endpoint,
		Bucket:         fmt.Sprintf("vidra-verify-%d", time.Now().UnixNano()),
		AccessKey:      envOr("S3_TEST_ACCESS_KEY", "vidra"),
		SecretKey:      envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"),
		UseSSL:         useSSL,
		ForcePathStyle: true,
	}
	b, err := storage.NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := b.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanCancel()
		keys, err := b.ListAllKeys(cleanCtx)
		if err != nil {
			t.Logf("cleanup: list %s: %v", cfg.Bucket, err)
			return
		}
		for _, k := range keys {
			_ = b.Delete(cleanCtx, k)
		}
	})
	return verifyBucket{backend: b, cfg: cfg}
}

func sha256Hex(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// TestVerifyBlobsAgainstPostgresAndMinIO walks the four outcomes that matter,
// in order, mutating one thing at a time so every assertion is attributable.
func TestVerifyBlobsAgainstPostgresAndMinIO(t *testing.T) {
	dsn := verifyThrowawayDSN(t)
	bucket := newVerifyBucket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect throwaway db: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// --- seed: one video with four stored files ------------------------------
	suffix := time.Now().UnixNano()
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     fmt.Sprintf("verify-%d", suffix),
		Email:        fmt.Sprintf("verify-%d@example.test", suffix),
		PasswordHash: "$2a$12$fakefakefakefakefakefakefakefakefakefakefakefakefakefe",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: u.ID, Handle: fmt.Sprintf("verify-ch-%d", suffix), DisplayName: "Verify"})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "verify", Privacy: "private", CommentsPolicy: "enabled"})
	if err != nil {
		t.Fatalf("create video: %v", err)
	}

	const (
		okBody      = "the original bytes"
		corruptBody = "the second file's original bytes"
		goneBody    = "a thumbnail"
	)
	okKey := "web-videos/" + v.ID.String() + ".mp4"
	corruptKey := "web-videos/" + v.ID.String() + ".r1.mp4"
	goneKey := "thumbnails/" + v.ID.String() + ".jpg"
	// The W2 sentinel: the hash backfill already went looking for this object
	// and the store said it was not there. It must be reported EVERY run and
	// must never be counted as a new loss.
	sentinelKey := "storyboards/" + v.ID.String() + ".jpg"

	for _, f := range []struct {
		kind, key, body, sha string
		store                bool
	}{
		{kind: "original", key: okKey, body: okBody, sha: sha256Hex(okBody), store: true},
		{kind: "rendition", key: corruptKey, body: corruptBody, sha: sha256Hex(corruptBody), store: true},
		{kind: "thumbnail", key: goneKey, body: goneBody, sha: sha256Hex(goneBody), store: true},
		{kind: "storyboard", key: sentinelKey, sha: mediahash.SentinelMissing},
	} {
		if f.store {
			if _, err := bucket.backend.Put(ctx, f.key, strings.NewReader(f.body)); err != nil {
				t.Fatalf("put %s: %v", f.key, err)
			}
		}
		if _, err := q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
			VideoID: v.ID, Kind: f.kind, StorageKey: f.key, SizeBytes: int64(len(f.body)), Sha256: f.sha,
		}); err != nil {
			t.Fatalf("create video_file %s: %v", f.key, err)
		}
	}

	// --- the binary, and how it is run ---------------------------------------
	bin := filepath.Join(t.TempDir(), "api")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	// Built explicitly rather than appended to os.Environ(): Go resolves a
	// DUPLICATED key to its FIRST occurrence, so appending DATABASE_URL after
	// the ambient one would silently point the child at the shared database.
	overrides := map[string]string{
		"DATABASE_URL":                dsn,
		"STORAGE_BACKEND":             "s3",
		"STORAGE_S3_ENDPOINT":         bucket.cfg.Endpoint,
		"STORAGE_S3_BUCKET":           bucket.cfg.Bucket,
		"STORAGE_S3_ACCESS_KEY":       bucket.cfg.AccessKey,
		"STORAGE_S3_SECRET_KEY":       bucket.cfg.SecretKey,
		"STORAGE_S3_USE_SSL":          strconv.FormatBool(bucket.cfg.UseSSL),
		"STORAGE_S3_FORCE_PATH_STYLE": "true",
	}
	env := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := overrides[k]; !replaced {
			env = append(env, kv)
		}
	}
	for k, val := range overrides {
		if val == "" {
			continue
		}
		env = append(env, k+"="+val)
	}

	run := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"verify-blobs"}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		code := cmd.ProcessState.ExitCode()
		if err != nil && code < 0 {
			t.Fatalf("run verify-blobs %v: %v\n%s", args, err, out)
		}
		t.Logf("verify-blobs %v -> exit %d\n%s", args, code, out)
		return code, string(out)
	}

	// --- 1. a consistent store exits 0, and says why the sentinel is not a loss
	t.Run("a consistent store exits 0", func(t *testing.T) {
		code, out := run(t)
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "checked 4 referenced object(s)") {
			t.Errorf("did not check the four seeded rows:\n%s", out)
		}
		if !strings.Contains(out, "MISSING:        0") {
			t.Errorf("reported a missing object in a clean store:\n%s", out)
		}
		if !strings.Contains(out, "known missing:  1") || !strings.Contains(out, sentinelKey) {
			t.Errorf("the sentinel row was not reported as known-missing:\n%s", out)
		}
		if !strings.Contains(out, "consistent:") {
			t.Errorf("a clean store did not report itself consistent:\n%s", out)
		}
	})

	// --- 2. one object deleted: exit 3, and the key is named ------------------
	t.Run("a deleted object exits 3 and is named", func(t *testing.T) {
		if err := bucket.backend.Delete(ctx, goneKey); err != nil {
			t.Fatalf("delete %s: %v", goneKey, err)
		}
		code, out := run(t)
		if code != exitInconsistent {
			t.Fatalf("exit = %d, want %d\n%s", code, exitInconsistent, out)
		}
		if !strings.Contains(out, goneKey) {
			t.Errorf("the missing key was not named:\n%s", out)
		}
		if !strings.Contains(out, "MISSING:        1") {
			t.Errorf("missing count wrong:\n%s", out)
		}
		// The two absent objects are the SAME fact to the bucket and different
		// facts to an operator. Collapsing them would make the pre-existing
		// dangling row indistinguishable from the one this restore lost.
		if !strings.Contains(out, "known missing:  1") {
			t.Errorf("the sentinel row was folded into the new loss:\n%s", out)
		}
		if strings.Contains(out, "\nconsistent:") {
			t.Errorf("a store with a lost object called itself consistent:\n%s", out)
		}
		// Put it back for the remaining subtests.
		if _, err := bucket.backend.Put(ctx, goneKey, strings.NewReader(goneBody)); err != nil {
			t.Fatalf("restore %s: %v", goneKey, err)
		}
	})

	// --- 3. corruption is invisible without --hash and loud with it ----------
	t.Run("corruption needs --hash", func(t *testing.T) {
		if _, err := bucket.backend.Put(ctx, corruptKey, strings.NewReader("TAMPERED — not the bytes that were written")); err != nil {
			t.Fatalf("overwrite %s: %v", corruptKey, err)
		}
		if code, out := run(t); code != 0 {
			t.Fatalf("the existence-only pass must not read bytes: exit = %d\n%s", code, out)
		}
		code, out := run(t, "--hash")
		if code != exitInconsistent {
			t.Fatalf("exit = %d, want %d\n%s", code, exitInconsistent, out)
		}
		if !strings.Contains(out, "MISMATCHED:     1") || !strings.Contains(out, corruptKey) {
			t.Errorf("corruption was not reported loudly:\n%s", out)
		}
		if !strings.Contains(out, "CORRUPT") {
			t.Errorf("the mismatch section is not labelled as corruption:\n%s", out)
		}
	})

	// --- 4. --json is a document a machine can read --------------------------
	t.Run("--json parses", func(t *testing.T) {
		code, out := run(t, "--hash", "--deep", "--json")
		if code != exitInconsistent {
			t.Fatalf("exit = %d, want %d\n%s", code, exitInconsistent, out)
		}
		var doc struct {
			blobverify.Report
			StorageMigrationActive bool `json:"storage_migration_active"`
			ExitCode               int  `json:"exit_code"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("the --json output is not one JSON document: %v\n%s", err, out)
		}
		if doc.Checked != 4 || doc.Mismatched != 1 || doc.KnownMissing != 1 || doc.Missing != 0 {
			t.Errorf("json counts = %+v", doc.Report)
		}
		if doc.ExitCode != exitInconsistent {
			t.Errorf("exit_code = %d, want %d", doc.ExitCode, exitInconsistent)
		}
		if doc.StorageMigrationActive {
			t.Errorf("no campaign was started, but the document says one is in flight")
		}
		if len(doc.MismatchedKeys) != 1 || doc.MismatchedKeys[0] != corruptKey {
			t.Errorf("mismatched_keys = %v", doc.MismatchedKeys)
		}
	})

	// --- 5. the engine itself, in process, so -race covers the worker pool ---
	t.Run("the engine is race-clean under concurrency", func(t *testing.T) {
		rep, err := blobverify.Verify(ctx, q, bucket.backend, blobverify.Options{Hash: true, Deep: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if rep.Checked != 4 || rep.Mismatched != 1 || rep.KnownMissing != 1 {
			t.Fatalf("in-process report disagreed with the binary: %+v", rep)
		}
	})

	// --- 6. an unreachable bucket is exit 1, not exit 3 ----------------------
	t.Run("a bucket that is not there exits 1", func(t *testing.T) {
		bad := make([]string, 0, len(env))
		for _, kv := range env {
			if strings.HasPrefix(kv, "STORAGE_S3_BUCKET=") {
				continue
			}
			bad = append(bad, kv)
		}
		bad = append(bad, "STORAGE_S3_BUCKET=vidra-does-not-exist-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		cmd := exec.Command(bin, "verify-blobs")
		cmd.Env = bad
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Fatalf("exit = %d, want 1 (could not check)\n%s", code, out)
		}
		if !strings.Contains(string(out), "media store is unreachable") {
			t.Errorf("the failure was not explained:\n%s", out)
		}
	})
}
