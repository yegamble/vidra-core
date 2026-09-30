//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPeerTubeImportPreservesSafetyPolicies(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `ALTER TABLE "user" ADD COLUMN "nsfwPolicy" text DEFAULT 'display', ADD COLUMN "nsfwFlagsHidden" integer DEFAULT 0, ADD COLUMN "nsfwFlagsWarned" integer DEFAULT 0, ADD COLUMN "nsfwFlagsBlurred" integer DEFAULT 0;
 ALTER TABLE video ADD COLUMN "downloadEnabled" boolean DEFAULT true, ADD COLUMN "commentsPolicy" integer DEFAULT 1;
 UPDATE "user" SET "nsfwPolicy"='do_not_list' WHERE id=1;
 UPDATE "user" SET blocked=false,"nsfwFlagsWarned"=2 WHERE id=2;
 UPDATE video SET "downloadEnabled"=false,"commentsPolicy"=CASE WHEN id=1 THEN 2 ELSE 3 END;`)
	run := func(authoritative bool) *Report {
		t.Helper()
		r, err := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, MediaMode: MediaModeNone, SourceAuthoritative: authoritative}).Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := run(false)
	if got := scanStrings(t, ctx, dest, `SELECT comments_policy||':'||download_enabled::text FROM videos ORDER BY title`); strings.Join(got, ",") != "disabled:false,disabled:false" {
		t.Errorf("source video restrictions were widened: %v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT COALESCE(sensitive_content_policy,'inherit') FROM users ORDER BY username`); strings.Join(got, ",") != "hide,hide" {
		t.Errorf("source content filters were lost: %v", got)
	}
	if c := r.Entities["user_sensitive_policy"]; c == nil || c.Unsupported != 1 {
		t.Errorf("granular fallback report=%+v", c)
	}
	if c := r.Entities["video_comment_policy"]; c == nil || c.Unsupported != 1 {
		t.Errorf("approval fallback report=%+v", c)
	}
	mustExec(t, ctx, src, `UPDATE "user" SET "nsfwPolicy"='warn',"nsfwFlagsWarned"=0;UPDATE video SET "commentsPolicy"=1,"downloadEnabled"=true;`)
	run(false)
	if got := scanStrings(t, ctx, dest, `SELECT comments_policy FROM videos ORDER BY title`); strings.Join(got, ",") != "disabled,disabled" {
		t.Errorf("default replay changed policy: %v", got)
	}
	run(true)
	if got := scanStrings(t, ctx, dest, `SELECT comments_policy||':'||download_enabled::text FROM videos ORDER BY title`); strings.Join(got, ",") != "enabled:true,enabled:true" {
		t.Errorf("authoritative policy update=%v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT COALESCE(sensitive_content_policy,'inherit') FROM users ORDER BY username`); strings.Join(got, ",") != "warn,warn" {
		t.Errorf("authoritative content policy update=%v", got)
	}
	r = run(true)
	if r.Entities[KindUser].Updated != 0 || r.Entities[KindVideo].Updated != 0 {
		t.Errorf("unchanged policies caused writes: %+v", r.Entities)
	}
}

func TestPeerTubeImportRepairsLegacyPoliciesWithoutTakingNativeAccounts(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	run := func() *Report {
		t.Helper()
		r, err := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, MediaMode: MediaModeNone, SourceAuthoritative: true}).Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	run() // A source with the optional columns absent models imports made by older code.
	mustExec(t, ctx, src, `ALTER TABLE "user" ADD COLUMN "nsfwPolicy" text DEFAULT 'warn';
 ALTER TABLE video ADD COLUMN "downloadEnabled" boolean DEFAULT false,ADD COLUMN "commentsPolicy" integer DEFAULT 2;`)
	mustExec(t, ctx, dest, `UPDATE peertube_import_ledger SET created_by_import=false WHERE entity_kind='user' AND source_id='2';`)
	run()
	if got := scanStrings(t, ctx, dest, `SELECT COALESCE(sensitive_content_policy,'inherit') FROM users ORDER BY username`); strings.Join(got, ",") != "warn,inherit" {
		t.Errorf("owned/native preference boundary=%v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT comments_policy||':'||download_enabled::text FROM videos ORDER BY title`); strings.Join(got, ",") != "disabled:false,disabled:false" {
		t.Errorf("legacy restrictions not repaired: %v", got)
	}
	// Absent optional source values are no instruction to erase destination choices.
	mustExec(t, ctx, src, `ALTER TABLE video DROP COLUMN "downloadEnabled",DROP COLUMN "commentsPolicy";ALTER TABLE "user" DROP COLUMN "nsfwPolicy";`)
	run()
	if got := scanStrings(t, ctx, dest, `SELECT comments_policy||':'||download_enabled::text FROM videos ORDER BY title`); strings.Join(got, ",") != "disabled:false,disabled:false" {
		t.Errorf("absent columns erased restrictions: %v", got)
	}
}

func TestPeerTubeImportLegacyCommentPolicy(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `ALTER TABLE video ADD COLUMN "commentsEnabled" boolean;UPDATE video SET "commentsEnabled"=(id<>1);`)
	run := func() {
		t.Helper()
		_, err := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, MediaMode: MediaModeNone, SourceAuthoritative: true}).Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		got := scanStrings(t, ctx, dest, `SELECT comments_policy FROM videos ORDER BY title`)
		if strings.Join(got, ",") != want {
			t.Errorf("comments=%v, want %s", got, want)
		}
	}
	run()
	check("disabled,enabled")
	mustExec(t, ctx, src, `UPDATE video SET "commentsEnabled"=false`)
	run()
	check("disabled,disabled")
	mustExec(t, ctx, src, `ALTER TABLE video ADD COLUMN "commentsPolicy" integer DEFAULT 1`)
	run()
	check("enabled,enabled") // The enum is authoritative when both columns exist.
	mustExec(t, ctx, dest, `UPDATE videos SET comments_policy='disabled'`)
	mustExec(t, ctx, src, `ALTER TABLE video DROP COLUMN "commentsEnabled",DROP COLUMN "commentsPolicy"`)
	run()
	check("disabled,disabled")
}
